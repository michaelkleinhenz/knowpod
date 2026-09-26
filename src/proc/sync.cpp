#include "pipeline.h"
#include "net/http.h"
#include "store/config.h"
#include "store/recordings.h"

// Downloads transcripts and summaries made by a knowpod-service backend.
// The device token only covers /uploads, so reading recordings needs a user
// session: POST /auth/login with backend.email/password, then
// GET /recordings/{id} with the session cookie.

#define API_TIMEOUT_MS      30000
#define POLL_S              60      // while the backend is still processing
#define NOT_FOUND_POLL_S    120     // uploaded but not visible yet
#define BAD_LOGIN_RETRY_S   1800
#define LIST_LIMIT          200

static String session;   // "knowpod_session=..." (worker task only)

static Step login()
{
    String email = config_backend_email(), password = config_backend_password();
    if (email.isEmpty() || password.isEmpty())
        return {STEP_RETRY, "Set backend.email and backend.password in config.json to download results",
                BAD_LOGIN_RETRY_S};

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

// GET with the session; signs in (again) when needed.
static Step api_get(const String &path, JsonDocument &out, int &status)
{
    for (int attempt = 0; attempt < 2; attempt++) {
        if (session.isEmpty()) {
            Step s = login();
            if (s.result != STEP_OK) return s;
        }
        HttpResponse r = http_request(config_backend_url() + path, "GET", {{"Cookie", session}}, "", 0,
                                      nullptr, API_TIMEOUT_MS);
        status = r.status;
        if (r.status == 401) {  // session expired
            session = "";
            continue;
        }
        if (r.status < 0) return {STEP_RETRY, "Backend: " + r.body};
        if (r.status == 429 || r.status >= 500)
            return {STEP_RETRY, "Backend HTTP " + String(r.status), r.retry_after_s};
        out.clear();
        if (r.status == 200 && deserializeJson(out, r.body)) return {STEP_RETRY, "Backend: invalid JSON"};
        return {STEP_OK, ""};
    }
    return {STEP_RETRY, "Backend login failed", BAD_LOGIN_RETRY_S};
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
        Step s = api_get("/recordings/" + upload_id, doc, status);
        if (s.result != STEP_OK) return s;
        if (status == 200 && sha.equalsIgnoreCase(doc["sha256"] | "")) {
            remote_id = upload_id;
            return {STEP_OK, ""};
        }
    }

    Step s = api_get("/recordings?limit=" + String(LIST_LIMIT), doc, status);
    if (s.result != STEP_OK) return s;
    for (JsonObjectConst rec : doc.as<JsonArrayConst>()) {
        if (sha.equalsIgnoreCase(rec["sha256"] | "")) {
            remote_id = rec["id"] | "";
            return {STEP_OK, ""};
        }
    }
    return {STEP_WAIT, "Not visible on the backend yet (is backend.email the device's owner?)", NOT_FOUND_POLL_S};
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

    JsonDocument rec;
    int status = 0;
    Step s = api_get("/recordings/" + remote_id, rec, status);
    if (s.result != STEP_OK) return s;
    if (status == 404) {  // deleted or replaced on the backend: look it up again
        up.remove("remote_id");
        return {STEP_WAIT, "Recording not found on the backend", NOT_FOUND_POLL_S};
    }
    if (status != 200) return {STEP_RETRY, "Backend HTTP " + String(status)};

    String remote_status = rec["status"] | "";
    if (remote_status == "failed")
        return {STEP_FAILED, "Backend: " + String(rec["lastError"] | "processing failed")};

    // Transcript
    String text = rec["transcript"]["text"] | "";
    String state = meta["state"] | "recorded";
    if (!text.isEmpty() && state != "transcribed" && state != "summarized") {
        if (!write_file(recording_path(id, "transcript.md"), "# Transcript\n\n" + text + "\n"))
            return {STEP_FAILED, "Cannot write transcript.md"};
        meta["state"] = "transcribed";
        meta["transcript_model"] = rec["transcript"]["model"] | "";
    }

    // Summary
    String markdown = rec["summary"]["markdown"] | "";
    if (!markdown.isEmpty()) {
        String title = rec["summary"]["title"] | "";
        if (title.isEmpty()) title = "Untitled";
        JsonDocument summary;
        summary["title"] = title;
        summary["summary"] = markdown;
        String json;
        serializeJsonPretty(summary, json);
        if (!write_file(recording_path(id, "summary.json"), json) ||
            !write_file(recording_path(id, "summary.md"), "# " + title + "\n\n" + markdown + "\n"))
            return {STEP_FAILED, "Cannot write summary"};
        meta["title"] = title;
        meta["model"] = rec["summary"]["model"] | "";
        meta["template"] = "backend";
        meta["state"] = "summarized";
        return {STEP_OK, ""};
    }

    String what = text.isEmpty() ? "transcribe" : "summarize";
    return {STEP_WAIT, "Waiting for the backend to " + what + " (" + remote_status + ")", POLL_S};
}
