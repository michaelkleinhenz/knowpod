#pragma once

#include <Arduino.h>
#include <ArduinoJson.h>
#include <FS.h>
#include <functional>

// Upload steps for one recording. Each step does one unit of network work,
// updates `meta` (the caller saves it) and can be resumed after a reboot,
// since all progress is kept in meta.json.

enum StepResult {
    STEP_OK,       // progress made
    STEP_RETRY,    // temporary problem (network, rate limit); try again later
    STEP_FAILED,   // permanent problem; needs the user's attention
};

struct Step {
    StepResult result;
    String     error;
    int        retry_after_s = 0;   // server-requested wait for STEP_RETRY
};

// Called while a chunk is sent with the share of the file sent so far (0-100).
using UploadProgress = std::function<void(int percent)>;

// Uploads the recording to the knowpod backend, one step (checksum, create,
// or one chunk) per call. Sets meta["upload"]["status"] to "done" at the end.
Step upload_next(const String &id, JsonDocument &meta, int &percent,
                 const UploadProgress &progress = nullptr);

// SHA-256 of a whole file as lowercase hex (the checksum uploads are created with).
bool sha256_file(File &f, String &hex);

// Sends the highlights of an already uploaded recording (uploaded before
// highlights were supported). Sets meta["upload"]["highlights_synced"].
Step upload_highlights(const String &id, JsonDocument &meta);
