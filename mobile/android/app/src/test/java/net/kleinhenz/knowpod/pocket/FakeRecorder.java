package net.kleinhenz.knowpod.pocket;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

// FakeRecorder behaves like the real recorder measured on firmware 1.8
// (tools/pocket-wifi-probe/RESEARCH.md), as in desktop/test/pocket-wifi.test.js: it serves the
// transfer socket for two connections per access point session, sends a file and the end
// marker only on a connection that is open when U&WIFI comes, and answers like the recorder.
// Failures seen on the real one can be switched on (see the fields).
final class FakeRecorder implements PocketSession.Link {
    // the recordings: timestamp → file
    final Map<String, byte[]> files = new LinkedHashMap<>();
    // send the end marker after a file
    boolean marker = true;
    // reset the connection right after sending a file
    boolean resetAfterFile;
    // numbers of accepted connections (counting all) to reset right away
    final List<Integer> resetAccepted = new ArrayList<>();
    // answers to U&<date>&<timestamp> other than the size
    final Map<String, String> answers = new HashMap<>();
    // how many APP&WIFIO it answers before it hangs
    int wifioAnswers = Integer.MAX_VALUE;
    // how many times each notification is delivered (BlueZ delivers some several times)
    int repeats = 1;

    final MessageLog log = new MessageLog();
    final List<String> sent = Collections.synchronizedList(new ArrayList<>());
    final List<String> violations = Collections.synchronizedList(new ArrayList<>());
    final List<String> calls = Collections.synchronizedList(new ArrayList<>());
    volatile int status;
    int apStarts;
    final int port;

    private ServerSocket server;
    private int accepted;
    private int acceptedTotal;
    private volatile Socket current;
    private byte[] staged;

    FakeRecorder() throws IOException {
        try (ServerSocket probe = new ServerSocket(0, 50, InetAddress.getLoopbackAddress())) {
            port = probe.getLocalPort();
        }
    }

    static byte[] mp3(int n, int seed) {
        byte[] out = new byte[n];
        for (int i = 0; i < n; i++) out[i] = (byte) (i * 7 + seed);
        out[0] = (byte) 0xff;
        out[1] = (byte) 0xf3;
        out[2] = 0x48;
        out[3] = (byte) 0xc4;
        return out;
    }

    FakeRecorder withDefaultFiles() {
        files.put("20261003142550", mp3(885_788, 1));
        files.put("20261003141332", mp3(722_348, 2));
        files.put("20261003160116", mp3(92_062, 3));
        return this;
    }

    PocketSession session() {
        return new PocketSession(this, log);
    }

    private void say(String text) {
        for (int i = 0; i < repeats; i++) log.add(text);
    }

    private synchronized void startAp() throws IOException {
        accepted = 0;
        apStarts++;
        ServerSocket s = new ServerSocket();
        s.setReuseAddress(true);
        s.bind(new InetSocketAddress(InetAddress.getLoopbackAddress(), port));
        server = s;
        Thread thread = new Thread(() -> {
            try {
                while (!s.isClosed()) {
                    Socket socket = s.accept();
                    synchronized (this) {
                        accepted++;
                        acceptedTotal++;
                        if (resetAccepted.contains(acceptedTotal)) {
                            // Shortly after the handshake, as seen on the real recorder.
                            new Thread(() -> {
                                sleep(10);
                                reset(socket);
                            }).start();
                        } else {
                            current = socket;
                        }
                        if (accepted >= 2) s.close(); // stops listening: connects are refused
                    }
                }
            } catch (IOException ignored) {
                // closed
            }
        });
        thread.setDaemon(true);
        thread.start();
    }

    private static void sleep(long ms) {
        try {
            Thread.sleep(ms);
        } catch (InterruptedException ignored) {
            Thread.currentThread().interrupt();
        }
    }

    private static void reset(Socket socket) {
        try {
            socket.setSoLinger(true, 0);
            socket.close();
        } catch (IOException ignored) {
            // gone
        }
    }

    private boolean open(Socket socket) {
        if (socket == null || socket.isClosed()) return false;
        try {
            socket.setSoTimeout(1);
            int b = socket.getInputStream().read();
            if (b < 0) return false;
        } catch (java.net.SocketTimeoutException e) {
            return true;
        } catch (IOException e) {
            return false;
        }
        return true;
    }

    @Override
    public void write(byte[] bytes) throws IOException {
        String text = new String(bytes, StandardCharsets.US_ASCII);
        if (!text.startsWith("APP&")) throw new IOException("not a command: " + text);
        handle(text.substring(4));
    }

    private void handle(String name) throws IOException {
        sent.add(name);
        if (name.startsWith("SK&")) say("MCU&SK&OK");
        else if (name.equals("FW")) say("MCU&FW&1.8");
        else if (name.equals("BAT")) say("MCU&BAT&58");
        else if (name.equals("LIST_DIRS")) {
            List<String> days = new ArrayList<>();
            for (String ts : files.keySet()) {
                String day = day(ts);
                if (!days.contains(day)) days.add(day);
            }
            for (String day : days) say("MCU&DIRS&" + day);
            say("MCU&DIRS_SUM&" + days.size());
        } else if (name.startsWith("LIST&")) {
            String day = name.substring(5);
            int n = 0;
            for (Map.Entry<String, byte[]> f : files.entrySet()) {
                if (!day(f.getKey()).equals(day)) continue;
                say("MCU&F&" + day + "&" + f.getKey() + "&" + f.getValue().length / 4000);
                n++;
            }
            say("MCU&LIST&" + n);
        } else if (name.equals("WIFIO")) {
            if (wifioAnswers-- <= 0) return; // hangs: no answer
            status = 3;
            startAp();
            say("MCU&WIFIO");
            status = 2;
        } else if (name.equals("WIFI")) say("MCU&WIFI&PKT01_GREY_TEST&abcd1234");
        else if (name.equals("WIFIS")) say("MCU&WIFIS&" + status);
        else if (name.equals("WPING")) say("MCU&WPING");
        else if (name.equals("WIFIC")) {
            synchronized (this) {
                if (server != null) server.close();
            }
            status = 0;
            say("MCU&WIFIC");
        } else if (name.equals("U&WIFI")) {
            Socket socket = current;
            if (!open(socket)) {
                violations.add("U&WIFI without an open connection");
                return;
            }
            say("MCU&U&WIFI");
            say("MCU&U&" + staged.length);
            byte[] file = staged;
            new Thread(() -> {
                try {
                    OutputStream out = socket.getOutputStream();
                    out.write(file);
                    if (marker) out.write(Transfer.END_MARKER);
                    out.flush();
                } catch (IOException e) {
                    violations.add("sending failed: " + e);
                }
                say("MCU&OFF");
                if (resetAfterFile) {
                    sleep(5);
                    reset(socket);
                }
            }).start();
        } else if (name.startsWith("U&")) {
            String timestamp = name.split("&")[2];
            if (answers.containsKey(timestamp)) {
                say("MCU&U&" + answers.get(timestamp));
                return;
            }
            staged = files.get(timestamp);
            say("MCU&U&" + staged.length);
        }
    }

    static String day(String timestamp) {
        return timestamp.substring(0, 4) + "-" + timestamp.substring(4, 6) + "-" + timestamp.substring(6, 8);
    }

    @Override
    public void subscribeAudio() {
        calls.add("subscribeAudio");
    }

    @Override
    public void close() {
        calls.add("close");
    }

    synchronized void stop() throws IOException {
        if (server != null) server.close();
    }

    // wifi is the phone's side of the network: joining makes the recorder see a client.
    HostWifi wifi() {
        return new HostWifi() {
            @Override
            public void setup() {
                calls.add("setup");
            }

            @Override
            public void prepare(String ssid, String password) {
                calls.add("prepare " + ssid + " " + password);
            }

            @Override
            public boolean join(String ssid, long deadline) {
                calls.add("join");
                status = 1;
                return true;
            }

            @Override
            public void leave() {
                calls.add("leave");
            }

            @Override
            public void restore() {
                calls.add("restore");
            }

            @Override
            public Socket connect(String host, int p, int timeout) throws IOException {
                Socket socket = new Socket();
                socket.connect(new InetSocketAddress(host, p), timeout);
                return socket;
            }
        };
    }

    static WifiSession.Timings timings() {
        WifiSession.Timings t = new WifiSession.Timings();
        t.restartPause = 10;
        t.switchDelay = 5;
        t.heartbeat = 20;
        t.poll = 10;
        t.join = 5_000;
        t.settle = 30;
        t.reconnectPause = 20;
        t.offWait = 500;
        t.connectWait = 2_000;
        t.closeWait = 200;
        t.receive.firstByte = 2_000;
        t.receive.idle = 2_000;
        t.receive.marker = 300;
        return t;
    }

    WifiSession wifiSession() {
        return new WifiSession(session(), wifi(), "127.0.0.1", port, text -> { }, () -> calls.add("restart"), timings());
    }
}
