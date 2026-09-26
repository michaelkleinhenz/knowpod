#pragma once

#include <Arduino.h>

// One recording in progress: audio file, meta.json, highlights, auto-stop.

struct SessionInfo {
    String   id;
    String   started;         // local time, "14:30" (empty if clock not set)
    float    seconds;
    int      highlights;
    float    last_highlight;  // seconds into the recording, -1 if none
    float    hours_left;      // SD space left at 16 kHz
    float    dropped_seconds;
};

bool session_start(String &error);
void session_highlight();

// Call often while recording. Returns false once the session stopped by
// itself (SD full, file size limit, low battery); `reason` says why.
bool session_poll(String &reason);

// Stops and finalizes the recording; `info` describes the result.
void session_stop(SessionInfo &info);

bool session_active();
SessionInfo session_info();
