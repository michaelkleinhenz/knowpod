#pragma once

#include <Arduino.h>
#include <FS.h>
#include <ArduinoJson.h>
#include <vector>

// Each recording lives in its own folder on the SD card:
//
//   /recs/20260924-143012/
//     audio.wav        16 kHz mono PCM
//     meta.json        {"id", "created", "created_unix", "duration_s", "sample_rate",
//                       "highlights": [seconds, ...], "dropped_s", "state", "title",
//                       "tags", "template", "model", "error", "error_state",
//                       "upload": {"status", "sha256", "size", "upload_id", "offset", ...}}
//     transcript.json  {"segments": [{"start", "end", "text"}], "speakers": [...]}
//     transcript.md    readable transcript with timestamps (and speakers)
//     summary.json     {"title", "tags", "summary", "action_items", "highlights"}
//     summary.md       readable notes
//     qa.md            questions asked about this recording
//
// "state": recording -> recorded -> transcribing -> transcribed -> summarized,
// or error (with "error" and "error_state" to resume from).
//
// All functions are thread-safe.

#define RECS_DIR "/recs"

struct RecordingInfo {
    String   id;
    String   title;          // empty until summarized
    uint32_t created_unix;   // 0 if the clock was not set
    float    duration_s;
    int      highlights;
    String   state;
    String   error;
    String   upload;         // backend upload: "" (not started), "uploading", "done", "failed"
    String   upload_error;
    int      upload_percent;
};

bool recordings_begin(fs::FS &fs);   // creates /recs and recovers interrupted recordings
fs::FS &recordings_fs();

// Newest first; cached until something changes.
std::vector<RecordingInfo> recordings_list();
bool recording_info(const String &id, RecordingInfo &info);

// Incremented whenever a recording is added, changed or deleted.
uint32_t recordings_generation();
void recordings_touch();

// New unique id from the current time ("20260924-143012"), or "rec-0001"
// style if the clock is not set.
String recording_new_id();
bool recording_valid_id(const String &id);   // safe to use in paths

String recording_dir(const String &id);
String recording_path(const String &id, const char *file);
String recording_audio_path(const String &id);

bool recording_create(const String &id);   // folder only
bool recording_delete(const String &id);
bool recording_load_meta(const String &id, JsonDocument &meta);
bool recording_save_meta(const String &id, const JsonDocument &meta);

// Display helpers
String recording_display_title(const RecordingInfo &info);   // title or date
String recording_display_date(uint32_t unix_time);           // "Wed 24.09.2026 14:30"
String recording_state_label(const String &state);           // "Transcribing", ...
String format_duration(float seconds);                       // "1:02:03"

// Whole-file helpers (writes go through a temp file)
bool read_file(const String &path, String &out);
bool write_file(const String &path, const String &content);
bool append_file(const String &path, const String &content);
