#pragma once

#include <Arduino.h>
#include "store/recordings.h"

// Background task that uploads finished recordings to the knowpod backend,
// which transcribes and summarizes them. Runs on core 0 at low priority so
// the UI and audio capture stay responsive. Temporary failures are retried
// with backoff; permanent ones mark the upload "failed". While uploads wait
// and Wi-Fi can't be reached, it offers them over Bluetooth to the knowpod
// app instead (net/ble.h).

void worker_begin();
void worker_kick();                     // new work may be available
void worker_set_paused(bool paused);    // while recording (Wi-Fi is off then)

void worker_retry(const String &id);    // resume a failed upload

// The app finished uploading a recording it read over Bluetooth (net/ble.h):
// the upload is marked done as if the device had uploaded it itself.
void worker_ble_uploaded(const String &id, const String &upload_id, const String &sha256, size_t size,
                         const String &remote_status);

// The recording still needs (part of) its backend upload.
bool worker_needs_upload(const RecordingInfo &r);

String worker_status();                 // "Uploading <title> (40%)", "" when idle
String worker_status_short();           // for the status bar
String worker_current_id();             // recording being uploaded right now
int worker_pending_uploads();           // recordings waiting to be uploaded to the backend
int worker_progress();                  // percent of the recording being uploaded, -1 when none
uint32_t worker_generation();           // changes whenever the status changes
bool worker_busy();                     // has work it can do right now (don't sleep)
