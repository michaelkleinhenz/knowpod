#include "http.h"
#include <NetworkClient.h>
#include <NetworkClientSecure.h>
#include <memory>
#include "net/wifi.h"

// ============================================================
// Reading
// ============================================================

// Waits for response data while yielding the CPU. (Stream's readStringUntil()
// and readBytes() spin without yielding; waiting seconds for a server's answer
// that way starves the idle task and trips the task watchdog.)
static bool wait_data(Client &client, uint32_t deadline)
{
    while (!client.available()) {
        if (!client.connected() || (int32_t)(millis() - deadline) > 0) return false;
        delay(10);
    }
    return true;
}

static bool read_line(Client &client, uint32_t deadline, String &line)
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
static bool read_bytes(Client &client, uint32_t deadline, String &body, long len)
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
static String read_body(Client &client, uint32_t deadline, bool chunked, long content_length)
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

// ============================================================
// Writing
// ============================================================

// Adapts a client so large writes are retried until complete.
class FullWritePrint : public Print {
public:
    explicit FullWritePrint(Client &c) : client(c) {}
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
    Client &client;
};

// ============================================================
// Request
// ============================================================

struct Url {
    bool   https;
    String host;
    uint16_t port;
    String path;
};

static bool parse_url(const String &url, Url &out)
{
    int scheme_end = url.indexOf("://");
    if (scheme_end < 0) return false;
    String scheme = url.substring(0, scheme_end);
    scheme.toLowerCase();
    if (scheme != "http" && scheme != "https") return false;
    out.https = scheme == "https";

    int host_start = scheme_end + 3;
    int path_start = url.indexOf('/', host_start);
    String authority = url.substring(host_start, path_start < 0 ? url.length() : path_start);
    out.path = path_start < 0 ? String("/") : url.substring(path_start);

    int colon = authority.lastIndexOf(':');
    if (colon > 0) {
        out.host = authority.substring(0, colon);
        out.port = authority.substring(colon + 1).toInt();
    } else {
        out.host = authority;
        out.port = out.https ? 443 : 80;
    }
    return !out.host.isEmpty() && out.port > 0;
}

static HttpResponse fail(const String &error)
{
    HttpResponse r;
    r.body = error;
    return r;
}

HttpResponse http_request(const String &url, const char *method, const HttpHeaders &headers,
                          const String &content_type, size_t content_length,
                          const BodyWriter &write_body, uint32_t timeout_ms)
{
    Url u;
    if (!parse_url(url, u)) return fail("Invalid URL: " + url);
    if (!wifi_connected()) return fail("Wi-Fi not connected");

    std::unique_ptr<NetworkClient> client;
    if (u.https) {
        auto secure = new NetworkClientSecure();
        secure->useBuiltinCACertBundle();
        client.reset(secure);
    } else {
        client.reset(new NetworkClient());
    }
    client->setTimeout(timeout_ms);
    if (!client->connect(u.host.c_str(), u.port)) return fail("Connection to " + u.host + " failed");

    String head = String(method) + " " + u.path + " HTTP/1.1\r\nHost: " + u.host;
    if (u.port != (u.https ? 443 : 80)) head += ":" + String(u.port);
    head += "\r\nConnection: close\r\n";
    for (const auto &h : headers) head += h.first + ": " + h.second + "\r\n";
    if (content_length > 0 || write_body) {
        if (!content_type.isEmpty()) head += "Content-Type: " + content_type + "\r\n";
        head += "Content-Length: " + String((unsigned long)content_length) + "\r\n";
    }
    head += "\r\n";

    FullWritePrint out(*client);
    out.print(head);
    if (write_body && !write_body(out)) out.failed = true;
    if (out.failed) {
        client->stop();
        return fail("Upload failed");
    }

    // Status line, e.g. "HTTP/1.1 200 OK"; the server may take a while to answer
    uint32_t deadline = millis() + timeout_ms;
    HttpResponse r;
    String line;
    read_line(*client, deadline, line);
    r.status = line.length() > 9 ? line.substring(9, 12).toInt() : 0;
    if (r.status == 0) {
        client->stop();
        return fail("No response (timeout)");
    }

    bool chunked = false;
    long body_len = -1;
    for (;;) {
        if (!read_line(*client, deadline, line)) break;
        line.trim();
        if (line.isEmpty()) break;
        line.toLowerCase();
        if (line.startsWith("transfer-encoding:") && line.indexOf("chunked") > 0)
            chunked = true;
        else if (line.startsWith("content-length:"))
            body_len = line.substring(15).toInt();
        else if (line.startsWith("retry-after:"))
            r.retry_after_s = line.substring(12).toInt();
        else if (line.startsWith("upload-offset:"))
            r.upload_offset = atol(line.substring(14).c_str());
    }

    // No body for HEAD-like responses
    if (r.status != 204 && r.status != 304) r.body = read_body(*client, deadline, chunked, body_len);
    client->stop();
    return r;
}
