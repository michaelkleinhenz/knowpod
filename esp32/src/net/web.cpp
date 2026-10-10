#include "web.h"
#include <WebServer.h>
#include <ESPmDNS.h>
#include "net/wifi.h"
#include "session.h"
#include "proc/worker.h"
#include "store/config.h"
#include "store/recordings.h"

#define WEB_USER   "knowpod"
#define WEB_STACK  16384
#define AUTOSTART_RETRY_MS 60000

static WebServer *server = nullptr;
static TaskHandle_t task = nullptr;
static volatile bool running = false;
static volatile bool stop_requested = false;
static volatile bool suspended = false;   // stopped for a recording; blocks background starts
static String password;

// ============================================================
// HTML helpers
// ============================================================

static String esc(const String &s)
{
    String out;
    out.reserve(s.length() + 16);
    for (char c : s) {
        switch (c) {
        case '&': out += "&amp;"; break;
        case '<': out += "&lt;"; break;
        case '>': out += "&gt;"; break;
        case '"': out += "&quot;"; break;
        default:  out += c;
        }
    }
    return out;
}

static String page_start(const String &title)
{
    return "<!doctype html><html><head><meta charset=utf-8>"
           "<meta name=viewport content='width=device-width,initial-scale=1'>"
           "<title>" + esc(title) + " - knowpod</title><style>"
           "body{font-family:system-ui,sans-serif;max-width:900px;margin:0 auto;padding:1em;line-height:1.5}"
           "table{border-collapse:collapse;width:100%}td,th{padding:.4em;border-bottom:1px solid #ddd;text-align:left}"
           "pre{white-space:pre-wrap;background:#f5f5f5;padding:1em;border-radius:6px}"
           "textarea{width:100%;height:60vh;font-family:monospace}nav a{margin-right:1em}"
           ".muted{color:#777}"
           "</style></head><body><nav><a href='/'>Recordings</a>"
           "<a href='/wifi'>Wi-Fi</a><a href='/edit'>Settings</a></nav>"
           "<h1>" + esc(title) + "</h1>";
}

static const char PAGE_END[] = "</body></html>";

static bool auth()
{
    if (server->header("Authorization") == "Bearer " + password) return true;  // scripts
    if (server->authenticate(WEB_USER, password.c_str())) return true;
    server->requestAuthentication(BASIC_AUTH, "knowpod");
    return false;
}

// ============================================================
// Handlers
// ============================================================

static void handle_index()
{
    if (!auth()) return;
    String html = page_start("Recordings");
    String status = worker_status();
    if (!status.isEmpty()) html += "<p class=muted>" + esc(status) + "</p>";
    html += "<p class=muted>Recordings are uploaded to the knowpod backend, which transcribes and "
            "summarizes them.</p>";

    html += "<table><tr><th>Date</th><th>Title</th><th>Length</th><th>Upload</th></tr>";
    for (const RecordingInfo &r : recordings_list()) {
        html += "<tr><td>" + esc(recording_display_date(r.created_unix)) + "</td><td><a href='/rec?id=" +
                r.id + "'>" + esc(recording_display_title(r)) + "</a></td><td>" +
                format_duration(r.duration_s) + "</td><td>" + esc(recording_state_label(r)) + "</td></tr>";
    }
    html += "</table>";
    html += PAGE_END;
    server->send(200, "text/html; charset=utf-8", html);
}

static void handle_recording()
{
    if (!auth()) return;
    String id = server->arg("id");
    RecordingInfo info;
    if (!recording_valid_id(id) || !recording_info(id, info)) {
        server->send(404, "text/plain", "Recording not found");
        return;
    }

    String html = page_start(recording_display_title(info));
    html += "<p class=muted>" + esc(recording_display_date(info.created_unix)) + " &middot; " +
            format_duration(info.duration_s) + " &middot; " + esc(recording_state_label(info));
    if (!info.error.isEmpty()) html += " &middot; " + esc(info.error);
    if (info.upload == "failed") html += ": " + esc(info.upload_error);
    html += "</p><p>Download: ";
    for (const char *name : {"audio.mp3", "audio.wav", "meta.json"}) {
        if (recordings_fs().exists(recording_path(id, name)))
            html += "<a href='/file?id=" + id + "&name=" + name + "'>" + name + "</a> ";
    }
    String audio = recording_audio_path(id);
    audio = audio.substring(audio.lastIndexOf('/') + 1);
    html += "</p><audio controls preload=none src='/file?id=" + id + "&name=" + audio + "'></audio>";
    html += PAGE_END;
    server->send(200, "text/html; charset=utf-8", html);
}

static void handle_file()
{
    if (!auth()) return;
    String id = server->arg("id");
    String name = server->arg("name");
    static const char *const allowed[] = {"audio.mp3", "audio.wav", "meta.json"};
    bool ok = recording_valid_id(id);
    bool known = false;
    for (const char *a : allowed) known |= name == a;
    File f;
    if (ok && known) f = recordings_fs().open(recording_path(id, name.c_str()), FILE_READ);
    if (!f) {
        server->send(404, "text/plain", "File not found");
        return;
    }

    String type = name.endsWith(".wav") ? "audio/wav" : name.endsWith(".mp3") ? "audio/mpeg" : "application/json";
    if (name.startsWith("audio."))
        server->sendHeader("Content-Disposition", "attachment; filename=\"" + id + name.substring(5) + "\"");
    server->streamFile(f, type);
    f.close();
}

static void handle_edit()
{
    if (!auth()) return;
    String html = page_start("Settings (config.json)");
    html += "<p class=muted>Changes to Wi-Fi and time zone take effect after a restart.</p>"
            "<form method=post action='/save'><textarea name=content>" + esc(config_json()) +
            "</textarea><p><button>Save</button></p></form>";
    html += PAGE_END;
    server->send(200, "text/html; charset=utf-8", html);
}

static void handle_save()
{
    if (!auth()) return;
    String content = server->arg("content");
    content.replace("\r\n", "\n");
    String error;
    if (!config_replace(content, error)) {
        server->send(400, "text/plain", "Not saved. " + error);
        return;
    }
    server->sendHeader("Location", "/edit");
    server->send(303);
}

// Wi-Fi networks in priority order: add, remove, reorder. Passwords are
// never sent back to the browser.
static void handle_wifi()
{
    if (!auth()) return;
    std::vector<WifiNetwork> nets = config_wifi();

    if (server->method() == HTTP_POST) {
        String action = server->arg("action");
        int i = server->arg("index").toInt();
        bool valid = i >= 0 && i < (int)nets.size();
        if (action == "add") {
            WifiNetwork n;
            n.ssid = server->arg("ssid");
            n.ssid.trim();
            n.password = server->arg("password");
            n.hidden = server->hasArg("hidden");
            if (!n.ssid.isEmpty()) nets.push_back(n);
        } else if (action == "delete" && valid) {
            nets.erase(nets.begin() + i);
        } else if (action == "up" && valid && i > 0) {
            std::swap(nets[i], nets[i - 1]);
        } else if (action == "down" && valid && i + 1 < (int)nets.size()) {
            std::swap(nets[i], nets[i + 1]);
        }
        config_set_wifi(nets);
        server->sendHeader("Location", "/wifi");
        server->send(303);
        return;
    }

    String html = page_start("Wi-Fi networks");
    html += "<p>The device tries these networks from top to bottom and uses the first one that is "
            "in range. Currently connected to <b>" + esc(wifi_ssid()) + "</b>.</p><table>"
            "<tr><th>#</th><th>Network</th><th></th></tr>";
    for (size_t i = 0; i < nets.size(); i++) {
        String btn = "<form method=post style='display:inline'><input type=hidden name=index value=" +
                     String(i) + "><button name=action value=";
        html += "<tr><td>" + String(i + 1) + "</td><td>" + esc(nets[i].ssid) +
                (nets[i].hidden ? " <span class=muted>(hidden)</span>" : "") +
                (nets[i].password.isEmpty() ? " <span class=muted>(open)</span>" : "") + "</td><td>";
        if (i > 0) html += btn + "up>&uarr;</button></form> ";
        if (i + 1 < nets.size()) html += btn + "down>&darr;</button></form> ";
        html += btn + "delete onclick=\"return confirm('Remove this network?')\">Remove</button></form></td></tr>";
    }
    if (nets.empty()) html += "<tr><td colspan=3>No networks configured.</td></tr>";
    html += "</table><h2>Add network</h2><form method=post><input type=hidden name=action value=add>"
            "<p><input name=ssid placeholder='Network name (SSID)' required size=30></p>"
            "<p><input name=password type=password placeholder='Password (empty for open networks)' size=30></p>"
            "<p><label><input type=checkbox name=hidden> Hidden network (not broadcast)</label></p>"
            "<p><button>Add</button></p></form>"
            "<p class=muted>New networks are added at the end; use the arrows to change the order. "
            "Changes apply the next time the device connects.</p>";
    html += PAGE_END;
    server->send(200, "text/html; charset=utf-8", html);
}

// ============================================================
// Task
// ============================================================

static void web_task(void *)
{
    while (!stop_requested) {
        server->handleClient();
        delay(2);
    }
    server->stop();
    MDNS.end();
    delete server;
    server = nullptr;
    running = false;
    task = nullptr;
    vTaskDelete(nullptr);
}

bool web_start(String &error)
{
    if (running) return true;
    if (!wifi_connect()) {
        error = "No Wi-Fi connection.";
        return false;
    }
    if (suspended || session_active()) {  // a recording started meanwhile
        wifi_off(true);
        error = "Recording in progress.";
        return false;
    }
    wifi_hold(true);
    password = config_web_password();

    server = new WebServer(80);
    server->on("/", handle_index);
    server->on("/rec", handle_recording);
    server->on("/file", handle_file);
    server->on("/edit", handle_edit);
    server->on("/wifi", handle_wifi);
    server->on("/save", HTTP_POST, handle_save);
    const char *headers[] = {"Authorization"};
    server->collectHeaders(headers, 1);
    server->begin();
    if (MDNS.begin("knowpod")) MDNS.addService("http", "tcp", 80);

    stop_requested = false;
    running = true;
    xTaskCreatePinnedToCore(web_task, "web", WEB_STACK, nullptr, 1, &task, 0);
    Serial.printf("Web access on %s (user " WEB_USER ")\n", web_url().c_str());
    return true;
}

void web_stop()
{
    suspended = true;
    if (!running) return;
    stop_requested = true;
    while (running) delay(10);
    wifi_hold(false);
    Serial.println("Web access stopped");
}

bool web_active()
{
    return running;
}

String web_url()
{
    return "http://" + wifi_ip() + "/";
}

// ============================================================
// Automatic start
// ============================================================

static volatile bool starting = false;
static uint32_t last_attempt = 0;

static void starter_task(void *)
{
    String error;
    if (!web_start(error)) Serial.printf("Web access not started: %s\n", error.c_str());
    starting = false;
    vTaskDelete(nullptr);
}

void web_poll()
{
    if (session_active()) return;
#ifndef BOARD_HAS_PSRAM
    if (worker_busy()) return;  // uploading: TLS needs the memory (see worker.cpp)
#endif
    suspended = false;
    if (running || starting || !config_web_enabled() || config_wifi().empty()) return;
    if (last_attempt && millis() - last_attempt < AUTOSTART_RETRY_MS) return;
    last_attempt = millis();
    starting = true;
    xTaskCreatePinnedToCore(starter_task, "web_start", 6144, nullptr, 1, nullptr, 0);
}
