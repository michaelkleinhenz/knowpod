package net.kleinhenz.knowpod.pocket;

import java.io.File;
import java.io.IOException;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

// WifiSync copies new recordings from a Pocket recorder, as the Pocket app does: short ones
// over Bluetooth, long ones over the recorder's WiFi. The port of
// desktop/src/pocket-wifi-sync.js, with the Bluetooth transfer added. The order:
//
//   1. Bluetooth: connect, check firmware (1.8) and battery, list the recordings.
//   2. Ask the server which of them aren't notes yet.
//   3. Download those into a temporary folder: the short ones over Bluetooth
//      (BluetoothTransfer), then the long ones over the recorder's WiFi (WifiSession): raise
//      its access point, join it, download, lower it. Raising and joining takes longer than a
//      short recording takes over Bluetooth. The phone joins the recorder's network next to its
//      usual one (Android's local-only connection), so it stays online. When the phone can't
//      join (or has no WiFi to offer), the long ones come over Bluetooth too.
//   4. Upload them like the desktop app does; the server puts them into the folder "Pocket AI".
//
// state() is what the Pocket Sync dialog shows (frontend/src/components/PocketUsbSync.tsx):
// phase is '' (never run), connecting, listing, checking, wifi-starting, downloading (file
// current of total, bytes of totalBytes at rate bytes/s, via bluetooth or wifi),
// wifi-restarting, reconnecting, uploading (current of total), done (copied, failed; found
// recordings on the recorder, incomplete if its listing came back short) or failed (error,
// message).
public final class WifiSync {
    // Env is what the copy needs from the app.
    public interface Env {
        // serverUrl is the knowpod server, or null.
        String serverUrl();

        // openSession connects to the recorder over Bluetooth and unlocks it.
        PocketSession openSession() throws PocketException, InterruptedException;

        // wifi is this device's WiFi, or null if it can't join the recorder's network.
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
    // Recordings up to this size come over Bluetooth (about 65 KB/s on a phone, so 2.4 MB, ten
    // minutes of recording, take about 40 s); longer ones over WiFi (about 1 MB/s, after
    // 15 to 60 s for raising the access point and joining it). The Pocket app does the same
    // with a threshold of its own: 3 minutes came over Bluetooth, 45 minutes over WiFi.
    static long bluetoothMaxBytes = 2_400_000;
    // The failures of raising the access point and joining it after which the long recordings
    // come over Bluetooth instead.
    private static final Set<String> WIFI_FALLBACK = new HashSet<>(Arrays.asList(
            "wifi-unsupported", "wifi-off", "wifi-setup", "wifi-join", "wifi-ap"));
    // After the transfer, how long the server may take to be reachable.
    static long reconnectTimeout = 90_000;
    static long reconnectRetry = 3_000;

    private final Env env;
    private final String host;
    private final int port;
    private final WifiSession.Timings timings;
    private final BluetoothTransfer.Timings bluetoothTimings;
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
    private String via = "";
    private int copied;
    private int failed;
    private int found;
    private boolean incomplete;
    private String error = "";
    private String message = "";

    public WifiSync(Env env) {
        this(env, WifiSession.HOST, WifiSession.PORT, new WifiSession.Timings(), new BluetoothTransfer.Timings());
    }

    WifiSync(Env env, String host, int port, WifiSession.Timings timings, BluetoothTransfer.Timings bluetoothTimings) {
        this.env = env;
        this.host = host;
        this.port = port;
        this.timings = timings;
        this.bluetoothTimings = bluetoothTimings;
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
            s.put("via", via);
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
            via = "";
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
        lastFailure = null;
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

            if (!tempDir.isDirectory() && !tempDir.mkdirs()) throw new PocketException("failed", "Couldn't create " + tempDir);
            HostWifi wifi = env.wifi();
            List<PocketSession.Recording> overBluetooth = new ArrayList<>();
            List<PocketSession.Recording> overWifi = new ArrayList<>();
            for (PocketSession.Recording r : todo) {
                if (wifi != null && (long) r.seconds * BYTES_PER_SECOND > bluetoothMaxBytes) overWifi.add(r);
                else overBluetooth.add(r);
            }
            env.log(overBluetooth.size() + " over Bluetooth, " + overWifi.size() + " over WiFi");
            set(() -> {
                current = 0;
                total = todo.size();
            });

            List<PocketSession.Recording> downloaded = new ArrayList<>();
            int number = 0;
            BluetoothTransfer bluetooth = new BluetoothTransfer(ble, bluetoothTimings);
            for (PocketSession.Recording f : overBluetooth) {
                check();
                if (download(f, ++number, "bluetooth", tempDir, (r, file, progress) -> bluetooth.download(r, file, progress))) {
                    downloaded.add(f);
                } else {
                    failedNow++;
                }
            }

            WifiSession wifiSession = null;
            if (!overWifi.isEmpty()) {
                check();
                set(() -> phase = "wifi-starting");
                wifiSession = new WifiSession(ble, wifi, host, port, env::log, () -> set(() -> phase = "wifi-restarting"), timings);
                synchronized (lock) {
                    session = wifiSession;
                }
                try {
                    wifiSession.start();
                } catch (PocketException e) {
                    if (!WIFI_FALLBACK.contains(e.code) || cancelled) throw e;
                    // As the Pocket app does: the long ones come over Bluetooth, slower.
                    env.log("WiFi: " + e.getMessage() + "; copying over Bluetooth instead");
                    closeSession();
                    wifiSession = null;
                }
            }
            for (PocketSession.Recording f : overWifi) {
                check();
                final WifiSession ws = wifiSession;
                boolean ok = ws != null
                        ? download(f, ++number, "wifi", tempDir, (r, file, progress) -> ws.download(r, file, progress))
                        : download(f, ++number, "bluetooth", tempDir, (r, file, progress) -> bluetooth.download(r, file, progress));
                if (ok) {
                    downloaded.add(f);
                } else {
                    failedNow++;
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
            final PocketException last = this.lastFailure;
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

    // Download is one way of transferring a recording into file.
    private interface Download {
        void run(PocketSession.Recording recording, File file, Transfer.Progress progress)
                throws PocketException, InterruptedException, IOException;
    }

    // Why the last recording that failed to transfer failed (download).
    private PocketException lastFailure;

    // download transfers recording, number index of the copy, into tempDir via how, showing
    // the progress. Returns false if this one recording failed (lastFailure says why); throws
    // when nothing more can be transferred.
    private boolean download(PocketSession.Recording f, int index, String how, File tempDir, Download transfer)
            throws PocketException, InterruptedException {
        File file = new File(tempDir, f.name());
        long started = System.currentTimeMillis();
        set(() -> {
            phase = "downloading";
            via = how;
            current = index;
            bytes = 0;
            totalBytes = (long) f.seconds * BYTES_PER_SECOND;
            rate = 0;
        });
        try {
            transfer.run(f, file, (received, size) -> {
                double seconds = (System.currentTimeMillis() - started) / 1000.0;
                set(() -> {
                    phase = "downloading";
                    bytes = received;
                    totalBytes = size;
                    rate = seconds > 0.5 ? received / seconds : 0;
                });
            });
            return true;
        } catch (PocketException e) {
            if (cancelled || e.code.equals("cancelled")) throw new PocketException("cancelled");
            // The Bluetooth connection is gone, or the recorder stopped answering: nothing more
            // can be transferred.
            if (e.code.equals("disconnected") || e.code.equals("timeout") || e.code.equals("stuck")) throw e;
            lastFailure = e;
            env.log(f.name() + " (" + how + "): " + e.getMessage());
            return false;
        } catch (IOException e) {
            if (cancelled) throw new PocketException("cancelled");
            lastFailure = new PocketException("transfer", e.getMessage(), e);
            env.log(f.name() + " (" + how + "): " + e);
            return false;
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
