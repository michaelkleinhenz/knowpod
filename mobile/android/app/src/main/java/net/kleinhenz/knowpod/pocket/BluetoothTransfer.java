package net.kleinhenz.knowpod.pocket;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

// BluetoothTransfer downloads recordings over the Bluetooth connection itself, as the Pocket
// app does for all but long recordings: no access point to raise and join, which takes longer
// than a short recording takes over Bluetooth (tools/pocket-wifi-probe/RESEARCH.md, run 6 and
// captures 1 and 2 of the Pocket app):
//
//   U&<date>&<timestamp> → MCU&U&<size>; the recorder then sends the file's raw bytes (MP3,
//   no header) as notifications of the audio characteristic (001120a1, 244 bytes each), and
//   MCU&OFF once all of it went out. About 65 KB/s on a phone.
//
// The recorder only sends while the audio characteristic is subscribed to.
final class BluetoothTransfer {
    // Timeouts, ms: for the answer to U, the first bytes, a pause in the middle, and MCU&OFF
    // after the last byte.
    static final class Timings {
        long answer = 10_000;
        long firstByte = 15_000;
        long idle = 10_000;
        long offWait = 5_000;
    }

    private final PocketSession ble;
    private final Timings t;

    BluetoothTransfer(PocketSession ble, Timings timings) {
        this.ble = ble;
        this.t = timings;
    }

    // download transfers one recording into file (written to <file>.part, renamed once all
    // of it came) and returns its size. progress hears received bytes of size.
    long download(PocketSession.Recording recording, File file, Transfer.Progress progress)
            throws PocketException, InterruptedException, IOException {
        ble.subscribeAudio();
        File dir = file.getParentFile();
        if (dir != null && !dir.isDirectory() && !dir.mkdirs()) throw new IOException("Couldn't create " + dir);
        LinkedBlockingQueue<byte[]> chunks = new LinkedBlockingQueue<>();
        ble.receiveAudio(chunks::add);
        File partial = new File(file.getPath() + ".part");
        boolean ok = false;
        int since = -1;
        try {
            since = ble.send("U&" + recording.date + "&" + recording.timestamp);
            String answer = ble.waitFor("U", since, t.answer, PocketSession.ANY);
            if (answer == null) throw new PocketException("no-answer", "No answer to U&" + recording.timestamp);
            if (!answer.trim().matches("\\d+")) {
                throw new PocketException("refused", "The Pocket wouldn't send " + recording.timestamp + " (MCU&U&" + answer + ")");
            }
            long size = Long.parseLong(answer.trim());
            long received = 0;
            try (OutputStream out = new FileOutputStream(partial)) {
                long last = System.currentTimeMillis();
                long offSeen = 0;
                while (received < size) {
                    byte[] chunk = chunks.poll(200, TimeUnit.MILLISECONDS);
                    long now = System.currentTimeMillis();
                    if (chunk == null) {
                        if (!ble.connected()) throw new PocketException("disconnected", "The recorder disconnected");
                        // MCU&OFF ends the transfer; bytes still under way get a moment.
                        if (offSeen == 0 && !ble.values("OFF", since).isEmpty()) offSeen = now;
                        if (offSeen > 0 && now - offSeen > 1_000) {
                            throw new PocketException("transfer", "The Pocket stopped sending at " + received + " of " + size + " bytes");
                        }
                        if (now - last > (received == 0 ? t.firstByte : t.idle)) {
                            throw new PocketException("transfer", received == 0 ? "The Pocket sent nothing"
                                    : "The transfer stalled at " + received + " of " + size + " bytes");
                        }
                        continue;
                    }
                    last = now;
                    // Anything past the size isn't the file.
                    int n = (int) Math.min(chunk.length, size - received);
                    out.write(chunk, 0, n);
                    received += n;
                    if (progress != null) progress.update(received, size);
                }
            }
            if (ble.waitFor("OFF", since, t.offWait, PocketSession.ANY) == null) {
                throw new PocketException("transfer", "The Pocket did not report the end of the transfer");
            }
            if (file.exists() && !file.delete()) throw new IOException("Couldn't replace " + file);
            if (!partial.renameTo(file)) throw new IOException("Couldn't rename " + partial);
            ok = true;
            return size;
        } finally {
            if (!ok) {
                partial.delete();
                // A transfer that broke off may still be running: let it end before the next.
                if (since >= 0 && ble.connected() && !Thread.currentThread().isInterrupted()) {
                    try {
                        ble.waitFor("OFF", since, t.offWait, PocketSession.ANY);
                    } catch (PocketException | InterruptedException ignored) {
                        // the next step finds out
                    }
                }
            }
            ble.receiveAudio(null);
        }
    }
}
