#pragma once

#include <Arduino.h>
#include <ArduinoJson.h>

// Processing steps for one recording. Each step does one unit of network
// work, updates `meta` (the caller saves it) and can be resumed after a
// reboot, since all progress is kept in meta.json and the recording folder.

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

// Transcribes the next chunk (~5 min, cut at a quiet moment). Sets state
// "transcribed" after the last chunk. `progress` describes the chunk.
Step transcribe_next_chunk(const String &id, JsonDocument &meta, String &progress);

// Optional: asks the text model to label speakers per paragraph.
Step label_speakers(const String &id, JsonDocument &meta);

// Writes summary.json/.md using meta["template"] (or the default template).
// Sets state "summarized".
Step summarize(const String &id, JsonDocument &meta);

// Transcribes a spoken question and answers it from one recording
// (`scope_id`) or from all recordings (empty scope).
Step answer_question(const String &question_wav, const String &scope_id,
                     String &question, String &answer);

// Rebuilds transcript.md from transcript.json (paragraphs, timestamps,
// highlights, speakers).
bool write_transcript_md(const String &id, const JsonDocument &meta);

// Parses a JSON object from a model reply that may be wrapped in text or
// Markdown code fences.
bool parse_json_reply(const String &reply, JsonDocument &out);
