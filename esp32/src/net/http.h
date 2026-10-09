#pragma once

#include <Arduino.h>
#include <functional>
#include <utility>
#include <vector>

// Minimal HTTP/1.1 client for http:// and https:// URLs (HTTPS verified with
// the built-in CA bundle). Streams request bodies and yields while waiting
// for responses. Callers must connect Wi-Fi first.

// Writes exactly the announced number of bytes to `out`; returns false on error.
using BodyWriter = std::function<bool(Print &out)>;

using HttpHeaders = std::vector<std::pair<String, String>>;

struct HttpResponse {
    int    status = -1;          // -1 for connection errors and timeouts
    String body;                 // or the error description when status is -1
    long   upload_offset = -1;   // "Upload-Offset" header, if present
    int    retry_after_s = 0;    // "Retry-After" header (seconds form), if present
};

// `method` is e.g. "GET", "POST", "PATCH". For requests without a body pass
// content_length 0 and an empty writer.
HttpResponse http_request(const String &url, const char *method, const HttpHeaders &headers,
                          const String &content_type, size_t content_length,
                          const BodyWriter &write_body, uint32_t timeout_ms);
