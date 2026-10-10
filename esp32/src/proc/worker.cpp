#include "worker.h"
#include <ArduinoJson.h>
#include <atomic>
#include "net/ble.h"
#include "net/web.h"
#include "net/wifi.h"
#include "upload.h"
#include "store/config.h"
#include "store/recordings.h"

#define WORKER_STACK       16384
#define IDLE_WAIT_MS       30000
#define WIFI_IDLE_OFF_MS   60000
#define BACKOFF_MIN_MS     30000
#define BACKOFF_MAX_MS     600000
#define BLE_BUSY_WAIT_MS   5000    // Wi-Fi scans slow a Bluetooth transfer down

// Requests from other tasks, handled by the worker task
struct Command {
    enum Type : uint8_t { RETRY, BLE_UPLOADED } type;
    char id[65];
    // BLE_UPLOADED
    char upload_id[65];
    char sha256[65];
    char remote_status[16];
    uint32_t size;
};

static TaskHandle_t task;
static QueueHandle_t commands;
static SemaphoreHandle_t mutex = xSemaphoreCreateMutex();

static volatile bool paused = false;
static volatile bool step_running = false;
static volatile int pending = 0;   // recordings that still need uploading
static volatile int progress = -1; // percent of the file being sent, -1 when not uploading
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
    if (id.isEmpty()) progress = -1;
    generation++;
    if (!full.isEmpty()) Serial.printf("[worker] %s\n", full.c_str());
}

static void apply_command(const Command &cmd)
{
    JsonDocument meta;
    if (!recording_load_meta(cmd.id, meta)) return;
    if (cmd.type == Command::RETRY) {
        if (meta["upload"]["status"] == "failed") {
            meta["upload"]["status"] = "uploading";
            meta["upload"].remove("error");
            meta["upload"].remove("checksum_resets");
            reset_backoff();
        }
    } else {
        JsonObject up = meta["upload"].is<JsonObject>() ? meta["upload"].as<JsonObject>()
                                                        : meta["upload"].to<JsonObject>();
        up["status"] = "done";
        up["via"] = "bluetooth";
        up["upload_id"] = cmd.upload_id;
        up["sha256"] = cmd.sha256;
        up["size"] = cmd.size;
        up["offset"] = cmd.size;
        up["remote_status"] = cmd.remote_status;
        up["highlights_synced"] = true;  // the app sent them with the upload
        up.remove("error");
        up.remove("checksum_resets");
        reset_backoff();
        Serial.printf("[worker] %s uploaded by the app over Bluetooth\n", cmd.id);
    }
    recording_save_meta(cmd.id, meta);
}

void worker_retry(const String &id)
{
    Command cmd = {Command::RETRY};
    strlcpy(cmd.id, id.c_str(), sizeof(cmd.id));
    xQueueSend(commands, &cmd, 0);
    worker_kick();
}

void worker_ble_uploaded(const String &id, const String &upload_id, const String &sha256, size_t size,
                         const String &remote_status)
{
    Command cmd = {Command::BLE_UPLOADED};
    strlcpy(cmd.id, id.c_str(), sizeof(cmd.id));
    strlcpy(cmd.upload_id, upload_id.c_str(), sizeof(cmd.upload_id));
    strlcpy(cmd.sha256, sha256.c_str(), sizeof(cmd.sha256));
    strlcpy(cmd.remote_status, remote_status.c_str(), sizeof(cmd.remote_status));
    cmd.size = size;
    // Must not get lost: the BLE task can wait for room in the queue
    xQueueSend(commands, &cmd, pdMS_TO_TICKS(5000));
    worker_kick();
}

// ============================================================
// Uploads
// ============================================================

bool worker_needs_upload(const RecordingInfo &r)
{
    if (r.state == "recording" || r.upload == "failed") return false;
    return r.upload != "done" || r.highlights_unsynced;
}

// Oldest recording that needs uploading; counts the pending ones.
// `has_file`: a recording's file still has to go (not just its highlights).
static bool find_work(RecordingInfo &next, bool &has_file)
{
    has_file = false;
    if (!config_backend_enabled()) {
        pending = 0;
        return false;
    }
    std::vector<RecordingInfo> list = recordings_list();
    int n = 0;
    for (auto it = list.rbegin(); it != list.rend(); ++it) {
        if (!worker_needs_upload(*it)) continue;
        if (!n++) next = *it;
        if (it->upload != "done") has_file = true;
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
    auto show = [&](int percent) {
        progress = percent;
        set_status("Uploading " + title + " (" + String(percent) + "%)", "Upload " + String(percent) + "%",
                   info.id);
    };
    show(info.upload_percent);

    int percent = 0;
    step_running = true;
#ifndef BOARD_HAS_PSRAM
    // Without PSRAM the heap has no room for the web server beside a TLS
    // connection; web_poll() starts it again once uploads are done or wait
    if (web_active()) {
        Serial.println("[worker] web access paused for the upload");
        web_stop();
    }
#endif
    wifi_full_power(true);
    Step step = info.upload == "done" ? upload_highlights(info.id, meta)
                                      : upload_next(info.id, meta, percent, show);
    wifi_full_power(false);
    step_running = false;

    if (paused && step.result != STEP_OK) return;  // Wi-Fi switched off for a recording

    switch (step.result) {
    case STEP_OK:
        backoff_ms = 0;
        if (info.upload != "done") show(percent);
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

        Command cmd;
        while (xQueueReceive(commands, &cmd, 0) == pdTRUE) apply_command(cmd);

        RecordingInfo next;
        bool has_file;
        bool has_work = find_work(next, has_file);
        if (!has_work) {
            set_status("", "");
            if (millis() - idle_since > WIFI_IDLE_OFF_MS) wifi_off();  // unless held by the web server
            // Nothing to upload; without Wi-Fi a paired app's "Copy" still finds the recorder
            ble_offer(wifi_connected() ? BLE_OFFER_NONE : BLE_OFFER_IDLE);
            wait = IDLE_WAIT_MS;
            continue;
        }
        idle_since = millis();

        if (waiting()) {
            wait = retry_at - millis();
            continue;
        }
        // An app is reading recordings over Bluetooth: let it finish first
        if (ble_connected() && !wifi_connected()) {
            wait = BLE_BUSY_WAIT_MS;
            continue;
        }

        if (!wifi_connect()) {
            backoff("no Wi-Fi");
            // Offer the waiting recordings to the knowpod app over Bluetooth instead
            ble_offer(has_file ? BLE_OFFER_WAITING : BLE_OFFER_IDLE);
            continue;
        }
        ble_offer(BLE_OFFER_NONE);
        upload(next);
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
        reset_backoff();
        worker_kick();
    }
}

String worker_status()        { Lock l; return status; }
String worker_status_short()  { Lock l; return status_short; }
String worker_current_id()    { Lock l; return current_id; }
int worker_pending_uploads()  { return pending; }
int worker_progress()         { return progress; }
uint32_t worker_generation()  { return generation + recordings_generation() + ble_generation(); }

bool worker_busy()
{
    if (step_running) return true;
    if (paused) return false;
    return pending > 0 && !waiting();
}
