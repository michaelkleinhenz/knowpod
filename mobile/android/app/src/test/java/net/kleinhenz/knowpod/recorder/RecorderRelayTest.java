package net.kleinhenz.knowpod.recorder;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;

// Tests for the Bluetooth relay of the knowpod recorder (RecorderRelay) against a fake recorder
// that answers like esp32/src/net/ble.cpp and a fake backend that follows the device upload
// protocol (docs/device-protocol.md); the same cases as desktop/test/recorder-relay.test.js.
public class RecorderRelayTest {
    private static final String TOKEN = "kpd_test";
    private static final String SERVER = "https://knowpod.example";

    private static byte[] audio(int n, int seed) {
        byte[] out = new byte[n];
        for (int i = 0; i < n; i++) out[i] = (byte) (i * 13 + seed);
        return out;
    }

    private static String sha(byte[] data) throws Exception {
        StringBuilder hex = new StringBuilder();
        for (byte b : MessageDigest.getInstance("SHA-256").digest(data)) hex.append(String.format("%02x", b));
        return hex.toString();
    }

    // FakeRecorder holds files by id and answers the requests of docs/ble-transfer.md.
    static final class FakeRecorder implements RecorderRelay.Link {
        final Map<String, byte[]> files = new LinkedHashMap<>();
        final List<JSONObject> done = new ArrayList<>();
        final List<Long> readOffsets = new ArrayList<>();
        String token = TOKEN;
        String backend = SERVER + "/api/v1";

        @Override
        public JSONObject request(JSONObject m, long timeout) throws java.io.IOException {
            try {
                JSONObject r = new JSONObject().put("ok", true).put("op", m.getString("op"));
                switch (m.getString("op")) {
                    case "info":
                        return r.put("name", "knowpod-1A2B").put("backend", backend).put("token", token);
                    case "list": {
                        JSONArray list = new JSONArray();
                        for (Map.Entry<String, byte[]> e : files.entrySet()) {
                            list.put(new JSONObject().put("id", e.getKey()).put("size", e.getValue().length)
                                    .put("recordedAt", "2026-09-26T08:15:00Z").put("highlights", new JSONArray().put(1500)));
                        }
                        return r.put("recordings", list);
                    }
                    case "open": {
                        byte[] data = files.get(m.getString("id"));
                        return r.put("id", m.getString("id")).put("size", data.length).put("sha256", sha(data));
                    }
                    case "done":
                        done.add(m);
                        return r;
                    default:
                        return new JSONObject().put("ok", false).put("error", "bad-request");
                }
            } catch (Exception e) {
                throw new java.io.IOException(e);
            }
        }

        @Override
        public byte[] read(String id, long offset, int length) {
            assertTrue(length <= RecorderRelay.READ_CHUNK);
            readOffsets.add(offset);
            byte[] data = files.get(id);
            return Arrays.copyOfRange(data, (int) offset, (int) Math.min(data.length, offset + length));
        }
    }

    // FakeBackend keeps uploads like the service does.
    static final class FakeBackend implements RecorderRelay.Http {
        static final class Upload {
            String recordingId;
            long size;
            String sha256;
            ByteArrayOutputStream data = new ByteArrayOutputStream();
            String status = "uploading";
            JSONObject request;
        }

        final Map<String, Upload> uploads = new LinkedHashMap<>();
        final List<String> calls = new ArrayList<>();
        int garble; // garbles this many PATCH bodies
        int next = 1;

        private RecorderRelay.Response reply(int status, String id, Upload u) throws Exception {
            JSONObject body = new JSONObject().put("uploadId", id).put("recordingId", u.recordingId).put("status", u.status)
                    .put("size", u.size).put("offset", u.data.size());
            return new RecorderRelay.Response(status, body.toString(), u.data.size());
        }

        @Override
        public RecorderRelay.Response call(String method, String url, Map<String, String> headers, byte[] body) throws java.io.IOException {
            try {
                String path = url.substring((SERVER + "/api/v1").length());
                calls.add(method + " " + path);
                if (!("Bearer " + TOKEN).equals(headers.get("Authorization"))) return new RecorderRelay.Response(401, "{}", -1);
                if (method.equals("POST") && path.equals("/uploads")) {
                    JSONObject req = new JSONObject(new String(body, StandardCharsets.UTF_8));
                    for (Map.Entry<String, Upload> e : uploads.entrySet()) {
                        Upload u = e.getValue();
                        if (!u.recordingId.equals(req.getString("recordingId"))) continue;
                        if (u.size != req.getLong("size") || !u.sha256.equals(req.getString("sha256"))) return new RecorderRelay.Response(409, "{}", -1);
                        return reply(200, e.getKey(), u);
                    }
                    Upload u = new Upload();
                    u.recordingId = req.getString("recordingId");
                    u.size = req.getLong("size");
                    u.sha256 = req.getString("sha256");
                    u.request = req;
                    String id = "up" + next++;
                    uploads.put(id, u);
                    return reply(201, id, u);
                }
                String id = path.substring(path.lastIndexOf('/') + 1);
                Upload u = uploads.get(id);
                if (u == null) return new RecorderRelay.Response(404, "{}", -1);
                if (method.equals("GET")) return reply(200, id, u);
                long offset = Long.parseLong(headers.get("Upload-Offset"));
                if (offset != u.data.size()) return reply(409, id, u);
                if (garble > 0) {
                    garble--;
                    body = new byte[body.length];
                }
                u.data.write(body);
                if (u.data.size() == u.size) {
                    if (!sha(u.data.toByteArray()).equals(u.sha256)) {
                        u.data.reset();
                        return new RecorderRelay.Response(422, "{\"error\":\"checksum mismatch\"}", -1);
                    }
                    u.status = "received";
                }
                return reply(200, id, u);
            } catch (Exception e) {
                throw new java.io.IOException(e);
            }
        }
    }

    private static RecorderRelay.Result run(FakeRecorder recorder, FakeBackend backend) throws Exception {
        return new RecorderRelay(recorder, backend, (phase, current, total, title, bytes, totalBytes) -> {}, () -> false).run(SERVER);
    }

    @Test
    public void relaysEveryRecordingAndReportsItDone() throws Exception {
        FakeRecorder recorder = new FakeRecorder();
        recorder.files.put("20260926-101500", audio(2_500_000, 0));
        recorder.files.put("20260926-120000", audio(300, 5));
        FakeBackend backend = new FakeBackend();
        RecorderRelay.Result result = run(recorder, backend);

        assertEquals(2, result.copied);
        assertEquals(0, result.failed);
        assertEquals(2, recorder.done.size());
        for (JSONObject d : recorder.done) {
            FakeBackend.Upload u = backend.uploads.get(d.getString("uploadId"));
            assertEquals("received", d.getString("status"));
            assertArrayEquals(recorder.files.get(d.getString("id")), u.data.toByteArray());
            assertEquals(1500, u.request.getJSONArray("highlights").getJSONObject(0).getLong("offsetMs"));
            assertEquals("2026-09-26T08:15:00Z", u.request.getString("recordedAt"));
        }
        assertEquals(4, backend.calls.stream().filter(c -> c.startsWith("PATCH")).count());
    }

    @Test
    public void resumesWhereTheBackendStands() throws Exception {
        byte[] data = audio(1_500_000, 0);
        FakeRecorder recorder = new FakeRecorder();
        recorder.files.put("rec-0001", data);
        FakeBackend backend = new FakeBackend();
        FakeBackend.Upload u = new FakeBackend.Upload();
        u.recordingId = "rec-0001";
        u.size = data.length;
        u.sha256 = sha(data);
        u.data.write(data, 0, 600_000);
        backend.uploads.put("up0", u);

        assertEquals(1, run(recorder, backend).copied);
        assertEquals(600_000L, (long) recorder.readOffsets.get(0));
        assertArrayEquals(data, u.data.toByteArray());
    }

    @Test
    public void conflictingIdFailsThatRecordingOnly() throws Exception {
        FakeRecorder recorder = new FakeRecorder();
        recorder.files.put("a", audio(100, 0));
        recorder.files.put("b", audio(100, 1));
        FakeBackend backend = new FakeBackend();
        FakeBackend.Upload u = new FakeBackend.Upload();
        u.recordingId = "a";
        u.size = 1;
        u.sha256 = "x";
        backend.uploads.put("up0", u);

        RecorderRelay.Result result = run(recorder, backend);
        assertEquals(1, result.copied);
        assertEquals(1, result.failed);
        assertEquals("b", recorder.done.get(0).getString("id"));
    }

    @Test
    public void startsOverAfterChecksumMismatchAndGivesUpWhenItRepeats() throws Exception {
        FakeRecorder recorder = new FakeRecorder();
        recorder.files.put("a", audio(3000, 0));
        FakeBackend backend = new FakeBackend();
        backend.garble = 1;
        assertEquals(1, run(recorder, backend).copied);

        recorder = new FakeRecorder();
        recorder.files.put("b", audio(3000, 0));
        backend = new FakeBackend();
        backend.garble = 100;
        RecorderRelay.Result result = run(recorder, backend);
        assertEquals(0, result.copied);
        assertEquals(1, result.failed);
        assertTrue(recorder.done.isEmpty());
    }

    @Test
    public void refusesRecorderWithoutTokenOrForAnotherServer() throws Exception {
        FakeRecorder recorder = new FakeRecorder();
        recorder.token = "";
        expect("no-token", recorder);
        recorder = new FakeRecorder();
        recorder.backend = "https://elsewhere.example/api/v1";
        expect("other-server", recorder);
        recorder = new FakeRecorder();
        recorder.token = "kpd_revoked";
        recorder.files.put("a", audio(10, 0));
        expect("token", recorder);
    }

    private static void expect(String code, FakeRecorder recorder) throws Exception {
        try {
            run(recorder, new FakeBackend());
            fail("expected " + code);
        } catch (RecorderRelay.RelayException e) {
            assertEquals(code, e.code);
        }
    }

    @Test
    public void originsCompare() {
        assertEquals("https://knowpod.example", RecorderRelay.originOf("https://KnowPod.example:443/api/v1"));
        assertEquals("http://host:8080", RecorderRelay.originOf("http://host:8080/api/v1"));
        assertEquals(null, RecorderRelay.originOf("ftp://host"));
    }
}
