#include "pipeline.h"
#include "net/http.h"
#include "store/config.h"
#include "store/recordings.h"

// Downloads transcripts and summaries made by a knowpod-service backend:
//   GET /recordings/{id}             status (and "failed" with lastError)
//   GET /recordings/{id}/transcript  text with [m:ss] time stamps per speaker turn
//   GET /recordings/{id}/summary?format=json
//
// These endpoints accept a user session or the admin token (API 0.10). The
// device token is tried first, so once the backend accepts it there,
// backend.email/password can be removed from config.json; until then the
// device signs in as the recordings' owner with POST /auth/login.

#define API_TIMEOUT_MS      30000
#define POLL_S              60      // while the backend is still processing
#define NOT_FOUND_POLL_S    120     // uploaded but not visible yet
#define BAD_LOGIN_RETRY_S   1800
#define LIST_LIMIT          200

// Worker task only
static String session;                      // "knowpod_session=..."
static bool device_token_rejected = false;  // backend doesn't let the device token read recordings

static Step login()
{
    String email = config_backend_email(), password = config_backend_password();
    if (email.isEmpty() || password.isEmpty())
        return {STEP_RETRY, "The backend doesn't let the device token download results; set "
                            "backend.email and backend.password in config.json", BAD_LOGIN_RETRY_S};

    JsonDocument req;
    req["email"] = email;
    req["password"] = password;
    HttpResponse r = http_request(config_backend_url() + "/auth/login", "POST", {}, "application/json",
                                  measureJson(req), [&](Print &out) {
                                      serializeJson(req, out);
                                      return true;
                                  }, API_TIMEOUT_MS);
    if (r.status == 200 && !r.cookies.isEmpty()) {
        session = r.cookies;
        return {STEP_OK, ""};
    }
    if (r.status == 401) return {STEP_RETRY, "Backend login failed (backend.email/password)", BAD_LOGIN_RETRY_S};
    if (r.status == 429) return {STEP_RETRY, "Backend login rate limited", max(r.retry_after_s, 60)};
    return {STEP_RETRY, "Backend login: " + (r.status < 0 ? r.body : "HTTP " + String(r.status)), r.retry_after_s};
}

// GET with the device token, or with a user session if the backend refuses
// the token. On success `r` holds the response (any status but 401/5xx).
static Step api_get(const String &path, HttpResponse &r)
{
    for (int attempt = 0; attempt < 3; attempt++) {
        HttpHeaders headers;
        if (!device_token_rejected) {
            headers.push_back({"Authorization", "Bearer " + config_backend_token()});
        } else {
            if (session.isEmpty()) {
                Step s = login();
                if (s.result != STEP_OK) return s;
            }
            headers.push_back({"Cookie", session});
        }

        r = http_request(config_backend_url() + path, "GET", headers, "", 0, nullptr, API_TIMEOUT_MS);
        if (r.status == 401) {
            if (!device_token_rejected) {
                device_token_rejected = true;
                Serial.println("Backend: device token can't read recordings; using backend.email/password");
            } else {
                session = "";  // expired
            }
            continue;
        }
        if (r.status < 0) return {STEP_RETRY, "Backend: " + r.body};
        if (r.status == 429 || r.status >= 500)
            return {STEP_RETRY, "Backend HTTP " + String(r.status), r.retry_after_s};
        return {STEP_OK, ""};
    }
    return {STEP_RETRY, "Backend login failed", BAD_LOGIN_RETRY_S};
}

static Step api_get_json(const String &path, JsonDocument &doc, int &status)
{
    HttpResponse r;
    Step s = api_get(path, r);
    if (s.result != STEP_OK) return s;
    status = r.status;
    doc.clear();
    if (r.status == 200 && deserializeJson(doc, r.body)) return {STEP_RETRY, "Backend: invalid JSON"};
    return {STEP_OK, ""};
}

// Finds the backend recording for this upload: the upload id first, then
// the list, matched by the audio checksum (unique per file).
static Step find_remote(JsonObject up, String &remote_id)
{
    String sha = up["sha256"] | "";
    String upload_id = up["upload_id"] | "";
    JsonDocument doc;
    int status = 0;

    if (!upload_id.isEmpty()) {
        Step s = api_get_json("/recordings/" + upload_id, doc, status);
        if (s.result != STEP_OK) return s;
        if (status == 200 && sha.equalsIgnoreCase(doc["sha256"] | "")) {
            remote_id = upload_id;
            return {STEP_OK, ""};
        }
    }

    Step s = api_get_json("/recordings?limit=" + String(LIST_LIMIT), doc, status);
    if (s.result != STEP_OK) return s;
    for (JsonObjectConst rec : doc.as<JsonArrayConst>()) {
        if (sha.equalsIgnoreCase(rec["sha256"] | "")) {
            remote_id = rec["id"] | "";
            return {STEP_OK, ""};
        }
    }
    return {STEP_WAIT, "Not visible on the backend yet", NOT_FOUND_POLL_S};
}

Step sync_from_backend(const String &id, JsonDocument &meta)
{
    JsonObject up = meta["upload"];
    String remote_id = up["remote_id"] | "";
    if (remote_id.isEmpty()) {
        Step s = find_remote(up, remote_id);
        if (s.result != STEP_OK) return s;
        up["remote_id"] = remote_id;
    }
    String base = "/recordings/" + remote_id;

    JsonDocument rec;
    int status = 0;
    Step s = api_get_json(base, rec, status);
    if (s.result != STEP_OK) return s;
    if (status == 404) {  // deleted or replaced on the backend: look it up again
        up.remove("remote_id");
        return {STEP_WAIT, "Recording not found on the backend", NOT_FOUND_POLL_S};
    }
    if (status != 200) return {STEP_RETRY, "Backend HTTP " + String(status)};

    String remote_status = rec["status"] | "";
    if (remote_status == "failed")
        return {STEP_FAILED, "Backend: " + String(rec["lastError"] | "processing failed")};

    // Transcript (plain text with [m:ss] time stamps per speaker turn)
    String state = meta["state"] | "recorded";
    if (state != "transcribed" && state != "summarized") {
        HttpResponse r;
        s = api_get(base + "/transcript", r);
        if (s.result != STEP_OK) return s;
        if (r.status == 200) {
            if (!write_file(recording_path(id, "transcript.md"), "# Transcript\n\n" + r.body + "\n"))
                return {STEP_FAILED, "Cannot write transcript.md"};
            meta["state"] = "transcribed";
            meta["transcript_model"] = rec["transcript"]["model"] | "";
            state = "transcribed";
        } else if (r.status != 409) {  // 409: no transcript yet
            return {STEP_RETRY, "Backend transcript: HTTP " + String(r.status)};
        }
    }

    // Summary
    if (state == "transcribed") {
        JsonDocument sum;
        s = api_get_json(base + "/summary?format=json", sum, status);
        if (s.result != STEP_OK) return s;
        if (status == 200) {
            String title = sum["title"] | "";
            if (title.isEmpty()) title = "Untitled";
            String markdown = sum["markdown"] | "";
            JsonDocument summary;
            summary["title"] = title;
            summary["summary"] = markdown;
            String json;
            serializeJsonPretty(summary, json);
            if (!write_file(recording_path(id, "summary.json"), json) ||
                !write_file(recording_path(id, "summary.md"), "# " + title + "\n\n" + markdown + "\n"))
                return {STEP_FAILED, "Cannot write summary"};
            meta["title"] = title;
            meta["model"] = sum["model"] | "";
            meta["template"] = sum["themeName"] | (sum["themeId"] | "backend");
            meta["state"] = "summarized";
            return {STEP_OK, ""};
        }
        if (status != 409) return {STEP_RETRY, "Backend summary: HTTP " + String(status)};  // 409: not yet
    }

    String what = state == "transcribed" ? "summarize" : "transcribe";
    return {STEP_WAIT, "Waiting for the backend to " + what + " (" + remote_status + ")", POLL_S};
}
