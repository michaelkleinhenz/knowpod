#pragma once

#include <Arduino.h>
#include <ArduinoJson.h>
#include <functional>

// OpenRouter API client. Models and request options come from config.json;
// the API key from /openrouter.txt. Callers must connect Wi-Fi first.
// Thread-safe (each call uses its own TLS connection).

// Writes exactly the announced number of bytes to `out`; returns false on error.
using BodyWriter = std::function<bool(Print &out)>;

struct ApiResult {
    bool   ok = false;
    int    status = -1;     // HTTP status, -1 for connection/timeout errors
    String error;
    int    retry_after_s = 0;  // from the Retry-After header of 429/503 responses

    // Network problems, rate limits and server errors are worth retrying;
    // other 4xx errors (bad key, no credits, invalid request) are not.
    bool retryable() const { return !ok && (status < 0 || status == 408 || status == 429 || status >= 500); }
};

// Speech-to-text via /audio/transcriptions. `write_wav` must produce a WAV
// file of `wav_bytes` bytes (it may be called again for fallback models).
// `out` receives the response: {"text": ...} and, with `timestamps`,
// "segments": [{"start", "end", "text"}] if the model provides them.
// Tries config.json's stt "model" and then its "models" fallbacks while the
// errors are retryable (e.g. a provider's rate limit).
ApiResult openrouter_transcribe(size_t wav_bytes, const BodyWriter &write_wav,
                                bool timestamps, JsonDocument &out);

// Chat completion via /chat/completions. The request is config.json's "llm"
// object plus `messages` ([{"role": "system"|"user"|"assistant", "content": "..."}]).
// `model_used` receives the model that actually answered (fallbacks, auto routing).
ApiResult openrouter_chat(JsonArrayConst messages, String &reply, String *model_used = nullptr);

// Convenience wrapper for a system prompt plus one user message.
ApiResult openrouter_chat(const String &system, const String &user, String &reply,
                          String *model_used = nullptr);
