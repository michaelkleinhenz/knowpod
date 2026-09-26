#include "session.h"
#include <SD_MMC.h>
#include <ArduinoJson.h>
#include "audio/audio.h"
#include "hw/clock.h"
#include "hw/power.h"
#include "net/web.h"
#include "net/wifi.h"
#include "proc/worker.h"
#include "store/config.h"
#include "store/recordings.h"

#define LOW_BATTERY_PERCENT  3      // stop before the PMIC cuts power at 5 %
#define BATTERY_CHECK_MS     10000
#define META_CHECKPOINT_MS   30000
#define MAX_HIGHLIGHTS       200

static bool active = false;
static String id;
static String started;
static JsonDocument meta;
static uint64_t free_bytes_at_start;
static uint32_t last_battery_check;
static uint32_t last_meta_checkpoint;

static void save_meta(const char *state)
{
    meta["duration_s"] = recorder_seconds();
    meta["dropped_s"] = recorder_dropped_seconds();
    meta["state"] = state;
    recording_save_meta(id, meta);
}

bool session_start(String &error)
{
    if (active) return true;

    uint64_t total = SD_MMC.totalBytes();
    free_bytes_at_start = total - SD_MMC.usedBytes();
    if (free_bytes_at_start < 10ull * 1024 * 1024) {
        error = "SD card is full.";
        return false;
    }

    id = recording_new_id();
    if (!recording_create(id)) {
        error = "Cannot create " + recording_dir(id);
        return false;
    }

    // Wi-Fi draws a lot of power and adds noise; it is not needed while recording
    worker_set_paused(true);
    web_stop();
    wifi_off(true);

    meta.clear();
    meta["id"] = id;
    if (clock_valid()) {
        meta["created"] = clock_format("%Y-%m-%dT%H:%M:%S%z");
        meta["created_unix"] = (uint32_t)time(nullptr);
    }
    meta["sample_rate"] = SAMPLE_RATE;
    meta["highlights"].to<JsonArray>();
    meta["duration_s"] = 0;
    meta["state"] = "recording";
    recording_save_meta(id, meta);

    // Audible confirmation; finishes before the mic starts so it isn't recorded
    if (config_sound_cues()) play_cue(CUE_START);

    if (!recorder_start(SD_MMC, recording_audio_path(id).c_str())) {
        error = "Cannot start recording.";
        meta["state"] = "error";
        meta["error"] = error;
        recording_save_meta(id, meta);
        worker_set_paused(false);
        return false;
    }

    started = clock_valid() ? clock_format("%H:%M") : String();
    active = true;
    last_battery_check = last_meta_checkpoint = millis();
    Serial.printf("Session %s started\n", id.c_str());
    return true;
}

void session_highlight()
{
    if (!active) return;
    JsonArray hl = meta["highlights"];
    if (hl.size() >= MAX_HIGHLIGHTS) return;
    float t = roundf(recorder_seconds() * 10) / 10;
    hl.add(t);
    save_meta("recording");
    Serial.printf("Highlight at %.1f s\n", t);
}

bool session_poll(String &reason)
{
    if (!active) return false;
    recorder_poll();

    if (recorder_failed()) {
        reason = "Recording stopped: SD card full or write error.";
        return false;
    }

    uint32_t now = millis();
    if (now - last_battery_check >= BATTERY_CHECK_MS) {
        last_battery_check = now;
        PowerStatus p = power_status();
        if (p.battery_percent >= 0 && p.battery_percent <= LOW_BATTERY_PERCENT && !p.usb_connected) {
            reason = "Recording stopped: battery low.";
            return false;
        }
    }

    // Keep duration current in meta.json in case of power loss
    if (now - last_meta_checkpoint >= META_CHECKPOINT_MS) {
        last_meta_checkpoint = now;
        save_meta("recording");
    }
    return true;
}

SessionInfo session_info()
{
    SessionInfo info;
    info.id = id;
    info.started = started;
    info.seconds = recorder_seconds();
    JsonArrayConst hl = meta["highlights"];
    info.highlights = hl.size();
    info.last_highlight = hl.size() ? hl[hl.size() - 1].as<float>() : -1;
    uint64_t written = (uint64_t)(info.seconds * BYTES_PER_SEC);
    uint64_t left = free_bytes_at_start > written ? free_bytes_at_start - written : 0;
    info.hours_left = (float)left / BYTES_PER_SEC / 3600;
    info.dropped_seconds = recorder_dropped_seconds();
    return info;
}

void session_stop(SessionInfo &info)
{
    if (!active) return;
    recorder_stop();
    if (config_sound_cues()) play_cue(CUE_STOP);
    info = session_info();
    save_meta("recorded");
    active = false;
    worker_set_paused(false);  // start transcribing
    Serial.printf("Session %s saved: %.1f s, %d highlights\n", id.c_str(), info.seconds, info.highlights);
}

bool session_active()
{
    return active;
}
