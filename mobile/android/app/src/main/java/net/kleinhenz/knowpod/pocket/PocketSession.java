package net.kleinhenz.knowpod.pocket;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;
import java.util.function.Consumer;
import java.util.function.Predicate;

// PocketSession talks to a Pocket recorder (heypocketai.com) over a Bluetooth connection: the
// app writes "APP&<command>" to a characteristic, and the recorder answers
// "MCU&<command>&<value>" in notifications of the same characteristic (kept in a MessageLog).
// A connection has to be unlocked with the recorder's session key first ("APP&SK&<key>",
// answered "MCU&SK&OK"). The commands used here:
//   BAT         → MCU&BAT&58              battery, percent
//   FW          → MCU&FW&1.8              firmware version
//   SPACE       → MCU&SPA&060846&061032   storage used and total, KB
//   GET&USB     → MCU&USB&1               whether the recorder is a USB drive when plugged in
//   LIST_DIRS   → MCU&DIRS&<date>… MCU&DIRS_SUM&<n>                 days with recordings
//   LIST&<date> → MCU&F&<date>&<timestamp>&<seconds>… MCU&LIST&<n>   a day's recordings
//   U&<date>&<timestamp> → MCU&U&<size>, then the file over Bluetooth (BluetoothTransfer) … MCU&OFF
// and for the WiFi transfer WIFIO, WIFI, WIFIS, WPING, U&WIFI and WIFIC (see WifiSession). A port of desktop/src/bluetooth.js; the Bluetooth connection itself is
// a Link (AndroidBle on the phone, a fake recorder in the tests).
public class PocketSession {
    // Link is the Bluetooth connection: writes go to the command characteristic, in order,
    // from any thread.
    public interface Link {
        void write(byte[] bytes) throws IOException;

        // subscribeAudio subscribes to the recording data of a Bluetooth transfer: the raw
        // bytes of the file, in notifications that go to listener in order. A transfer only
        // runs while it's subscribed to, and the WiFi transfer switches a running Bluetooth
        // transfer over.
        void subscribeAudio(Consumer<byte[]> listener) throws IOException;

        void close();
    }

    // How long to wait for an answer.
    static final long ANSWER_TIMEOUT = 5_000;
    // How long to wait for the end of a listing.
    static long listTimeout = 10_000;
    // How long to let late repeats of a listing's answers come in before the next listing.
    static long listSettle = 250;

    // The checks waitFor and command take.
    public static final Predicate<String> ANY = v -> true;
    public static final Predicate<String> DIGITS = v -> v.trim().matches("\\d+");
    public static final Predicate<String> ONE = v -> v.trim().equals("1");
    public static final Predicate<String> PAIR = v -> v.contains("&");

    private final Link link;
    private final MessageLog log;
    private boolean audio;
    // Where the recording data goes; null throws it away (a transfer switched to WiFi).
    private volatile Consumer<byte[]> audioSink;

    public PocketSession(Link link, MessageLog log) {
        this.link = link;
        this.log = log;
    }

    public int mark() {
        return log.mark();
    }

    public boolean connected() {
        return !log.isDisconnected();
    }

    // send writes "APP&<name>" and returns the mark before it.
    public int send(String name) throws PocketException {
        if (log.isDisconnected()) throw new PocketException("disconnected", "The recorder disconnected");
        int since = log.mark();
        try {
            link.write(("APP&" + name).getBytes(StandardCharsets.US_ASCII));
        } catch (IOException e) {
            throw new PocketException(log.isDisconnected() ? "disconnected" : "failed", e.getMessage(), e);
        }
        return since;
    }

    // waitFor returns the value of the first answer to answer after since that accept takes,
    // or null after timeout ms.
    public String waitFor(String answer, int since, long timeout, Predicate<String> accept)
            throws PocketException, InterruptedException {
        return log.waitFor(answer, since, timeout, accept);
    }

    public String command(String name, String answer) throws PocketException, InterruptedException {
        return command(name, answer, ANSWER_TIMEOUT, ANY);
    }

    // command sends name and returns the value of its answer, or throws 'no-answer'.
    public String command(String name, String answer, long timeout, Predicate<String> accept)
            throws PocketException, InterruptedException {
        String value = waitFor(answer, send(name), timeout, accept);
        if (value == null) throw new PocketException("no-answer", "No answer to " + name);
        return value;
    }

    public List<String> values(String answer, int since) {
        return log.values(answer, since);
    }

    public synchronized void subscribeAudio() throws PocketException {
        if (audio) return;
        try {
            link.subscribeAudio(bytes -> {
                Consumer<byte[]> sink = audioSink;
                if (sink != null) sink.accept(bytes);
            });
        } catch (IOException e) {
            throw new PocketException("failed", e.getMessage(), e);
        }
        audio = true;
    }

    // receiveAudio sends the recording data of Bluetooth transfers to sink; null throws it away.
    public void receiveAudio(Consumer<byte[]> sink) {
        audioSink = sink;
    }

    public void close() {
        link.close();
    }

    // optional runs a command whose answer is nice to have, returning null without one.
    private String optional(String name, String answer) throws PocketException, InterruptedException {
        try {
            return command(name, answer);
        } catch (PocketException e) {
            if (e.code.equals("no-answer")) return null;
            throw e;
        }
    }

    // unlock unlocks the connection with the session key.
    public void unlock(String sessionKey) throws PocketException, InterruptedException {
        if (!"OK".equals(optional("SK&" + sessionKey, "SK"))) {
            throw new PocketException("auth", "The recorder refused the session key");
        }
    }

    // check reads the battery, firmware, storage and USB state, as the settings page shows them.
    public Map<String, Object> check() throws PocketException, InterruptedException {
        Map<String, Object> info = new LinkedHashMap<>();
        Integer battery = parseInt(command("BAT", "BAT"));
        String firmware = optional("FW", "FW");
        String space = optional("SPACE", "SPA");
        String usb = optional("GET&USB", "USB");
        info.put("battery", battery);
        info.put("firmware", firmware == null ? null : firmware.trim());
        Map<String, Object> storage = null;
        if (space != null) {
            String[] parts = space.split("&");
            Integer used = parts.length > 0 ? parseInt(parts[0]) : null;
            Integer total = parts.length > 1 ? parseInt(parts[1]) : null;
            if (used != null && total != null) {
                storage = new LinkedHashMap<>();
                storage.put("usedKB", used);
                storage.put("totalKB", total);
            }
        }
        info.put("storage", storage);
        info.put("usb", "1".equals(usb) ? Boolean.TRUE : "0".equals(usb) ? Boolean.FALSE : null);
        return info;
    }

    static Integer parseInt(String text) {
        if (text == null) return null;
        java.util.regex.Matcher m = java.util.regex.Pattern.compile("^\\s*(-?\\d+)").matcher(text);
        if (!m.find()) return null;
        try {
            return Integer.parseInt(m.group(1));
        } catch (NumberFormatException e) {
            return null;
        }
    }

    // Recording is one recording on the recorder.
    public static final class Recording {
        public final String date; // 2026-10-03
        public final String timestamp; // 20261003142550
        public final int seconds;

        public Recording(String date, String timestamp, int seconds) {
            this.date = date;
            this.timestamp = timestamp;
            this.seconds = seconds;
        }

        public String name() {
            return timestamp + ".mp3";
        }
    }

    // Listing is the recorder's recordings, oldest first; incomplete if its listing came back
    // short, rather than the missing recordings looking copied.
    public static final class Listing {
        public final List<Recording> recordings;
        public final int days;
        public final boolean incomplete;

        Listing(List<Recording> recordings, int days, boolean incomplete) {
            this.recordings = recordings;
            this.days = days;
            this.incomplete = incomplete;
        }
    }

    private static final class Rows {
        final List<String> rows;
        final boolean isShort;

        Rows(List<String> rows, boolean isShort) {
            this.rows = rows;
            this.isShort = isShort;
        }
    }

    // collect sends name and returns the rows (the distinct values of answer that keep takes)
    // up to an end that counts them, and whether fewer came than it counts.
    private Rows collect(String name, String answer, String end, Predicate<String> keep)
            throws PocketException, InterruptedException {
        int since = send(name);
        Predicate<String> counted = value -> {
            Integer n = parseInt(value);
            return rows(answer, since, keep).size() >= (n == null ? 0 : n);
        };
        String value = waitFor(end, since, listTimeout, counted);
        List<String> ends = values(end, since);
        if (value == null && ends.isEmpty()) throw new PocketException("no-answer", "No end of the answer to " + name);
        Thread.sleep(listSettle);
        int expected = 0;
        for (String e : ends) {
            Integer n = parseInt(e);
            if (n != null && n > expected) expected = n;
        }
        List<String> found = rows(answer, since, keep);
        return new Rows(found, value == null || found.size() < expected);
    }

    private List<String> rows(String answer, int since, Predicate<String> keep) {
        List<String> kept = new ArrayList<>();
        for (String row : new LinkedHashSet<>(values(answer, since))) if (keep.test(row)) kept.add(row);
        return kept;
    }

    // again asks once more when the answer came back short.
    private Rows again(String name, String answer, String end, Predicate<String> keep)
            throws PocketException, InterruptedException {
        Rows first = collect(name, answer, end, keep);
        if (!first.isShort) return first;
        Rows second = collect(name, answer, end, keep);
        return second.rows.size() >= first.rows.size() ? second : first;
    }

    // listRecordings lists the recorder's recordings, oldest first. Repeats are dropped: some
    // systems deliver each notification several times, a few milliseconds apart, so a late
    // repeat of one day's end ("MCU&LIST&<n>") can come in after the next day was asked for. A
    // listing ends only at an end whose count its rows reach, and the repeats are let in
    // before the next listing is asked for. A listing that comes back short (fewer rows than
    // its end counts, or a day without any) is asked for again; if it stays short, incomplete
    // says so.
    public Listing listRecordings() throws PocketException, InterruptedException {
        boolean incomplete = false;
        Map<String, Recording> recordings = new TreeMap<>();
        Rows dirs = again("LIST_DIRS", "DIRS", "DIRS_SUM", day -> day.matches("\\d{4}-\\d{2}-\\d{2}"));
        if (dirs.isShort) incomplete = true;
        for (String day : dirs.rows) {
            Predicate<String> keep = row -> {
                String[] parts = row.split("&");
                return parts[0].equals(day) && parts.length > 1 && parts[1].matches("\\d{14}");
            };
            // A day is listed because it has recordings: none means its answer got lost.
            Rows list = again("LIST&" + day, "F", "LIST", keep);
            if (list.rows.isEmpty()) list = collect("LIST&" + day, "F", "LIST", keep);
            if (list.isShort || list.rows.isEmpty()) incomplete = true;
            for (String row : list.rows) {
                String[] parts = row.split("&");
                Integer seconds = parts.length > 2 ? parseInt(parts[2]) : null;
                recordings.put(parts[1], new Recording(parts[0], parts[1], seconds == null ? 0 : seconds));
            }
        }
        return new Listing(Collections.unmodifiableList(new ArrayList<>(recordings.values())), dirs.rows.size(), incomplete);
    }
}
