#pragma once

#include <Arduino.h>

// Background task that processes recordings (transcribe -> label speakers ->
// summarize), uploads them to the knowpod backend, and answers questions.
// Runs on core 0 at low priority so the UI and audio capture stay responsive.
// Temporary failures are retried with backoff; permanent ones put the
// recording into the "error" state (or its upload into "failed").

void worker_begin();
void worker_kick();                     // new work may be available
void worker_set_paused(bool paused);    // while recording (Wi-Fi is off then)

void worker_retry(const String &id);    // resume failed processing and/or a failed upload
void worker_resummarize(const String &id, const String &tmpl);

// Asking a question; the answer arrives asynchronously.
enum AskState { ASK_IDLE, ASK_RUNNING, ASK_DONE };
bool worker_ask(const String &question_wav, const String &scope_id);
AskState worker_ask_state(String &question, String &answer, bool &ok);
void worker_ask_reset();

String worker_status();                 // "Transcribing <title>, chunk 2/5", "" when idle
String worker_status_short();           // for the status bar
String worker_current_id();             // recording being processed right now
int worker_pending();                   // recordings waiting for processing
int worker_pending_uploads();           // recordings waiting to be uploaded to the backend
uint32_t worker_generation();           // changes whenever the status changes
bool worker_busy();                     // has work it can do right now (don't sleep)
