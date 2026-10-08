#pragma once

#include <Arduino.h>

// Background task that uploads finished recordings to the knowpod backend,
// which transcribes and summarizes them. Runs on core 0 at low priority so
// the UI and audio capture stay responsive. Temporary failures are retried
// with backoff; permanent ones mark the upload "failed".

void worker_begin();
void worker_kick();                     // new work may be available
void worker_set_paused(bool paused);    // while recording (Wi-Fi is off then)

void worker_retry(const String &id);    // resume a failed upload

String worker_status();                 // "Uploading <title> (40%)", "" when idle
String worker_status_short();           // for the status bar
String worker_current_id();             // recording being uploaded right now
int worker_pending_uploads();           // recordings waiting to be uploaded to the backend
uint32_t worker_generation();           // changes whenever the status changes
bool worker_busy();                     // has work it can do right now (don't sleep)
