package net.kleinhenz.knowpod.pocket;

import java.io.File;
import java.io.IOException;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

// WifiSync copies new recordings from a Pocket recorder over its WiFi, the port of
// desktop/src/pocket-wifi-sync.js. The order:
//
//   1. Bluetooth: connect, check firmware (1.8) and battery, list the recordings.
//   2. Ask the server which of them aren't notes yet.
//   3. Raise the recorder's access point, join it and download those (WifiSession) into a
//      temporary folder; then lower it. The phone joins the recorder's network next to its
//      usual one (Android's local-only connection), so it stays online.
//   4. Upload them like the desktop app does; the server puts them into the folder "Pocket AI".
//
// state() is what the Pocket Sync dialog shows (frontend/src/components/PocketUsbSync.tsx):
// phase is '' (never run), connecting, listing, checking, wifi-starting, downloading (file
// current of total, bytes of totalBytes at rate bytes/s), wifi-restarting, reconnecting,
// uploading (current of total), done (copied, failed; found recordings on the recorder,
// incomplete if its listing came back short) or failed (error, message).
public final class WifiSync {
    // Env is what the copy needs from the app.
    public interface Env {
        // serverUrl is the knowpod server, or null.
        String serverUrl();

        // openSession connects to the recorder over Bluetooth and unlocks it.
        PocketSession openSession() throws PocketException, InterruptedException;

        HostWifi wifi();

        ServerApi api();

        // remembered are the files copied to server before (not asked about again, so a
        // note deleted for good isn't copied again either); remember adds one.
        Set<String> remembered(String server);

        void remember(String server, String name);

        File tempDir();

        void log(String text);

        // changed: state() changed.
        void changed();

        // finished: the copy ended (state() has how); lastId is the note of the last
        // recording copied.
        void finished(String lastId);
    }

    // Bytes per second of a recording (32 kbps MP3), to estimate sizes before the transfer.
    static final int BYTES_PER_SECOND = 4_000;
    // After the transfer, how long the server may take to be reachable.
    static long reconnectTimeout = 90_000;
    static long reconnectRetry = 3_000;

    private final Env env;
    private final String host;
    private final int port;
    private final WifiSession.Timings timings;
    private final Object lock = new Object();
    private boolean running;
    private volatile boolean cancelled;
    private Thread worker;
    private WifiSession session;

    private String phase = "";
    private int current;
    private int total;
    private long bytes;
    private long totalBytes;
    private double rate;
    private int copied;
    private int failed;
    private int found;
    private boolean incomplete;
    private String error = "";
    private String message = "";

    public WifiSync(Env env) {
        this(env, WifiSession.HOST, WifiSession.PORT, new WifiSession.Timings());
    }

    WifiSync(Env env, String host, int port, WifiSession.Timings timings) {
        this.env = env;
        this.host = host;
        this.port = port;
        this.timings = timings;
    }

    public boolean running() {
        synchronized (lock) {
            return running;
        }
    }

    public Map<String, Object> state() {
        synchronized (lock) {
            Map<String, Object> s = new LinkedHashMap<>();
            s.put("running", running);
            s.put("cancelling", running && cancelled);
            s.put("phase", phase);
            s.put("current", current);
            s.put("total", total);
            s.put("bytes", bytes);
            s.put("totalBytes", totalBytes);
            s.put("rate", rate);
            s.put("copied", copied);
            s.put("failed", failed);
            s.put("found", found);
            s.put("incomplete", incomplete);
            s.put("error", error);
            s.put("message", message);
            return s;
        }
    }

    // start runs a copy on a thread of its own unless one runs already.
    public boolean start() {
        synchronized (lock) {
            if (running) return false;
            running = true;
            cancelled = false;
            phase = "connecting";
            current = total = copied = failed = found = 0;
            bytes = totalBytes = 0;
            rate = 0;
            incomplete = false;
            error = message = "";
            worker = new Thread(this::run, "pocket-wifi-sync");
        }
        env.changed();
        worker.start();
        return true;
    }

    // cancel stops the copy that runs; the WiFi and the recorder are put back first.
    public void cancel() {
        Thread thread;
        WifiSession current;
        synchronized (lock) {
            if (!running) return;
            cancelled = true;
            thread = worker;
            current = session;
        }
        if (current != null) current.cancel();
        // Ends a wait for the network, an answer or a pause.
        if (thread != null) thread.interrupt();
        env.changed();
    }

    // join waits for the copy to end (tests).
    void join() throws InterruptedException {
        Thread thread;
        synchronized (lock) {
            thread = worker;
        }
        if (thread != null) thread.join();
    }

    private interface Update {
        void apply();
    }

    private void set(Update update) {
        synchronized (lock) {
            update.apply();
        }
        env.changed();
    }

    private void check() throws PocketException {
        if (cancelled) throw new PocketException("cancelled");
    }

    // upload sends one file to the server, waiting for it to be reachable. Returns the note's
    // id (or "" if it already was one).
    private String upload(String server, File file, String name) throws PocketException, InterruptedException, IOException {
        long deadline = System.currentTimeMillis() + reconnectTimeout;
        for (;;) {
            try {
                return env.api().upload(server, file, name);
            } catch (ServerApi.HttpException e) {
                if (e.status == 409) return ""; // already a note
                if (e.status == 401) throw new PocketException("signed-out", e.getMessage());
                throw new PocketException("server", e.getMessage(), e);
            } catch (IOException e) {
                // Not reachable yet.
                if (System.currentTimeMillis() >= deadline) throw new PocketException("offline", e.getMessage(), e);
                Thread.sleep(reconnectRetry);
            }
        }
    }

    private void run() {
        File tempDir = env.tempDir();
        PocketSession ble = null;
        int copiedNow = 0;
        int failedNow = 0;
        PocketException lastFailure = null;
        String lastId = "";
        try {
            String server = env.serverUrl();
            if (server == null || server.isEmpty()) throw new PocketException("no-server");

            ble = env.openSession();
            check();
            String firmware = ble.command("FW", "FW").trim();
            if (!firmware.startsWith("1.8")) throw new PocketException("firmware", firmware);
            Integer battery = PocketSession.parseInt(ble.command("BAT", "BAT"));
            if (battery != null && battery < 10) throw new PocketException("battery", String.valueOf(battery));

            set(() -> phase = "listing");
            Set<String> known = env.remembered(server);
            PocketSession.Listing listing = ble.listRecordings();
            List<PocketSession.Recording> files = new ArrayList<>();
            StringBuilder names = new StringBuilder();
            for (PocketSession.Recording r : listing.recordings) {
                if (!known.contains(r.name())) files.add(r);
                names.append(' ').append(r.name());
            }
            final int listed = listing.recordings.size();
            set(() -> {
                found = listed;
                incomplete = listing.incomplete;
            });
            env.log(listed + " recordings on " + listing.days + " days" + (listing.incomplete ? " (listing incomplete)" : "") + ", "
                    + (listed - files.size()) + " copied before:" + names);
            check();

            set(() -> phase = "checking");
            Set<String> wanted = new HashSet<>();
            if (!files.isEmpty()) {
                List<String> asked = new ArrayList<>();
                for (PocketSession.Recording r : files) asked.add(r.name());
                try {
                    wanted.addAll(env.api().newFiles(server, asked));
                } catch (ServerApi.HttpException e) {
                    if (e.status == 401) throw new PocketException("signed-out", e.getMessage());
                    throw new PocketException("server", e.getMessage(), e);
                } catch (IOException e) {
                    throw new PocketException("offline", e.getMessage(), e);
                }
            }
            List<PocketSession.Recording> todo = new ArrayList<>();
            for (PocketSession.Recording r : files) {
                if (wanted.contains(r.name())) todo.add(r);
                else env.remember(server, r.name());
            }
            env.log((files.size() - todo.size()) + " already notes, " + todo.size() + " new");
            if (todo.isEmpty()) {
                set(() -> {
                    phase = "done";
                    copied = 0;
                    failed = 0;
                });
                return;
            }
            check();

            set(() -> {
                phase = "wifi-starting";
                current = 0;
                total = todo.size();
            });
            if (!tempDir.isDirectory() && !tempDir.mkdirs()) throw new PocketException("failed", "Couldn't create " + tempDir);
            WifiSession wifiSession = new WifiSession(ble, env.wifi(), host, port, env::log,
                    () -> set(() -> phase = "wifi-restarting"), timings);
            synchronized (lock) {
                session = wifiSession;
            }
            check();
            wifiSession.start();

            List<PocketSession.Recording> downloaded = new ArrayList<>();
            for (int i = 0; i < todo.size(); i++) {
                check();
                PocketSession.Recording f = todo.get(i);
                File file = new File(tempDir, f.name());
                long started = System.currentTimeMillis();
                final int index = i + 1;
                set(() -> {
                    phase = "downloading";
                    current = index;
                    bytes = 0;
                    totalBytes = (long) f.seconds * BYTES_PER_SECOND;
                    rate = 0;
                });
                try {
                    wifiSession.download(f, file, (received, size) -> {
                        double seconds = (System.currentTimeMillis() - started) / 1000.0;
                        set(() -> {
                            phase = "downloading";
                            bytes = received;
                            totalBytes = size;
                            rate = seconds > 0.5 ? received / seconds : 0;
                        });
                    });
                    downloaded.add(f);
                } catch (PocketException e) {
                    if (e.code.equals("cancelled")) throw e;
                    // The Bluetooth connection is gone, or the recorder stopped answering WiFi
                    // commands: nothing more can be transferred.
                    if (e.code.equals("disconnected") || e.code.equals("timeout") || e.code.equals("stuck")) throw e;
                    failedNow++;
                    lastFailure = e;
                    env.log(f.name() + ": " + e.getMessage());
                } catch (IOException e) {
                    if (cancelled) throw new PocketException("cancelled");
                    failedNow++;
                    lastFailure = new PocketException("transfer", e.getMessage(), e);
                    env.log(f.name() + ": " + e);
                }
            }

            set(() -> phase = "reconnecting");
            closeSession();
            ble.close();
            ble = null;

            for (int i = 0; i < downloaded.size(); i++) {
                check();
                PocketSession.Recording f = downloaded.get(i);
                final int index = i + 1;
                set(() -> {
                    phase = "uploading";
                    current = index;
                    total = downloaded.size();
                });
                File file = new File(tempDir, f.name());
                String id = upload(server, file, f.name());
                if (!id.isEmpty()) lastId = id;
                env.remember(server, f.name());
                copiedNow++;
                final int copiedSoFar = copiedNow;
                set(() -> copied = copiedSoFar);
                file.delete();
            }
            final int c = copiedNow;
            final int fl = failedNow;
            final PocketException last = lastFailure;
            set(() -> {
                phase = "done";
                copied = c;
                failed = fl;
                error = fl > 0 ? last.code : "";
                message = fl > 0 ? String.valueOf(last.getMessage()) : "";
            });
        } catch (Exception e) {
            String code = cancelled ? "cancelled" : PocketException.codeOf(e, e instanceof InterruptedException ? "cancelled" : "failed");
            final int c = copiedNow;
            set(() -> {
                phase = "failed";
                error = code;
                message = String.valueOf(e.getMessage());
                copied = c;
            });
            if (!code.equals("cancelled")) env.log("failed: " + e);
        } finally {
            // Always: lower the access point, put the WiFi back, end the Bluetooth session, and
            // don't leave recordings lying around in the temporary folder. A cancel may have
            // interrupted this thread; the clean-up needs its waits.
            Thread.interrupted();
            closeSession();
            if (ble != null) ble.close();
            deleteTree(tempDir);
            synchronized (lock) {
                running = false;
            }
            env.changed();
            env.finished(lastId);
        }
    }

    private void closeSession() {
        WifiSession current;
        synchronized (lock) {
            current = session;
            session = null;
        }
        if (current != null) current.close();
    }

    private static void deleteTree(File file) {
        File[] children = file.listFiles();
        if (children != null) for (File child : children) deleteTree(child);
        file.delete();
    }
}
