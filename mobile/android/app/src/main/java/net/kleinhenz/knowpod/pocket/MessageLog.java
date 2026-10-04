package net.kleinhenz.knowpod.pocket;

import java.util.ArrayList;
import java.util.List;
import java.util.function.Predicate;

// MessageLog keeps every answer of the recorder in order ("MCU&<answer>&<value>"), so answers
// that come on their own (WIFIS changes, MCU&OFF) can be waited for after a mark(): see
// waitFor. The Bluetooth connection adds each notification of the command characteristic and
// tells when it's gone. Thread-safe: the heartbeat and polling write from other threads.
// The same as connect() in desktop/src/bluetooth.js.
public final class MessageLog {
    private final List<String> messages = new ArrayList<>();
    private boolean disconnected;

    // split splits a notification that carries several answers ("MCU&WIFIOMCU&OFF").
    static List<String> split(String text) {
        List<String> parts = new ArrayList<>();
        for (String part : text.replace("\0", "").split("(?=MCU&)")) {
            String trimmed = part.trim();
            if (!trimmed.isEmpty()) parts.add(trimmed);
        }
        return parts;
    }

    // valueOf is the value of an answer "MCU&<answer>&<value>" ("" for a bare "MCU&<answer>"),
    // or null if text is another answer.
    static String valueOf(String text, String answer) {
        String exact = "MCU&" + answer;
        if (text.equals(exact)) return "";
        return text.startsWith(exact + "&") ? text.substring(exact.length() + 1) : null;
    }

    // add keeps the answers of one notification.
    public synchronized void add(String notification) {
        messages.addAll(split(notification));
        notifyAll();
    }

    // disconnect marks the connection as gone: waiting ends with 'disconnected'.
    public synchronized void disconnect() {
        disconnected = true;
        notifyAll();
    }

    public synchronized boolean isDisconnected() {
        return disconnected;
    }

    public synchronized int mark() {
        return messages.size();
    }

    // waitFor returns the value of the first answer to answer after since that accept takes,
    // or null after timeout ms; it throws 'disconnected' when the recorder disconnects.
    public synchronized String waitFor(String answer, int since, long timeout, Predicate<String> accept)
            throws PocketException, InterruptedException {
        long end = System.currentTimeMillis() + timeout;
        int index = since;
        for (;;) {
            while (index < messages.size()) {
                String value = valueOf(messages.get(index++), answer);
                if (value != null && accept.test(value)) return value;
            }
            if (disconnected) throw new PocketException("disconnected", "The recorder disconnected");
            long left = end - System.currentTimeMillis();
            if (left <= 0) return null;
            wait(left);
        }
    }

    // values are the values of all answers to answer after since, in order.
    public synchronized List<String> values(String answer, int since) {
        List<String> found = new ArrayList<>();
        for (int i = since; i < messages.size(); i++) {
            String value = valueOf(messages.get(i), answer);
            if (value != null) found.add(value);
        }
        return found;
    }
}
