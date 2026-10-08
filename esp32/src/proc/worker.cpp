#include "worker.h"
#include <ArduinoJson.h>
#include <atomic>
#include "net/wifi.h"
#include "upload.h"
#include "store/config.h"
#include "store/recordings.h"

#define WORKER_STACK       16384
#define IDLE_WAIT_MS       30000
#define WIFI_IDLE_OFF_MS   60000
#define BACKOFF_MIN_MS     30000
#define BACKOFF_MAX_MS     600000

static TaskHandle_t task;
static QueueHandle_t retries;   // ids of recordings whose failed upload should be retried
static SemaphoreHandle_t mutex = xSemaphoreCreateMutex();

static volatile bool paused = false;
static volatile bool step_running = false;
static volatile int pending = 0;   // recordings that still need uploading
static volatile uint32_t retry_at = 0;
static uint32_t backoff_ms = 0;
static std::atomic<uint32_t> generation{0};
static String status, status_short, current_id;

namespace {  // file-local: each file's Lock guards its own mutex
struct Lock {
    Lock()  { xSemaphoreTake(mutex, portMAX_DELAY); }
    ~Lock() { xSemaphoreGive(mutex); }
};
}

static bool waiting()      { return retry_at && (int32_t)(retry_at - millis()) > 0; }
static void reset_backoff() { backoff_ms = 0; retry_at = 0; }

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

static void apply_retry(const char *id)
{
    JsonDocument meta;
    if (!recording_load_meta(id, meta)) return;
    if (meta["upload"]["status"] == "failed") {
        meta["upload"]["status"] = "uploading";
        meta["upload"].remove("error");
        meta["upload"].remove("checksum_resets");
        reset_backoff();
    }
    recording_save_meta(id, meta);
}

void worker_retry(const String &id)
{
    char buf[65];
    strlcpy(buf, id.c_str(), sizeof(buf));
    xQueueSend(retries, buf, 0);
    worker_kick();
}

// ============================================================
// Uploads
// ============================================================

static bool needs_upload(const RecordingInfo &r)
{
    if (r.state == "recording" || r.upload == "failed") return false;
    return r.upload != "done" || r.highlights_unsynced;
}

// Oldest recording that needs uploading; counts the pending ones.
static bool find_work(RecordingInfo &next)
{
    if (!config_backend_enabled()) {
        pending = 0;
        return false;
    }
    std::vector<RecordingInfo> list = recordings_list();
    int n = 0;
    for (auto it = list.rbegin(); it != list.rend(); ++it) {
        if (needs_upload(*it) && !n++) next = *it;
    }
    pending = n;
    return n > 0;
}

static void backoff(const String &reason, int retry_after_s = 0)
{
    backoff_ms = backoff_ms ? min<uint32_t>(backoff_ms * 2, BACKOFF_MAX_MS) : BACKOFF_MIN_MS;
    backoff_ms = max<uint32_t>(backoff_ms, min(retry_after_s, 3600) * 1000);
    retry_at = millis() + backoff_ms;
    String when = backoff_ms >= 60000 ? String(backoff_ms / 60000) + " min" : String(backoff_ms / 1000) + " s";
    set_status("Retrying upload in " + when + ": " + reason, "Retry in " + when);
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
        backoff_ms = 0;
        if (meta["upload"]["status"] == "done") Serial.printf("[worker] %s uploaded\n", info.id.c_str());
        break;
    case STEP_RETRY:
        backoff(step.error, step.retry_after_s);
        break;
    case STEP_FAILED:
        Serial.printf("[worker] upload of %s failed: %s\n", info.id.c_str(), step.error.c_str());
        meta["upload"]["status"] = "failed";
        meta["upload"]["error"] = step.error;
        break;
    }
    recording_save_meta(info.id, meta);
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

        char id[65];
        while (xQueueReceive(retries, id, 0) == pdTRUE) apply_retry(id);

        RecordingInfo next;
        if (!find_work(next)) {
            set_status("", "");
            if (millis() - idle_since > WIFI_IDLE_OFF_MS) wifi_off();  // unless held by the web server
            wait = IDLE_WAIT_MS;
            continue;
        }
        idle_since = millis();

        if (waiting()) {
            wait = retry_at - millis();
            continue;
        }
        if (!wifi_connect()) {
            backoff("no Wi-Fi");
            continue;
        }
        upload(next);
    }
}

// ============================================================
// Public API
// ============================================================

void worker_begin()
{
    retries = xQueueCreate(8, 65);
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
        reset_backoff();
        worker_kick();
    }
}

String worker_status()        { Lock l; return status; }
String worker_status_short()  { Lock l; return status_short; }
String worker_current_id()    { Lock l; return current_id; }
int worker_pending_uploads()  { return pending; }
uint32_t worker_generation()  { return generation + recordings_generation(); }

bool worker_busy()
{
    if (step_running) return true;
    if (paused) return false;
    return pending > 0 && !waiting();
}
