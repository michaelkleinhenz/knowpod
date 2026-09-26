#include "openrouter.h"
#include <map>
#include <NetworkClientSecure.h>
#include "net/wifi.h"
#include "store/config.h"

#define OPENROUTER_HOST     "openrouter.ai"
#define APP_TITLE           "knowpod"
#define STT_TIMEOUT_MS      60000    // OpenRouter's STT provider timeout is 60 s
#define CHAT_TIMEOUT_MS     180000   // long transcripts and reasoning models are slow
#define MULTIPART_BOUNDARY  "----knowpodFormBoundary7MA4YWxkTrZu0gW"

// ============================================================
// HTTP helpers
// ============================================================

// Waits for response data while yielding the CPU. (Stream's readStringUntil()
// and readBytes() spin without yielding; waiting seconds for a model's answer
// that way starves the idle task and trips the task watchdog.)
static bool wait_data(NetworkClientSecure &client, uint32_t deadline)
{
    while (!client.available()) {
        if (!client.connected() || (int32_t)(millis() - deadline) > 0) return false;
        delay(10);
    }
    return true;
}

static bool read_line(NetworkClientSecure &client, uint32_t deadline, String &line)
{
    line = "";
    for (;;) {
        if (!wait_data(client, deadline)) return !line.isEmpty();
        int c = client.read();
        if (c < 0) continue;
        if (c == '\n') return true;
        line += (char)c;
    }
}

// Reads up to `len` bytes into `body`; returns false on timeout/close before that.
static bool read_bytes(NetworkClientSecure &client, uint32_t deadline, String &body, long len)
{
    uint8_t buf[512];
    while (len != 0) {
        if (!wait_data(client, deadline)) return len < 0;  // unknown length: read until close
        size_t want = len < 0 ? sizeof(buf) : min<long>(len, sizeof(buf));
        int n = client.read(buf, want);
        if (n <= 0) continue;
        body.concat((const char *)buf, n);
        if (len > 0) len -= n;
    }
    return true;
}

// Reads the rest of the response body, decoding chunked transfer encoding.
static String read_body(NetworkClientSecure &client, uint32_t deadline, bool chunked, long content_length)
{
    String body;
    if (!chunked) {
        if (content_length > 0) body.reserve(content_length);
        read_bytes(client, deadline, body, content_length);
        return body;
    }

    String line;
    while (read_line(client, deadline, line)) {
        line.trim();
        long size = strtol(line.c_str(), nullptr, 16);
        if (size <= 0) break;
        if (!read_bytes(client, deadline, body, size)) break;
        read_line(client, deadline, line);  // CRLF after chunk data
    }
    return body;
}

// Adapts NetworkClientSecure so large writes are retried until complete.
class FullWritePrint : public Print {
public:
    explicit FullWritePrint(NetworkClientSecure &c) : client(c) {}
    size_t write(uint8_t b) override { return write(&b, 1); }
    size_t write(const uint8_t *buf, size_t len) override
    {
        size_t done = 0;
        while (done < len) {
            size_t n = client.write(buf + done, len - done);
            if (n == 0) { failed = true; break; }
            done += n;
        }
        return done;
    }
    bool failed = false;

private:
    NetworkClientSecure &client;
};

// POSTs a body of `content_length` bytes to /api/v1/<path>.
// Returns the HTTP status (or -1 on a connection error) and the response body.
static int https_post(const char *path, const String &content_type, size_t content_length,
                      const BodyWriter &write_body, uint32_t timeout_ms, String &response,
                      int *retry_after_s = nullptr)
{
    const String &key = config_api_key();
    if (key.isEmpty()) {
        response = "No API key in /openrouter.txt";
        return -1;
    }
    if (!wifi_connected()) {
        response = "Wi-Fi not connected";
        return -1;
    }

    NetworkClientSecure client;
    client.useBuiltinCACertBundle();
    client.setTimeout(timeout_ms);
    if (!client.connect(OPENROUTER_HOST, 443)) {
        response = "Connection to " OPENROUTER_HOST " failed";
        return -1;
    }

    client.printf("POST /api/v1/%s HTTP/1.1\r\n", path);
    client.print("Host: " OPENROUTER_HOST "\r\n"
                 "X-Title: " APP_TITLE "\r\n"
                 "Authorization: Bearer ");
    client.print(key);
    client.print("\r\nContent-Type: ");
    client.print(content_type);
    client.printf("\r\nContent-Length: %u\r\n"
                  "Connection: close\r\n\r\n", (unsigned)content_length);

    FullWritePrint out(client);
    if (!write_body(out) || out.failed) {
        client.stop();
        response = "Upload failed";
        return -1;
    }

    // Status line, e.g. "HTTP/1.1 200 OK"; the model may take a while to answer
    uint32_t deadline = millis() + timeout_ms;
    String status_line;
    read_line(client, deadline, status_line);
    int status = status_line.length() > 9 ? status_line.substring(9, 12).toInt() : 0;
    if (status == 0) {
        client.stop();
        response = "No response (timeout)";
        return -1;
    }

    bool chunked = false;
    long body_len = -1;
    String line;
    for (;;) {
        if (!read_line(client, deadline, line)) break;
        line.trim();
        if (line.isEmpty()) break;
        line.toLowerCase();
        if (line.startsWith("transfer-encoding:") && line.indexOf("chunked") > 0)
            chunked = true;
        else if (line.startsWith("content-length:"))
            body_len = line.substring(15).toInt();
        else if (line.startsWith("retry-after:") && retry_after_s)
            *retry_after_s = line.substring(12).toInt();  // seconds form only
    }

    response = read_body(client, deadline, chunked, body_len);
    client.stop();
    return status;
}

// Builds the result for a finished request; extracts OpenRouter's
// {"error": {"message": ...}} or falls back to the raw body.
static ApiResult make_result(int status, const String &body, JsonDocument &doc)
{
    ApiResult r;
    r.status = status;
    DeserializationError err = status > 0 ? deserializeJson(doc, body) : DeserializationError::EmptyInput;
    if (status == 200 && !err) {
        r.ok = true;
        return r;
    }
    if (status < 0) {
        r.error = body;
    } else if (status == 200) {
        r.error = "Invalid JSON response";
    } else {
        // {"error": {"message", "metadata": {"provider_name", "raw"}}}
        const char *msg = err ? nullptr : (const char *)doc["error"]["message"];
        r.error = "HTTP " + String(status) + ": " + (msg ? String(msg) : body.substring(0, 200));
        JsonVariantConst meta = doc["error"]["metadata"];
        const char *provider = meta["provider_name"];
        if (provider) r.error += " [" + String(provider) + "]";
        JsonVariantConst raw = meta["raw"];
        if (!raw.isNull()) {
            String detail = raw.is<const char *>() ? String(raw.as<const char *>()) : raw.as<String>();
            r.error += " " + detail.substring(0, 200);
        }
        Serial.printf("OpenRouter error: %s\n", body.substring(0, 500).c_str());
    }
    return r;
}

// ============================================================
// Speech-to-text
// ============================================================

static void add_field(String &form, const char *name, const String &value)
{
    form += "--" MULTIPART_BOUNDARY "\r\nContent-Disposition: form-data; name=\"";
    form += name;
    form += "\"\r\n\r\n" + value + "\r\n";
}

static ApiResult transcribe_with(const String &model, size_t wav_bytes, const BodyWriter &write_wav,
                                 bool timestamps, JsonDocument &out)
{
    String head;
    add_field(head, "model", model);
    String language = config_stt_language();
    if (!language.isEmpty()) add_field(head, "language", language);
    if (timestamps) add_field(head, "response_format", "verbose_json");
    head += "--" MULTIPART_BOUNDARY "\r\n"
            "Content-Disposition: form-data; name=\"file\"; filename=\"audio.wav\"\r\n"
            "Content-Type: audio/wav\r\n\r\n";
    const char *tail = "\r\n--" MULTIPART_BOUNDARY "--\r\n";

    String body;
    int retry_after = 0;
    int status = https_post("audio/transcriptions",
                            "multipart/form-data; boundary=" MULTIPART_BOUNDARY,
                            head.length() + wav_bytes + strlen(tail),
                            [&](Print &o) {
                                o.print(head);
                                bool ok = write_wav(o);
                                o.print(tail);
                                return ok;
                            },
                            STT_TIMEOUT_MS, body, &retry_after);
    ApiResult r = make_result(status, body, out);
    r.retry_after_s = retry_after;
    return r;
}

#define RATE_LIMIT_SKIP_MS  (10 * 60 * 1000)

// Models that returned 429 are skipped for a while, so every chunk doesn't
// upload megabytes of audio to a provider that will refuse it anyway.
static std::map<String, uint32_t> rate_limited_until;
static SemaphoreHandle_t rate_limit_mutex = xSemaphoreCreateMutex();

static bool is_rate_limited(const String &model)
{
    xSemaphoreTake(rate_limit_mutex, portMAX_DELAY);
    auto it = rate_limited_until.find(model);
    bool limited = it != rate_limited_until.end() && (int32_t)(it->second - millis()) > 0;
    xSemaphoreGive(rate_limit_mutex);
    return limited;
}

static void mark_rate_limited(const String &model)
{
    xSemaphoreTake(rate_limit_mutex, portMAX_DELAY);
    rate_limited_until[model] = millis() + RATE_LIMIT_SKIP_MS;
    xSemaphoreGive(rate_limit_mutex);
}

ApiResult openrouter_transcribe(size_t wav_bytes, const BodyWriter &write_wav,
                                bool timestamps, JsonDocument &out)
{
    std::vector<String> models = config_stt_models();
    std::vector<String> order;
    for (const String &m : models)
        if (!is_rate_limited(m)) order.push_back(m);
    if (order.empty()) order = models;  // all limited: try anyway

    ApiResult r;
    for (const String &model : order) {
        out.clear();
        r = transcribe_with(model, wav_bytes, write_wav, timestamps, out);
        if (r.status == 429) mark_rate_limited(model);
        if (r.ok || !r.retryable() || r.status < 0) break;  // no point switching on network errors
        Serial.printf("Transcription with %s failed (%s); trying next model\n", model.c_str(), r.error.c_str());
    }
    return r;
}

// ============================================================
// Chat completions
// ============================================================

ApiResult openrouter_chat(JsonArrayConst messages, String &reply, String *model_used)
{
    JsonDocument req;
    config_llm(req);
    req["messages"] = messages;
    req["stream"] = false;

    String body;
    int retry_after = 0;
    int status = https_post("chat/completions", "application/json", measureJson(req),
                            [&](Print &o) {
                                serializeJson(req, o);
                                return true;
                            },
                            CHAT_TIMEOUT_MS, body, &retry_after);
    req.clear();

    JsonDocument doc;
    ApiResult r = make_result(status, body, doc);
    r.retry_after_s = retry_after;
    if (!r.ok) return r;

    // Errors after the request started are reported inside the choice
    JsonObjectConst choice = doc["choices"][0];
    const char *content = choice["message"]["content"];
    if (choice["error"] || !content) {
        r.ok = false;
        r.status = 502;  // treat as a (retryable) upstream failure
        r.error = choice["error"]["message"] | "Empty response from model";
        return r;
    }
    reply = content;
    if (model_used) *model_used = doc["model"] | "";
    return r;
}

ApiResult openrouter_chat(const String &system, const String &user, String &reply, String *model_used)
{
    JsonDocument messages;
    JsonObject sys = messages.add<JsonObject>();
    sys["role"] = "system";
    sys["content"] = system;
    JsonObject usr = messages.add<JsonObject>();
    usr["role"] = "user";
    usr["content"] = user;
    return openrouter_chat(messages.as<JsonArrayConst>(), reply, model_used);
}
