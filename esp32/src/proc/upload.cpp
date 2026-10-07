#include "pipeline.h"
#include <mbedtls/sha256.h>
#include "net/http.h"
#include "store/config.h"
#include "store/recordings.h"

// Resumable upload to a knowpod-service backend (openapi.yaml, "Device uploads"):
//   POST  /uploads            {recordingId, size, sha256, recordedAt, highlights}
//                                                                     -> uploadId, offset
//   PATCH /uploads/{uploadId} Upload-Offset + chunk                   -> new offset, status
//   PUT   /uploads/{uploadId}/highlights                              (recordings uploaded
//                                                                      before highlights)
// Progress lives in meta.json "upload" and is saved after every step, so a
// reboot or a lost connection resumes at the last confirmed byte.

#define UPLOAD_CHUNK_BYTES   (4u * 1024 * 1024)
#define API_TIMEOUT_MS       30000
#define CHUNK_TIMEOUT_MS     180000
#define MAX_CHECKSUM_RESETS  2

static HttpHeaders auth_headers()
{
    return {{"Authorization", "Bearer " + config_backend_token()}};
}

// Server states from which the upload needs nothing more from us
static bool remote_complete(const String &status)
{
    return status == "received" || status == "stored" || status == "transcribed" || status == "summarized";
}

#define BAD_TOKEN_RETRY_S    1800

static Step api_error(const HttpResponse &r, const JsonDocument &body)
{
    if (r.status < 0) return {STEP_RETRY, "Backend: " + r.body};
    // A wrong or revoked token is a settings problem, not the recording's:
    // pause all uploads and continue by themselves once the token is fixed.
    if (r.status == 401)
        return {STEP_RETRY, "Backend rejected the device token (backend.token in config.json)", BAD_TOKEN_RETRY_S};
    String msg = body["error"] | r.body.substring(0, 200);
    bool retry = r.status == 408 || r.status == 429 || r.status >= 500;
    return {retry ? STEP_RETRY : STEP_FAILED, "Backend HTTP " + String(r.status) + ": " + msg, r.retry_after_s};
}

// Highlights as the API expects them: [{"offsetMs": ...}]
static void add_highlights(JsonDocument &req, const JsonDocument &meta)
{
    JsonArray list = req["highlights"].to<JsonArray>();
    for (float h : meta["highlights"].as<JsonArrayConst>())
        list.add<JsonObject>()["offsetMs"] = (int64_t)lroundf(h * 1000);
}

static bool sha256_file(File &f, String &hex)
{
    mbedtls_sha256_context ctx;
    mbedtls_sha256_init(&ctx);
    mbedtls_sha256_starts(&ctx, 0);
    f.seek(0);
    uint8_t buf[4096];
    size_t n;
    size_t total = 0;
    while ((n = f.read(buf, sizeof(buf))) > 0) {
        mbedtls_sha256_update(&ctx, buf, n);
        total += n;
    }
    uint8_t digest[32];
    mbedtls_sha256_finish(&ctx, digest);
    mbedtls_sha256_free(&ctx);
    if (total != f.size()) return false;

    hex = "";
    for (uint8_t b : digest) {
        char h[3];
        snprintf(h, sizeof(h), "%02x", b);
        hex += h;
    }
    return true;
}

// Applies an Upload object from the server; returns true when the upload is complete.
static bool apply_state(JsonObject up, const JsonDocument &body, const HttpResponse &r)
{
    long offset = body["offset"] | r.upload_offset;
    if (offset >= 0) up["offset"] = offset;
    String status = body["status"] | "";
    if (!status.isEmpty()) up["remote_status"] = status;
    if (!remote_complete(status)) return false;
    up["status"] = "done";
    return true;
}

Step upload_next(const String &id, JsonDocument &meta, int &percent)
{
    File f = recordings_fs().open(recording_audio_path(id), FILE_READ);
    if (!f) return {STEP_FAILED, "Audio file missing"};
    size_t size = f.size();

    JsonObject up = meta["upload"].is<JsonObject>() ? meta["upload"].as<JsonObject>()
                                                    : meta["upload"].to<JsonObject>();
    up["status"] = "uploading";

    // 1. Checksum of the finished WAV file
    if (!up["sha256"].is<const char *>() || (size_t)(up["size"] | 0) != size) {
        String hex;
        bool ok = sha256_file(f, hex);
        f.close();
        if (!ok) return {STEP_RETRY, "Could not read the audio file"};
        up["sha256"] = hex;
        up["size"] = size;
        up["offset"] = 0;
        up.remove("upload_id");
        percent = 0;
        return {STEP_OK, ""};
    }

    long offset = up["offset"] | 0L;
    percent = size ? (int)(100.0 * offset / size) : 0;
    String base = config_backend_url();

    // 2. Create (or look up) the upload; idempotent per recordingId
    if (!up["upload_id"].is<const char *>()) {
        f.close();
        JsonDocument req;
        req["recordingId"] = id;
        req["size"] = size;
        req["sha256"] = up["sha256"];
        uint32_t created = meta["created_unix"] | 0;
        if (created) {
            time_t t = created;
            tm utc;
            gmtime_r(&t, &utc);
            char iso[32];
            strftime(iso, sizeof(iso), "%Y-%m-%dT%H:%M:%SZ", &utc);
            req["recordedAt"] = iso;
        }
        add_highlights(req, meta);
        HttpResponse r = http_request(base + "/uploads", "POST", auth_headers(), "application/json",
                                      measureJson(req), [&](Print &out) {
                                          serializeJson(req, out);
                                          return true;
                                      }, API_TIMEOUT_MS);
        JsonDocument body;
        deserializeJson(body, r.body);
        if (r.status == 409)
            return {STEP_FAILED, "The backend already has a different recording with this id"};
        if (r.status != 200 && r.status != 201) return api_error(r, body);
        if (body["status"] == "failed")
            return {STEP_FAILED, "Backend rejected the recording: " + String(body["error"] | "")};

        up["upload_id"] = body["uploadId"] | "";
        up["highlights_synced"] = true;  // sent with the create
        apply_state(up, body, r);
        return {STEP_OK, ""};
    }

    // 3. Send the next chunk from the confirmed offset
    size_t n = min<size_t>(UPLOAD_CHUNK_BYTES, size - min<size_t>(offset, size));
    String url = base + "/uploads/" + String(up["upload_id"] | "");
    HttpHeaders headers = auth_headers();
    headers.push_back({"Upload-Offset", String(offset)});

    HttpResponse r;
    if (n == 0) {
        // Everything sent but not confirmed (e.g. the last reply got lost): ask
        f.close();
        r = http_request(url, "GET", auth_headers(), "", 0, nullptr, API_TIMEOUT_MS);
    } else {
        r = http_request(url, "PATCH", headers, "application/offset+octet-stream", n, [&](Print &out) {
            f.seek(offset);
            uint8_t buf[4096];
            size_t left = n;
            while (left > 0) {
                size_t got = f.read(buf, min<size_t>(left, sizeof(buf)));
                if (got == 0 || out.write(buf, got) != got) return false;
                left -= got;
            }
            return true;
        }, CHUNK_TIMEOUT_MS);
        f.close();
    }

    JsonDocument body;
    deserializeJson(body, r.body);
    switch (r.status) {
    case 200:
        if (body["status"] == "failed")
            return {STEP_FAILED, "Backend rejected the recording: " + String(body["error"] | "")};
        if (!apply_state(up, body, r) && n > 0 && (body["offset"].isNull() && r.upload_offset < 0))
            up["offset"] = offset + (long)n;  // server gave no offset; assume the chunk landed
        break;
    case 409:  // offset mismatch: continue from the server's offset
        apply_state(up, body, r);
        break;
    case 404:  // upload unknown (e.g. removed on the server): start a new one
        up.remove("upload_id");
        up["offset"] = 0;
        break;
    case 422: {  // checksum mismatch (server reset to 0) or not a valid WAV
        int resets = (up["checksum_resets"] | 0) + 1;
        up["checksum_resets"] = resets;
        if (resets > MAX_CHECKSUM_RESETS)
            return {STEP_FAILED, "Backend: " + String(body["error"] | "checksum mismatch")};
        up.remove("sha256");  // hash the file again and start over
        up.remove("upload_id");
        up["offset"] = 0;
        break;
    }
    default:
        return api_error(r, body);
    }

    long now = up["offset"] | 0L;
    percent = up["status"] == "done" ? 100 : size ? (int)(100.0 * now / size) : 0;
    return {STEP_OK, ""};
}

Step upload_highlights(const String &id, JsonDocument &meta)
{
    JsonObject up = meta["upload"];
    String upload_id = up["upload_id"] | "";
    if (upload_id.isEmpty()) {
        up["highlights_synced"] = true;  // nothing to attach them to
        return {STEP_OK, ""};
    }

    JsonDocument req;
    add_highlights(req, meta);
    HttpResponse r = http_request(config_backend_url() + "/uploads/" + upload_id + "/highlights", "PUT",
                                  auth_headers(), "application/json", measureJson(req), [&](Print &out) {
                                      serializeJson(req, out);
                                      return true;
                                  }, API_TIMEOUT_MS);
    JsonDocument body;
    deserializeJson(body, r.body);
    if (r.status == 200 || r.status == 404) {  // 404: gone on the backend; nothing to update
        up["highlights_synced"] = true;
        return {STEP_OK, ""};
    }
    return api_error(r, body);
}
