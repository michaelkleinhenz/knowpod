package net.kleinhenz.knowpod.recorder;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.BooleanSupplier;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

// RecorderRelay uploads the recordings a knowpod recorder (the ESP32 gadget, esp32/) hands over
// Bluetooth while it has no Wi-Fi (docs/ble-transfer.md), with the recorder's own device token,
// as the recorder would itself (docs/device-protocol.md): the backend then files them under the
// device and deduplicates them with later Wi-Fi uploads. A port of desktop/src/recorder-relay.js;
// the Bluetooth side is a Link (RecorderBle on the phone, a fake recorder in the tests), the
// backend an Http.
public final class RecorderRelay {
    // Bytes per PATCH to the backend.
    static final int UPLOAD_CHUNK = 1024 * 1024;
    // Bytes per Bluetooth read request (the recorder's maximum).
    static final int READ_CHUNK = 256 * 1024;
    // How long the recorder may take to hash a recording ("open").
    static final long OPEN_TIMEOUT = 180_000;
    static final long REQUEST_TIMEOUT = 15_000;
    private static final int MAX_CHECKSUM_RESETS = 2;

    // Link is the Bluetooth connection to the recorder.
    public interface Link {
        // request sends a request and returns the recorder's response.
        JSONObject request(JSONObject message, long timeout) throws IOException, InterruptedException;

        // read returns up to length bytes of recording id from offset, in order.
        byte[] read(String id, long offset, int length) throws IOException, InterruptedException;
    }

    // Http calls the backend.
    public interface Http {
        Response call(String method, String url, Map<String, String> headers, byte[] body) throws IOException;
    }

    public static final class Response {
        final int status;
        final String body;
        final long uploadOffset; // the Upload-Offset header, -1 without one

        public Response(int status, String body, long uploadOffset) {
            this.status = status;
            this.body = body == null ? "" : body;
            this.uploadOffset = uploadOffset;
        }

        JSONObject json() {
            try {
                return body.trim().isEmpty() ? new JSONObject() : new JSONObject(body);
            } catch (JSONException e) {
                return new JSONObject();
            }
        }
    }

    // RelayException ends the relay: code is no-token (the recorder has no device token),
    // other-server (it uploads to another server than the app's), token (the backend refused
    // the token), recorder (the recorder answered with an error) or http (the backend failed).
    public static final class RelayException extends IOException {
        public final String code;

        RelayException(String code, String message) {
            super(message);
            this.code = code;
        }
    }

    // Progress hears how the relay goes: phase listing, preparing or uploading.
    public interface Progress {
        void update(String phase, int current, int total, String title, long bytes, long totalBytes);
    }

    public static final class Result {
        public int copied;
        public int failed;
        public int total;
        public final List<String> errors = new ArrayList<>();
    }

    private final Link link;
    private final Http http;
    private final Progress progress;
    private final BooleanSupplier cancelled;
    private String backend;
    private String token;

    public RecorderRelay(Link link, Http http, Progress progress, BooleanSupplier cancelled) {
        this.link = link;
        this.http = http;
        this.progress = progress;
        this.cancelled = cancelled;
    }

    static boolean complete(String status) {
        return "received".equals(status) || "stored".equals(status) || "transcribed".equals(status) || "summarized".equals(status);
    }

    // originOf is the origin of url ("https://host[:port]"), or null.
    static String originOf(String url) {
        if (url == null) return null;
        java.util.regex.Matcher m = java.util.regex.Pattern.compile("(?i)^(https?)://([^/?#]+)").matcher(url.trim());
        if (!m.find()) return null;
        String scheme = m.group(1).toLowerCase();
        String host = m.group(2).toLowerCase();
        if (scheme.equals("https") && host.endsWith(":443")) host = host.substring(0, host.length() - 4);
        if (scheme.equals("http") && host.endsWith(":80")) host = host.substring(0, host.length() - 3);
        return scheme + "://" + host;
    }

    private static JSONObject message(Object... pairs) {
        JSONObject m = new JSONObject();
        try {
            for (int i = 0; i < pairs.length; i += 2) m.put((String) pairs[i], pairs[i + 1]);
        } catch (JSONException e) {
            throw new IllegalArgumentException(e);
        }
        return m;
    }

    // ask sends a request and returns the response, throwing when it failed.
    private JSONObject ask(JSONObject m, long timeout) throws IOException, InterruptedException {
        JSONObject r = link.request(m, timeout);
        if (r == null || !r.optBoolean("ok")) {
            String why = r == null ? "no answer" : r.optString("message", r.optString("error", "failed"));
            throw new RelayException("recorder", m.optString("op") + ": " + why);
        }
        return r;
    }

    private byte[] readRange(String id, long offset, int length) throws IOException, InterruptedException {
        ByteArrayOutputStream out = new ByteArrayOutputStream(length);
        while (out.size() < length) {
            byte[] part = link.read(id, offset + out.size(), Math.min(READ_CHUNK, length - out.size()));
            if (part.length == 0) throw new RelayException("recorder", "read: no data at " + (offset + out.size()));
            out.write(part, 0, Math.min(part.length, length - out.size()));
        }
        return out.toByteArray();
    }

    private Response api(String method, String path, JSONObject json, byte[] data, long offset) throws IOException {
        Map<String, String> headers = new LinkedHashMap<>();
        headers.put("Authorization", "Bearer " + token);
        byte[] body = null;
        if (json != null) {
            headers.put("Content-Type", "application/json");
            body = json.toString().getBytes(StandardCharsets.UTF_8);
        } else if (data != null) {
            headers.put("Content-Type", "application/offset+octet-stream");
            headers.put("Upload-Offset", String.valueOf(offset));
            body = data;
        }
        Response r = http.call(method, backend + path, headers, body);
        if (r.status == 401) throw new RelayException("token", "The backend refused the recorder's device token");
        return r;
    }

    private static RelayException httpError(String what, Response r) {
        String error = r.json().optString("error", "");
        return new RelayException("http", what + ": HTTP " + r.status + (error.isEmpty() ? "" : " (" + error + ")"));
    }

    // run uploads what the recorder offers to serverUrl (the app's server, an origin).
    public Result run(String serverUrl) throws IOException, InterruptedException {
        JSONObject info = ask(message("op", "info"), REQUEST_TIMEOUT);
        token = info.optString("token", "");
        backend = info.optString("backend", "").replaceAll("/+$", "");
        if (token.isEmpty() || backend.isEmpty()) throw new RelayException("no-token", "The recorder has no device token");
        String origin = originOf(backend);
        if (origin == null || !origin.equals(originOf(serverUrl))) {
            throw new RelayException("other-server", "The recorder uploads to " + (origin == null ? backend : origin));
        }

        progress.update("listing", 0, 0, "", 0, 0);
        JSONArray recordings = ask(message("op", "list"), REQUEST_TIMEOUT).optJSONArray("recordings");
        Result result = new Result();
        result.total = recordings == null ? 0 : recordings.length();
        for (int i = 0; i < result.total && !cancelled.getAsBoolean(); i++) {
            JSONObject rec = recordings.optJSONObject(i);
            if (rec == null) continue;
            String id = rec.optString("id");
            String title = rec.optString("title", id);
            progress.update("preparing", i + 1, result.total, title, 0, 0);
            JSONObject opened = ask(message("op", "open", "id", id), OPEN_TIMEOUT);
            long size = opened.optLong("size");
            String error = upload(rec, id, size, opened.optString("sha256"), i + 1, result.total, title);
            if (error == null) {
                result.copied++;
            } else if (!error.isEmpty()) {
                result.failed++;
                result.errors.add(error);
            }
        }
        return result;
    }

    private JSONObject createRequest(JSONObject rec, String id, long size, String sha256) {
        JSONObject req = message("recordingId", id, "size", size, "sha256", sha256);
        JSONArray highlights = new JSONArray();
        JSONArray marks = rec.optJSONArray("highlights");
        for (int i = 0; marks != null && i < marks.length(); i++) highlights.put(message("offsetMs", marks.optLong(i)));
        try {
            if (!rec.optString("recordedAt").isEmpty()) req.put("recordedAt", rec.optString("recordedAt"));
            req.put("highlights", highlights);
        } catch (JSONException e) {
            throw new IllegalArgumentException(e);
        }
        return req;
    }

    // upload sends one recording and returns null when it's done, an error message when it
    // failed for good, or "" when the relay was cancelled.
    private String upload(JSONObject rec, String id, long size, String sha256, int current, int total, String title)
            throws IOException, InterruptedException {
        JSONObject create = createRequest(rec, id, size, sha256);
        Response r = api("POST", "/uploads", create, null, 0);
        if (r.status == 409) return id + ": the backend already has a different recording with this id";
        if (r.status != 200 && r.status != 201) throw httpError("Creating the upload", r);
        JSONObject body = r.json();
        String uploadId = body.optString("uploadId");
        String status = body.optString("status");
        long offset = body.optLong("offset", 0);
        int resets = 0;

        while (!complete(status)) {
            if (cancelled.getAsBoolean()) return "";
            if ("failed".equals(status)) return id + ": the backend rejected the recording: " + body.optString("error", "");
            progress.update("uploading", current, total, title, offset, size);
            int n = (int) Math.min(UPLOAD_CHUNK, size - offset);
            if (n <= 0) {
                // Everything was sent but not confirmed: ask where the upload stands.
                r = api("GET", "/uploads/" + uploadId, null, null, 0);
            } else {
                r = api("PATCH", "/uploads/" + uploadId, null, readRange(id, offset, n), offset);
            }
            body = r.json();
            if (r.status == 200) {
                status = body.optString("status");
                long next = body.has("offset") ? body.optLong("offset") : r.uploadOffset >= 0 ? r.uploadOffset : offset + Math.max(n, 0);
                if (n <= 0 && !complete(status) && !"failed".equals(status) && next >= size) {
                    throw httpError("The backend has the whole file but did not finish it", r);
                }
                offset = next;
            } else if (r.status == 409) {
                // Offset mismatch: continue from the backend's.
                if (body.has("offset")) offset = body.optLong("offset");
                else if (r.uploadOffset >= 0) offset = r.uploadOffset;
                else throw httpError("Upload offset", r);
            } else if (r.status == 404) {
                // Gone on the backend (purged): start a new upload.
                r = api("POST", "/uploads", create, null, 0);
                if (r.status != 200 && r.status != 201) throw httpError("Creating the upload", r);
                body = r.json();
                uploadId = body.optString("uploadId");
                status = body.optString("status");
                offset = body.optLong("offset", 0);
            } else if (r.status == 422) {
                // Checksum mismatch (the backend reset the offset to 0) or not a supported file.
                if (++resets > MAX_CHECKSUM_RESETS) return id + ": backend: " + body.optString("error", "checksum mismatch");
                offset = 0;
            } else {
                throw httpError("Uploading", r);
            }
        }
        ask(message("op", "done", "id", id, "uploadId", uploadId, "status", status), REQUEST_TIMEOUT);
        progress.update("uploading", current, total, title, size, size);
        return null;
    }
}
