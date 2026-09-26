#include "worker.h"
#include <ArduinoJson.h>
#include <atomic>
#include <map>
#include "net/wifi.h"
#include "pipeline.h"
#include "store/config.h"
#include "store/recordings.h"

#define WORKER_STACK       16384
#define IDLE_WAIT_MS       30000
#define WIFI_IDLE_OFF_MS   60000
#define BACKOFF_MIN_MS     30000
#define BACKOFF_MAX_MS     600000

struct Command {
    enum Type : uint8_t { RETRY, RESUMMARIZE } type;
    char id[65];
    char tmpl[65];
};

static TaskHandle_t task;
static QueueHandle_t commands;
static SemaphoreHandle_t mutex = xSemaphoreCreateMutex();

// Processing (transcribe/summarize) and backend uploads are independent
// lanes with their own retry timers, so a rate-limited OpenRouter doesn't
// hold back uploads and an unreachable backend doesn't block summaries.
struct Lane {
    const char *name;
    volatile int pending = 0;   // recordings that still need this lane
    volatile int ready = 0;     // ... of which can be worked on right now
    volatile uint32_t retry_at = 0;
    uint32_t backoff_ms = 0;

    bool waiting() const { return retry_at && (int32_t)(retry_at - millis()) > 0; }
    void reset() { backoff_ms = 0; retry_at = 0; }
};

static volatile bool paused = false;
static volatile bool step_running = false;
static Lane processing = {"processing"};
static Lane uploads = {"upload"};
static std::atomic<uint32_t> generation{0};
static String status, status_short, current_id;

// Recordings waiting for something outside the device (the backend still
// processing them) are checked again at this time (worker task only).
static std::map<String, uint32_t> not_before;

static bool deferred(const String &id)
{
    auto it = not_before.find(id);
    if (it == not_before.end()) return false;
    if ((int32_t)(it->second - millis()) > 0) return true;
    not_before.erase(it);
    return false;
}

// Ask state (guarded by mutex)
static AskState ask_state = ASK_IDLE;
static bool ask_requested = false;
static bool ask_ok = false;
static String ask_wav, ask_scope, ask_question, ask_answer;

struct Lock {
    Lock()  { xSemaphoreTake(mutex, portMAX_DELAY); }
    ~Lock() { xSemaphoreGive(mutex); }
};

static void set_status(const String &full, const String &brief, const String &id = String())
{
    Lock lock;
    if (full == status && brief == status_short && id == current_id) return;
    status = full;
    status_short = brief;
    current_id = id;
    generation++;
    if (!full.isEmpty()) Serial.printf("[worker] %s\n", full.c_str());
}

// ============================================================
// Commands
// ============================================================

static void apply_command(const Command &cmd)
{
    JsonDocument meta;
    if (!recording_load_meta(cmd.id, meta)) return;
    if (cmd.type == Command::RETRY) {
        if (meta["state"] == "error") {
            meta["state"] = meta["error_state"] | "recorded";
            meta.remove("error");
            meta.remove("error_state");
            processing.reset();
        }
        if (meta["upload"]["status"] == "failed") {
            meta["upload"]["status"] = "uploading";
            meta["upload"].remove("error");
            meta["upload"].remove("checksum_resets");
            uploads.reset();
        }
    } else {
        String state = meta["state"] | "";
        meta["template"] = cmd.tmpl;  // used when the summary is (re)done
        if (state == "summarized" || (state == "error" && meta["error_state"] == "transcribed")) {
            meta["state"] = "transcribed";
            meta.remove("error");
            meta.remove("error_state");
        }
        processing.reset();
    }
    recording_save_meta(cmd.id, meta);
}

static void send_command(Command::Type type, const String &id, const String &tmpl)
{
    Command cmd = {type};
    strlcpy(cmd.id, id.c_str(), sizeof(cmd.id));
    strlcpy(cmd.tmpl, tmpl.c_str(), sizeof(cmd.tmpl));
    xQueueSend(commands, &cmd, 0);
    worker_kick();
}

void worker_retry(const String &id)                          { send_command(Command::RETRY, id, ""); }
void worker_resummarize(const String &id, const String &tmpl) { send_command(Command::RESUMMARIZE, id, tmpl); }

// ============================================================
// Processing
// ============================================================

static bool needs_processing(const RecordingInfo &r)
{
    bool open = r.state == "recorded" || r.state == "transcribing" || r.state == "transcribed";
    // With backend processing, results can only be fetched once uploaded
    if (open && config_processing_backend()) return config_backend_enabled() && r.upload == "done";
    return open;
}

static bool needs_upload(const RecordingInfo &r)
{
    if (r.state == "recording" || r.upload == "failed") return false;
    return r.upload != "done" || r.highlights_unsynced;
}

// Oldest recording of each lane that can be worked on now; counts the
// pending ones. `next_check` is the earliest time a deferred one is due.
static void find_work(RecordingInfo &proc, bool &has_proc, RecordingInfo &upl, bool &has_upl,
                      uint32_t &next_check_ms)
{
    std::vector<RecordingInfo> list = recordings_list();
    bool backend = config_backend_enabled();
    int n_proc = 0, n_upl = 0;
    has_proc = has_upl = false;
    next_check_ms = UINT32_MAX;
    for (auto it = list.rbegin(); it != list.rend(); ++it) {
        if (needs_processing(*it)) {
            n_proc++;
            if (deferred(it->id)) {
                next_check_ms = min<uint32_t>(next_check_ms, not_before[it->id] - millis());
            } else if (!has_proc) {
                proc = *it;
                has_proc = true;
            }
        }
        if (backend && needs_upload(*it)) {
            if (!n_upl++) upl = *it;
        }
    }
    has_upl = n_upl > 0;
    processing.pending = n_proc;
    processing.ready = has_proc;
    uploads.pending = n_upl;
    uploads.ready = n_upl;
}

static void backoff(Lane &lane, const String &reason, int retry_after_s = 0)
{
    lane.backoff_ms = lane.backoff_ms ? min<uint32_t>(lane.backoff_ms * 2, BACKOFF_MAX_MS) : BACKOFF_MIN_MS;
    lane.backoff_ms = max<uint32_t>(lane.backoff_ms, min(retry_after_s, 3600) * 1000);
    lane.retry_at = millis() + lane.backoff_ms;
    String when = lane.backoff_ms >= 60000 ? String(lane.backoff_ms / 60000) + " min"
                                           : String(lane.backoff_ms / 1000) + " s";
    set_status("Retrying " + String(lane.name) + " in " + when + ": " + reason, "Retry in " + when);
}

static void upload(const RecordingInfo &info)
{
    JsonDocument meta;
    if (!recording_load_meta(info.id, meta)) return;
    String title = recording_display_title(info);
    set_status("Uploading " + title + " (" + String(info.upload_percent) + "%)",
               "Upload " + String(info.upload_percent) + "%", info.id);

    int percent = 0;
    step_running = true;
    Step step = info.upload == "done" ? upload_highlights(info.id, meta) : upload_next(info.id, meta, percent);
    step_running = false;

    if (paused && step.result != STEP_OK) return;  // Wi-Fi switched off for a recording

    switch (step.result) {
    case STEP_OK:
        uploads.backoff_ms = 0;
        if (meta["upload"]["status"] == "done") Serial.printf("[worker] %s uploaded\n", info.id.c_str());
        break;
    case STEP_RETRY:
        backoff(uploads, step.error, step.retry_after_s);
        break;
    case STEP_WAIT:
        break;
    case STEP_FAILED:
        Serial.printf("[worker] upload of %s failed: %s\n", info.id.c_str(), step.error.c_str());
        meta["upload"]["status"] = "failed";
        meta["upload"]["error"] = step.error;
        break;
    }
    recording_save_meta(info.id, meta);
}

static void process(const RecordingInfo &info)
{
    JsonDocument meta;
    if (!recording_load_meta(info.id, meta)) return;
    String state = meta["state"] | "recorded";
    String title = recording_display_title(info);
    String progress;
    Step step = {STEP_OK, ""};

    step_running = true;
    if (config_processing_backend()) {
        set_status("Getting results for " + title, "Syncing", info.id);
        step = sync_from_backend(info.id, meta);
    } else if (state == "recorded") {
        // Fresh start: drop partial results from an earlier attempt
        recordings_fs().remove(recording_path(info.id, "transcript.json"));
        meta["state"] = "transcribing";
        meta["transcribed_s"] = 0;
    } else if (state == "transcribing") {
        float duration = meta["duration_s"] | 0.0f;
        int percent = duration > 0 ? (int)(100 * (meta["transcribed_s"] | 0.0f) / duration) : 0;
        set_status("Transcribing " + title + " (" + String(percent) + "%)",
                   "Transcribing " + String(percent) + "%", info.id);
        step = transcribe_next_chunk(info.id, meta, progress);
        if (!progress.isEmpty()) Serial.printf("[worker] %s: %s\n", info.id.c_str(), progress.c_str());
    } else if (state == "transcribed" && config_speaker_labels() && !(meta["speakers_done"] | false)) {
        set_status("Labeling speakers in " + title, "Speakers", info.id);
        step = label_speakers(info.id, meta);
    } else if (state == "transcribed") {
        set_status("Summarizing " + title, "Summarizing", info.id);
        step = summarize(info.id, meta);
    }
    step_running = false;

    // Wi-Fi is switched off when a recording starts; that is not a real failure
    if (paused && step.result != STEP_OK) return;

    switch (step.result) {
    case STEP_OK:
        processing.backoff_ms = 0;
        recording_save_meta(info.id, meta);
        break;
    case STEP_WAIT:
        // Only this recording waits; others are processed meanwhile
        processing.backoff_ms = 0;
        recording_save_meta(info.id, meta);
        not_before[info.id] = millis() + max(step.retry_after_s, 10) * 1000;
        set_status(title + ": " + step.error, "");
        break;
    case STEP_RETRY:
        recording_save_meta(info.id, meta);
        backoff(processing, step.error, step.retry_after_s);
        break;
    case STEP_FAILED:
        Serial.printf("[worker] %s failed: %s\n", info.id.c_str(), step.error.c_str());
        meta["error"] = step.error;
        meta["error_state"] = meta["state"];
        meta["state"] = "error";
        recording_save_meta(info.id, meta);
        break;
    }
}

static void run_ask()
{
    String wav, scope;
    {
        Lock lock;
        wav = ask_wav;
        scope = ask_scope;
        ask_requested = false;
    }
    set_status("Answering question", "Thinking");

    String question, answer;
    Step step = {STEP_FAILED, "No Wi-Fi connection."};
    if (wifi_connect()) step = answer_question(wav, scope, question, answer);

    Lock lock;
    ask_question = question;
    ask_answer = step.result == STEP_OK ? answer : step.error;
    ask_ok = step.result == STEP_OK;
    ask_state = ASK_DONE;
    generation++;
}

static void worker_task(void *)
{
    uint32_t idle_since = millis();
    uint32_t wait = 0;

    for (;;) {
        ulTaskNotifyTake(pdTRUE, pdMS_TO_TICKS(wait));
        wait = 0;

        if (paused) {
            set_status("", "");
            wait = 1000;
            continue;
        }

        Command cmd;
        while (xQueueReceive(commands, &cmd, 0) == pdTRUE) apply_command(cmd);

        bool ask;
        {
            Lock lock;
            ask = ask_requested;
        }
        if (ask) {
            run_ask();
            idle_since = millis();
            continue;
        }

        RecordingInfo proc, upl;
        bool has_proc, has_upl;
        uint32_t next_check;
        find_work(proc, has_proc, upl, has_upl, next_check);
        if (!has_proc && !has_upl) {
            if (next_check == UINT32_MAX) set_status("", "");  // keep "waiting for the backend" visible
            if (millis() - idle_since > WIFI_IDLE_OFF_MS) wifi_off();  // unless held by the web server
            wait = min<uint32_t>(IDLE_WAIT_MS, next_check);
            continue;
        }
        idle_since = millis();

        bool run_proc = has_proc && !processing.waiting();
        bool run_upl = !run_proc && has_upl && !uploads.waiting();
        if (!run_proc && !run_upl) {
            // Both lanes are backing off: sleep until the first one may retry
            uint32_t now = millis();
            uint32_t until = UINT32_MAX;
            if (has_proc) until = min<uint32_t>(until, processing.retry_at - now);
            if (has_upl) until = min<uint32_t>(until, uploads.retry_at - now);
            wait = until;
            continue;
        }

        if (!wifi_connect()) {
            if (has_proc) backoff(processing, "no Wi-Fi");
            if (has_upl) backoff(uploads, "no Wi-Fi");
            continue;
        }
        if (run_proc) process(proc);
        else upload(upl);
    }
}

// ============================================================
// Public API
// ============================================================

void worker_begin()
{
    commands = xQueueCreate(8, sizeof(Command));
    xTaskCreatePinnedToCore(worker_task, "worker", WORKER_STACK, nullptr, 1, &task, 0);
}

void worker_kick()
{
    if (task) xTaskNotifyGive(task);
}

void worker_set_paused(bool p)
{
    paused = p;
    if (!p) {
        processing.reset();
        uploads.reset();
        worker_kick();
    }
}

bool worker_ask(const String &question_wav, const String &scope_id)
{
    {
        Lock lock;
        if (ask_state == ASK_RUNNING) return false;
        ask_wav = question_wav;
        ask_scope = scope_id;
        ask_requested = true;
        ask_state = ASK_RUNNING;
    }
    worker_kick();
    return true;
}

AskState worker_ask_state(String &question, String &answer, bool &ok)
{
    Lock lock;
    question = ask_question;
    answer = ask_answer;
    ok = ask_ok;
    return ask_state;
}

void worker_ask_reset()
{
    Lock lock;
    if (ask_state == ASK_DONE) ask_state = ASK_IDLE;
}

String worker_status()        { Lock l; return status; }
String worker_status_short()  { Lock l; return status_short; }
String worker_current_id()    { Lock l; return current_id; }
int worker_pending()          { return processing.pending; }
int worker_pending_uploads()  { return uploads.pending; }
uint32_t worker_generation()  { return generation + recordings_generation(); }

bool worker_busy()
{
    if (step_running) return true;
    {
        Lock lock;
        if (ask_state == ASK_RUNNING) return true;
    }
    if (paused) return false;
    return (processing.ready > 0 && !processing.waiting()) || (uploads.ready > 0 && !uploads.waiting());
}
