package net.kleinhenz.knowpod.pocket;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.io.PushbackInputStream;
import java.net.Socket;
import java.net.SocketTimeoutException;
import java.util.Arrays;

// Transfer is one connection to the recorder's transfer socket (192.168.200.1:8475), which
// carries one recording: exactly <size> bytes of MP3 file, then a fixed 10-byte end marker.
// The port of openSocket, alive, closeSocket and receiveFile in desktop/src/pocket-wifi.js.
final class Transfer {
    static final byte[] END_MARKER = {
        (byte) 0xba, 0x5a, 0x02, (byte) 0x8f, 0x04, (byte) 0xba, 0x5a, 0x02, (byte) 0x8f, 0x04,
    };

    // Progress hears how many bytes of size came so far.
    interface Progress {
        void update(long received, long size);
    }

    // Timeouts of receive(), ms.
    static final class Timeouts {
        int firstByte = 15_000;
        int idle = 15_000;
        int marker = 3_000;
    }

    private final Socket socket;
    private final PushbackInputStream in;
    private volatile String lost; // why the connection is gone, once it is

    Transfer(Socket socket) throws IOException {
        this.socket = socket;
        this.in = new PushbackInputStream(socket.getInputStream(), 1);
    }

    // open connects, retrying refusals for wait ms: after a connection closes, the recorder
    // refuses new ones for a few seconds.
    static Transfer open(HostWifi wifi, String host, int port, long wait) throws PocketException, InterruptedException {
        long deadline = System.currentTimeMillis() + wait;
        Exception last = null;
        for (;;) {
            try {
                return new Transfer(wifi.connect(host, port, 3_000));
            } catch (IOException e) {
                last = e;
            }
            if (System.currentTimeMillis() >= deadline) {
                throw new PocketException("transfer", "The Pocket didn't accept a connection (" + last.getMessage() + ")");
            }
            Thread.sleep(500);
        }
    }

    // alive says whether the connection is still open: not reset, closed or ended by the
    // recorder. The recorder sends nothing before the switch, so a short read only finds out
    // whether it's still there; a byte that does come is kept for receive().
    synchronized boolean alive() {
        if (lost != null || socket.isClosed()) return false;
        try {
            socket.setSoTimeout(1);
            int b = in.read();
            if (b < 0) {
                lost = "closed";
                return false;
            }
            in.unread(b);
            return true;
        } catch (SocketTimeoutException e) {
            return true;
        } catch (IOException e) {
            lost = e.getMessage() == null ? e.toString() : e.getMessage();
            return false;
        }
    }

    String lostReason() {
        return lost == null ? "closed" : lost;
    }

    // abort drops the connection at once (cancel): a receive() under way fails.
    void abort() {
        try {
            socket.close();
        } catch (IOException ignored) {
            // gone anyway
        }
    }

    // close ends the connection gracefully (FIN, as verified on the recorder) and returns once
    // the recorder closed its side too, closing it after ms at the latest.
    void close(int ms) {
        if (socket.isClosed()) return;
        try {
            socket.shutdownOutput();
            socket.setSoTimeout(ms);
            long end = System.currentTimeMillis() + ms;
            byte[] skip = new byte[4096];
            while (System.currentTimeMillis() < end && in.read(skip) >= 0) {
                // whatever still comes is thrown away
            }
        } catch (IOException ignored) {
            // closing anyway
        }
        abort();
    }

    // receive reads exactly size bytes of file from the connection into file, then the end
    // marker. It writes to <file>.part and renames it only once all of it arrived. Returns
    // whether the end marker followed (a missing one doesn't fail an otherwise complete file).
    boolean receive(long size, File file, Timeouts t, Progress progress) throws PocketException, IOException {
        File partial = new File(file.getPath() + ".part");
        File dir = file.getParentFile();
        if (dir != null && !dir.isDirectory() && !dir.mkdirs()) throw new IOException("Couldn't create " + dir);
        long received = 0;
        byte[] tail = new byte[0];
        byte[] buffer = new byte[64 * 1024];
        boolean ok = false;
        try (OutputStream out = new FileOutputStream(partial)) {
            socket.setSoTimeout(size == 0 ? t.marker : t.firstByte);
            for (;;) {
                int n;
                try {
                    n = in.read(buffer);
                } catch (SocketTimeoutException e) {
                    if (received >= size) break;
                    throw new PocketException("transfer", received == 0 ? "The Pocket sent nothing" : "The transfer stalled at " + received + " of " + size + " bytes");
                } catch (IOException e) {
                    if (received >= size) break;
                    throw new PocketException("transfer", e.getMessage() == null ? e.toString() : e.getMessage(), e);
                }
                if (n < 0) {
                    if (received >= size) break;
                    throw new PocketException("transfer", "The Pocket closed the connection at " + received + " of " + size + " bytes");
                }
                int body = (int) Math.min(n, size - received);
                if (body > 0) {
                    out.write(buffer, 0, body);
                    received += body;
                    if (progress != null) progress.update(received, size);
                }
                if (n > body) tail = concat(tail, Arrays.copyOfRange(buffer, body, n));
                if (received >= size) {
                    if (tail.length >= END_MARKER.length) break;
                    socket.setSoTimeout(t.marker);
                } else {
                    socket.setSoTimeout(t.idle);
                }
            }
            ok = true;
        } finally {
            if (!ok) partial.delete();
        }
        if (file.exists() && !file.delete()) throw new IOException("Couldn't replace " + file);
        if (!partial.renameTo(file)) {
            partial.delete();
            throw new IOException("Couldn't rename " + partial);
        }
        return tail.length >= END_MARKER.length && Arrays.equals(Arrays.copyOf(tail, END_MARKER.length), END_MARKER);
    }

    private static byte[] concat(byte[] a, byte[] b) {
        byte[] out = Arrays.copyOf(a, a.length + b.length);
        System.arraycopy(b, 0, out, a.length, b.length);
        return out;
    }
}
