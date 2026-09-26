#include "web.h"
#include <WebServer.h>
#include <ESPmDNS.h>
#include "net/mcp.h"
#include "net/wifi.h"
#include "session.h"
#include "proc/worker.h"
#include "store/config.h"
#include "store/recordings.h"
#include "store/search.h"
#include "store/templates.h"

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
           ".muted{color:#777}mark{background:#ffe066}"
           "</style></head><body><nav><a href='/'>Recordings</a><a href='/templates'>Templates</a>"
           "<a href='/wifi'>Wi-Fi</a><a href='/edit?kind=glossary'>Glossary</a><a href='/edit?kind=config'>Settings</a></nav>"
           "<h1>" + esc(title) + "</h1>";
}

static const char PAGE_END[] = "</body></html>";

static bool auth()
{
    if (server->header("Authorization") == "Bearer " + password) return true;  // MCP clients
    if (server->authenticate(WEB_USER, password.c_str())) return true;
    server->requestAuthentication(BASIC_AUTH, "knowpod");
    return false;
}

static String file_content(const String &id, const char *name)
{
    String s;
    read_file(recording_path(id, name), s);
    return s;
}

// ============================================================
// Handlers
// ============================================================

static void handle_index()
{
    if (!auth()) return;
    String html = page_start("Recordings");
    html += "<form action='/search'><input name=q placeholder='Search transcripts and notes' size=40> "
            "<button>Search</button></form>";
    String status = worker_status();
    if (!status.isEmpty()) html += "<p class=muted>" + esc(status) + "</p>";
    html += "<p class=muted>MCP server: <code>" + web_mcp_url() + "</code> with header "
            "<code>Authorization: Bearer &lt;password&gt;</code></p>";

    html += "<table><tr><th>Date</th><th>Title</th><th>Length</th><th>State</th></tr>";
    for (const RecordingInfo &r : recordings_list()) {
        html += "<tr><td>" + esc(recording_display_date(r.created_unix)) + "</td><td><a href='/rec?id=" +
                r.id + "'>" + esc(recording_display_title(r)) + "</a></td><td>" +
                format_duration(r.duration_s) + "</td><td>" + recording_state_label(r.state) + "</td></tr>";
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
            format_duration(info.duration_s) + " &middot; " + recording_state_label(info.state);
    if (!info.error.isEmpty()) html += " &middot; " + esc(info.error);
    html += "</p><p>Download: ";
    for (const char *name : {"audio.wav", "summary.md", "transcript.md", "transcript.json", "meta.json", "qa.md"}) {
        if (recordings_fs().exists(recording_path(id, name)))
            html += "<a href='/file?id=" + id + "&name=" + name + "'>" + name + "</a> ";
    }
    html += "</p><audio controls preload=none src='/file?id=" + id + "&name=audio.wav'></audio>";

    for (const char *name : {"summary.md", "qa.md", "transcript.md"}) {
        String content = file_content(id, name);
        if (!content.isEmpty()) html += "<h2>" + String(name) + "</h2><pre>" + esc(content) + "</pre>";
    }
    html += PAGE_END;
    server->send(200, "text/html; charset=utf-8", html);
}

static void handle_file()
{
    if (!auth()) return;
    String id = server->arg("id");
    String name = server->arg("name");
    static const char *const allowed[] = {"audio.wav", "summary.md", "summary.json", "transcript.md",
                                          "transcript.json", "meta.json", "qa.md"};
    bool ok = recording_valid_id(id);
    bool known = false;
    for (const char *a : allowed) known |= name == a;
    File f;
    if (ok && known) f = recordings_fs().open(recording_path(id, name.c_str()), FILE_READ);
    if (!f) {
        server->send(404, "text/plain", "File not found");
        return;
    }

    String type = name.endsWith(".wav") ? "audio/wav"
                : name.endsWith(".json") ? "application/json"
                : "text/markdown; charset=utf-8";
    if (name.endsWith(".wav"))
        server->sendHeader("Content-Disposition", "attachment; filename=\"" + id + ".wav\"");
    server->streamFile(f, type);
    f.close();
}

static void handle_search()
{
    if (!auth()) return;
    String q = server->arg("q");
    q.trim();
    String html = page_start("Search");
    html += "<form action='/search'><input name=q value=\"" + esc(q) + "\" size=40> <button>Search</button></form>";

    if (!q.isEmpty()) {
        std::vector<SearchHit> hits = search_recordings(q);
        for (const SearchHit &h : hits) {
            html += "<p><a href='/rec?id=" + h.recording.id + "'>" + esc(recording_display_title(h.recording)) +
                    "</a> <span class=muted>" + h.file + "</span><br>..." + esc(h.before) + "<mark>" +
                    esc(h.match) + "</mark>" + esc(h.after) + "...</p>";
        }
        if (hits.empty()) html += "<p>No matches.</p>";
    }
    html += PAGE_END;
    server->send(200, "text/html; charset=utf-8", html);
}

static void handle_templates()
{
    if (!auth()) return;
    String html = page_start("Templates");
    html += "<p>The default template for new recordings is <b>" + esc(config_default_template()) +
            "</b>.</p><ul>";
    for (const String &t : templates_list())
        html += "<li><a href='/edit?kind=template&name=" + t + "'>" + esc(t) + "</a></li>";
    html += "</ul><form action='/edit'><input type=hidden name=kind value=template>"
            "<input name=name placeholder='new-template-name' pattern='[A-Za-z0-9_-]+'> "
            "<button>Create</button></form>";
    html += PAGE_END;
    server->send(200, "text/html; charset=utf-8", html);
}

static void handle_edit()
{
    if (!auth()) return;
    String kind = server->arg("kind");
    String name = server->arg("name");
    String title, content, help;

    if (kind == "config") {
        title = "Settings (config.json)";
        content = config_json();
        help = "Changes to Wi-Fi and time zone take effect after a restart.";
    } else if (kind == "glossary") {
        title = "Glossary";
        content = glossary_load();
        help = "Names and terms (one per line) that summaries should spell correctly.";
    } else if (kind == "template" && template_valid_name(name)) {
        title = "Template: " + name;
        content = template_load(name);
        help = "Instructions for the summary model. The response format (title, tags, "
               "summary, action items, highlights) is added automatically.";
    } else {
        server->send(400, "text/plain", "Invalid request");
        return;
    }

    String html = page_start(title);
    html += "<p class=muted>" + esc(help) + "</p><form method=post action='/save'>"
            "<input type=hidden name=kind value='" + esc(kind) + "'>"
            "<input type=hidden name=name value='" + esc(name) + "'>"
            "<textarea name=content>" + esc(content) + "</textarea><p><button>Save</button></p></form>";
    html += PAGE_END;
    server->send(200, "text/html; charset=utf-8", html);
}

static void handle_save()
{
    if (!auth()) return;
    String kind = server->arg("kind");
    String name = server->arg("name");
    String content = server->arg("content");
    content.replace("\r\n", "\n");
    String error;
    bool ok = false;
    String back = "/";

    if (kind == "config") {
        ok = config_replace(content, error);
        back = "/edit?kind=config";
    } else if (kind == "glossary") {
        ok = write_file(GLOSSARY_PATH, content);
        back = "/edit?kind=glossary";
    } else if (kind == "template" && template_valid_name(name)) {
        ok = template_save(name, content);
        back = "/templates";
    }
    if (!ok) {
        server->send(400, "text/plain", "Not saved. " + error);
        return;
    }
    server->sendHeader("Location", back);
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

static void handle_mcp()
{
    if (!auth()) return;
    String response;
    int status = mcp_handle(server->arg("plain"), response);
    if (status == 202) server->send(202);
    else server->send(status, "application/json", response);
}

static void handle_mcp_other()
{
    // Stateless server: no server-initiated SSE stream, no sessions to delete
    server->send(405, "text/plain", "Method not allowed");
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
    server->on("/search", handle_search);
    server->on("/templates", handle_templates);
    server->on("/edit", handle_edit);
    server->on("/wifi", handle_wifi);
    server->on("/save", HTTP_POST, handle_save);
    server->on("/mcp", HTTP_POST, handle_mcp);
    server->on("/mcp", HTTP_GET, handle_mcp_other);
    server->on("/mcp", HTTP_DELETE, handle_mcp_other);
    const char *headers[] = {"Authorization"};
    server->collectHeaders(headers, 1);
    server->begin();
    if (MDNS.begin("knowpod")) MDNS.addService("http", "tcp", 80);

    stop_requested = false;
    running = true;
    xTaskCreatePinnedToCore(web_task, "web", WEB_STACK, nullptr, 1, &task, 0);
    Serial.printf("Web access on %s (user " WEB_USER "), MCP on %s\n", web_url().c_str(), web_mcp_url().c_str());
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

String web_mcp_url()
{
    return "http://" + wifi_ip() + "/mcp";
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
    suspended = false;
    if (running || starting || !config_web_enabled() || config_wifi().empty()) return;
    if (last_attempt && millis() - last_attempt < AUTOSTART_RETRY_MS) return;
    last_attempt = millis();
    starting = true;
    xTaskCreatePinnedToCore(starter_task, "web_start", 6144, nullptr, 1, nullptr, 0);
}
