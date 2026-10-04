package net.kleinhenz.knowpod.pocket;

// PocketException carries a code for the web app to explain (frontend/src/components/PocketUsbSync.tsx),
// the same codes the desktop app uses:
//   Bluetooth: not-configured, not-found, auth, unsupported, busy, timeout, disconnected,
//              no-answer, failed
//   WiFi:      wifi-unsupported, wifi-setup, wifi-join, wifi-ap, stuck, refused, transfer,
//              cancelled
//   copy:      firmware, battery, no-server, signed-out, offline, server
public class PocketException extends Exception {
    public final String code;

    public PocketException(String code) {
        this(code, code);
    }

    public PocketException(String code, String message) {
        super(message == null || message.isEmpty() ? code : message);
        this.code = code;
    }

    public PocketException(String code, String message, Throwable cause) {
        super(message == null || message.isEmpty() ? code : message, cause);
        this.code = code;
    }

    // codeOf is the code of err, or fallback when it has none.
    public static String codeOf(Throwable err, String fallback) {
        return err instanceof PocketException ? ((PocketException) err).code : fallback;
    }
}
