package net.kleinhenz.knowpod.pocket;

import java.io.File;
import java.io.IOException;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.TimeUnit;
import java.util.function.Consumer;

// WifiSession transfers recordings from a Pocket recorder over its WiFi access point, about
// 1 MB/s instead of Bluetooth's tens of KB/s. Decoded on firmware 1.8 / WiFi firmware V9
// (tools/pocket-wifi-probe/RESEARCH.md has the details); the port of createWifiSession in
// desktop/src/pocket-wifi.js:
//
//   1. Over Bluetooth: WIFIO raises the access point, WIFI gives its name and password, and
//      WIFIS goes 3 (starting), 2 (waiting for a client), 1 (a client has joined). The network
//      is hidden, WPA2-PSK; the recorder is 192.168.200.1 and hands out 192.168.200.2.
//   2. Per file: connect to 192.168.200.1:8475 and send nothing; request the file as a
//      Bluetooth transfer (U&<date>&<timestamp> → MCU&U&<size>), and 0.3 s later switch it to
//      WiFi (U&WIFI → MCU&U&WIFI). The socket then carries the MP3 file, exactly <size> bytes,
//      and a fixed 10-byte end marker; MCU&OFF comes over Bluetooth. Close the connection.
//   3. The recorder serves two connections per access point session; then 8475 stops
//      listening until the access point is restarted (WIFIC, WIFIO). U&WIFI must never be
//      sent without a connection open: the recorder hangs until it reports MCU&SHUT.
public final class WifiSession {
    public static final String HOST = "192.168.200.1";
    public static final int PORT = 8475;
    static final int FILES_PER_SESSION = 2;

    // Timings, ms. settle: how long a new connection must stay open before a file is requested
    // on it (the recorder sometimes accepts a connection and resets it right away);
    // reconnectPause: the pause between closing one connection and opening the next (the
    // recorder refuses for a few seconds after a close; the timing verified on the recorder).
    public static final class Timings {
        public long join = 90_000;
        public long restartPause = 2_000;
        public long switchDelay = 300;
        public long heartbeat = 5_000;
        public long poll = 1_000;
        public long settle = 500;
        public long reconnectPause = 2_000;
        public long offWait = 10_000;
        public long connectWait = 15_000;
        public int closeWait = 2_000;
        public Transfer.Timeouts receive = new Transfer.Timeouts();
    }

    // Result is a transferred recording.
    public static final class Result {
        public final long size;
        public final boolean markerOk;

        Result(long size, boolean markerOk) {
            this.size = size;
            this.markerOk = markerOk;
        }
    }

    private final PocketSession ble;
    private final HostWifi wifi;
    private final String host;
    private final int port;
    private final Consumer<String> log;
    private final Runnable onRestart;
    private final Timings t;
    private final ScheduledExecutorService timer = Executors.newSingleThreadScheduledExecutor(r -> {
        Thread thread = new Thread(r, "pocket-wifi-timer");
        thread.setDaemon(true);
        return thread;
    });

    private String ssid;
    private boolean raised;
    private boolean prepared;
    private int connections;
    private ScheduledFuture<?> heartbeat;
    private volatile Transfer transfer;
    private volatile boolean cancelled;
    private long closedAt; // when the last transfer connection was closed

    public WifiSession(PocketSession ble, HostWifi wifi, Consumer<String> log, Runnable onRestart) {
        this(ble, wifi, HOST, PORT, log, onRestart, new Timings());
    }

    WifiSession(PocketSession ble, HostWifi wifi, String host, int port, Consumer<String> log, Runnable onRestart, Timings timings) {
        this.ble = ble;
        this.wifi = wifi;
        this.host = host;
        this.port = port;
        this.log = log;
        this.onRestart = onRestart;
        this.t = timings;
    }

    private void check() throws PocketException {
        if (cancelled) throw new PocketException("cancelled");
    }

    // sendQuietly writes a command whose answer nobody waits for (heartbeat, polling).
    private void sendQuietly(String name) {
        try {
            ble.send(name);
        } catch (PocketException ignored) {
            // the next step finds out
        }
    }

    // raise starts the access point and joins it: WIFIO, WIFI for the credentials, then join
    // while WIFIS is polled until it reports a client (1) — the Pocket app's order.
    private void raise() throws PocketException, InterruptedException {
        int since = ble.mark();
        raised = true;
        long started = System.currentTimeMillis();
        try {
            ble.command("WIFIO", "WIFIO");
        } catch (PocketException e) {
            // No answer: the recorder hangs (e.g. after a switch it couldn't serve). Every later
            // attempt would fail the same way, so the copy stops here.
            if (e.code.equals("no-answer")) throw new PocketException("stuck", "The Pocket stopped answering WiFi commands");
            throw e;
        }
        if (ssid == null) {
            String credentials = ble.command("WIFI", "WIFI", 5_000, PocketSession.PAIR);
            int amp = credentials.indexOf('&');
            ssid = credentials.substring(0, amp);
            wifi.prepare(ssid, credentials.substring(amp + 1));
            prepared = true;
        }
        check();
        ScheduledFuture<?> poll = timer.scheduleAtFixedRate(() -> sendQuietly("WIFIS"), t.poll, t.poll, TimeUnit.MILLISECONDS);
        try {
            if (!wifi.join(ssid, started + t.join)) throw new PocketException("wifi-join", "Couldn't join " + ssid);
            check();
            long left = Math.max(1_000, started + t.join - System.currentTimeMillis());
            if (ble.waitFor("WIFIS", since, left, PocketSession.ONE) == null) {
                throw new PocketException("wifi-ap", "The Pocket never reported this phone on its WiFi");
            }
        } finally {
            poll.cancel(false);
        }
        connections = 0;
        log.accept("On " + ssid);
    }

    public void start() throws PocketException, InterruptedException {
        wifi.setup();
        ble.subscribeAudio();
        // The Pocket app keeps a heartbeat going while the access point is up.
        heartbeat = timer.scheduleAtFixedRate(() -> sendQuietly("WPING"), t.heartbeat, t.heartbeat, TimeUnit.MILLISECONDS);
        raise();
    }

    // download transfers one recording into file. progress hears received bytes of size.
    public Result download(PocketSession.Recording recording, File file, Transfer.Progress progress)
            throws PocketException, InterruptedException, IOException {
        check();
        if (connections >= FILES_PER_SESSION) {
            onRestart.run();
            try {
                ble.command("WIFIC", "WIFIC");
            } catch (PocketException ignored) {
                // restarting anyway
            }
            raised = false;
            wifi.leave();
            Thread.sleep(t.restartPause);
            check();
            raise();
        }
        long pause = closedAt + t.reconnectPause - System.currentTimeMillis();
        if (pause > 0) Thread.sleep(pause);
        // The connection must be open BEFORE the switch (U&WIFI).
        Transfer current;
        try {
            current = Transfer.open(wifi, host, port, t.connectWait);
        } catch (PocketException e) {
            connections = FILES_PER_SESSION; // not listening: a fresh access point may help
            throw e;
        }
        transfer = current;
        connections++;
        try {
            check();
            // An accepted connection the recorder resets shows up within moments; asking for the
            // file on it would start a transfer nobody can receive.
            Thread.sleep(t.settle);
            if (!current.alive()) {
                throw new PocketException("transfer", "The Pocket dropped the connection (" + current.lostReason() + ")");
            }
            String answer = ble.command("U&" + recording.date + "&" + recording.timestamp, "U", 10_000, PocketSession.ANY);
            if (!answer.trim().matches("\\d+")) {
                throw new PocketException("refused", "The Pocket wouldn't send " + recording.timestamp + " (MCU&U&" + answer + ")");
            }
            long size = Long.parseLong(answer.trim());
            Thread.sleep(t.switchDelay);
            // Never switch without an open connection: the recorder would hang (MCU&SHUT).
            if (!current.alive()) {
                throw new PocketException("transfer", "The connection was lost before the switch (" + current.lostReason() + ")");
            }
            int since = ble.send("U&WIFI");
            boolean markerOk = current.receive(size, file, t.receive, progress);
            if (ble.waitFor("OFF", since, t.offWait, PocketSession.ANY) == null) log.accept("The Pocket did not report the end of the transfer");
            return new Result(size, markerOk);
        } catch (PocketException | IOException | InterruptedException | RuntimeException e) {
            // The recorder's state is unknown now: the next file starts on a fresh access point.
            connections = FILES_PER_SESSION;
            if (cancelled) throw new PocketException("cancelled");
            throw e;
        } finally {
            current.close(t.closeWait);
            closedAt = System.currentTimeMillis();
            if (transfer == current) transfer = null;
        }
    }

    // cancel stops a transfer that runs now; download() then throws 'cancelled'.
    public void cancel() {
        cancelled = true;
        Transfer current = transfer;
        if (current != null) current.abort();
    }

    // close lowers the access point and puts this device's WiFi back. Never throws.
    public void close() {
        if (heartbeat != null) heartbeat.cancel(false);
        timer.shutdownNow();
        if (raised) {
            raised = false;
            try {
                ble.command("WIFIC", "WIFIC");
            } catch (PocketException | InterruptedException ignored) {
                // lowered or gone anyway
            }
        }
        if (prepared) {
            prepared = false;
            try {
                wifi.restore();
            } catch (RuntimeException e) {
                log.accept("WiFi: couldn't restore: " + e.getMessage());
            }
        }
    }
}
