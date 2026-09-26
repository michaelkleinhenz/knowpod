#include "worker.h"
#include <ArduinoJson.h>
#include <atomic>
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

static volatile bool paused = false;
static volatile bool step_running = false;
static volatile int pending = 0;
static std::atomic<uint32_t> generation{0};
static volatile uint32_t retry_at = 0;
static uint32_t backoff_ms = 0;
static String status, status_short, current_id;

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
        if (meta["state"] != "error") return;
        meta["state"] = meta["error_state"] | "recorded";
        meta.remove("error");
        meta.remove("error_state");
    } else {
        String state = meta["state"] | "";
        meta["template"] = cmd.tmpl;  // used when the summary is (re)done
        if (state == "summarized" || (state == "error" && meta["error_state"] == "transcribed")) {
            meta["state"] = "transcribed";
            meta.remove("error");
            meta.remove("error_state");
        }
    }
    recording_save_meta(cmd.id, meta);
    backoff_ms = 0;
    retry_at = 0;
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

static bool needs_work(const String &state)
{
    return state == "recorded" || state == "transcribing" || state == "transcribed";
}

// Oldest recording that still needs processing; counts all of them.
static bool next_recording(RecordingInfo &next)
{
    std::vector<RecordingInfo> list = recordings_list();
    int count = 0;
    bool found = false;
    for (auto it = list.rbegin(); it != list.rend(); ++it) {
        if (!needs_work(it->state)) continue;
        if (!found) next = *it;
        found = true;
        count++;
    }
    pending = count;
    return found;
}

static void backoff(const String &reason, int retry_after_s = 0)
{
    backoff_ms = backoff_ms ? min<uint32_t>(backoff_ms * 2, BACKOFF_MAX_MS) : BACKOFF_MIN_MS;
    backoff_ms = max<uint32_t>(backoff_ms, min(retry_after_s, 3600) * 1000);
    retry_at = millis() + backoff_ms;
    String when = backoff_ms >= 60000 ? String(backoff_ms / 60000) + " min" : String(backoff_ms / 1000) + " s";
    set_status("Retrying in " + when + ": " + reason, "Retry in " + when);
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
    if (state == "recorded") {
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
        backoff_ms = 0;
        recording_save_meta(info.id, meta);
        break;
    case STEP_RETRY:
        recording_save_meta(info.id, meta);
        backoff(step.error, step.retry_after_s);
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

        RecordingInfo next;
        if (!next_recording(next)) {
            set_status("", "");
            if (millis() - idle_since > WIFI_IDLE_OFF_MS) wifi_off();  // unless held by the web server
            wait = IDLE_WAIT_MS;
            continue;
        }
        idle_since = millis();

        int32_t until_retry = (int32_t)(retry_at - millis());
        if (retry_at && until_retry > 0) {
            wait = until_retry;
            continue;
        }

        if (!wifi_connect()) {
            backoff("no Wi-Fi");
            continue;
        }
        process(next);
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
        backoff_ms = 0;
        retry_at = 0;
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
int worker_pending()          { return pending; }
uint32_t worker_generation()  { return generation + recordings_generation(); }

bool worker_busy()
{
    if (step_running) return true;
    {
        Lock lock;
        if (ask_state == ASK_RUNNING) return true;
    }
    int32_t until_retry = (int32_t)(retry_at - millis());
    return pending > 0 && !paused && !(retry_at && until_retry > 0);
}
