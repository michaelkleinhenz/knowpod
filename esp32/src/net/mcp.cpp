#include "mcp.h"
#include <ArduinoJson.h>
#include <SD_MMC.h>
#include "hw/clock.h"
#include "hw/power.h"
#include "net/wifi.h"
#include "proc/worker.h"
#include "store/recordings.h"
#include "store/search.h"

#define SERVER_VERSION        "1.0.0"
#define LATEST_PROTOCOL       "2025-06-18"
#define TRANSCRIPT_PAGE_CHARS 20000

static const char *const SUPPORTED_PROTOCOLS[] = {"2025-06-18", "2025-03-26", "2024-11-05"};

static const char INSTRUCTIONS[] =
    "knowpod is a pocket voice recorder. Each recording has an id (e.g. 20260924-143012), "
    "a transcript with [H:MM:SS] timestamps, and (once processed) a summary with title, "
    "tags, action items and explanations of moments the user highlighted. Use "
    "list_recordings or search_recordings to find recordings, then get_recording for the "
    "notes and get_transcript for the full text.";

static const char TOOLS[] = R"json([
  {
    "name": "list_recordings",
    "title": "List recordings",
    "description": "Lists recordings, newest first, with id, date, title, duration, processing state and tags.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "limit": {"type": "integer", "description": "Maximum number of recordings (default 50)"},
        "since_days": {"type": "integer", "description": "Only recordings from the last N days"}
      }
    },
    "annotations": {"readOnlyHint": true}
  },
  {
    "name": "get_recording",
    "title": "Get recording notes",
    "description": "Returns a recording's metadata (date, duration, highlight times, template, model) and its summary notes in Markdown, including action items and highlights.",
    "inputSchema": {
      "type": "object",
      "properties": {"id": {"type": "string", "description": "Recording id"}},
      "required": ["id"]
    },
    "annotations": {"readOnlyHint": true}
  },
  {
    "name": "get_transcript",
    "title": "Get transcript",
    "description": "Returns the transcript of a recording in Markdown with [H:MM:SS] timestamps. Long transcripts are returned in pages; use offset to continue.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "id": {"type": "string", "description": "Recording id"},
        "offset": {"type": "integer", "description": "Character offset to start from (default 0)"},
        "max_chars": {"type": "integer", "description": "Maximum characters to return (default 20000)"}
      },
      "required": ["id"]
    },
    "annotations": {"readOnlyHint": true}
  },
  {
    "name": "search_recordings",
    "title": "Search recordings",
    "description": "Full-text search in summaries and transcripts (case-insensitive). Returns matching recordings with a text snippet around the first match.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "query": {"type": "string", "description": "Text to search for"},
        "limit": {"type": "integer", "description": "Maximum number of results (default 20)"}
      },
      "required": ["query"]
    },
    "annotations": {"readOnlyHint": true}
  },
  {
    "name": "get_action_items",
    "title": "Get action items",
    "description": "Collects the action items (task, owner, due date) from the summaries of recent recordings.",
    "inputSchema": {
      "type": "object",
      "properties": {"since_days": {"type": "integer", "description": "Only recordings from the last N days (default 30)"}}
    },
    "annotations": {"readOnlyHint": true}
  },
  {
    "name": "get_device_status",
    "title": "Get device status",
    "description": "Battery, storage, time, network and background processing status of the recorder.",
    "inputSchema": {"type": "object", "properties": {}},
    "annotations": {"readOnlyHint": true}
  }
])json";

// ============================================================
// Tools
// ============================================================

// Each tool writes its result (or an error message) to `out` and returns
// false on error. Errors are reported inside the result (isError) so the
// model sees them.

static bool in_range(const RecordingInfo &r, int since_days)
{
    if (since_days <= 0) return true;
    if (!r.created_unix) return false;
    return (uint32_t)time(nullptr) - r.created_unix <= (uint32_t)since_days * 86400;
}

static void add_recording(JsonObject o, const RecordingInfo &r)
{
    o["id"] = r.id;
    o["title"] = recording_display_title(r);
    if (r.created_unix) o["date"] = recording_display_date(r.created_unix);
    o["duration"] = format_duration(r.duration_s);
    o["state"] = recording_state_label(r.state);
    if (!r.error.isEmpty()) o["error"] = r.error;
}

static bool find_recording(JsonObjectConst args, RecordingInfo &info, String &error)
{
    String id = args["id"] | "";
    if (recording_valid_id(id) && recording_info(id, info)) return true;
    error = "No recording with id \"" + id + "\". Use list_recordings to find ids.";
    return false;
}

static bool tool_list_recordings(JsonObjectConst args, String &out)
{
    int limit = args["limit"] | 50;
    int since_days = args["since_days"] | 0;
    JsonDocument doc;
    JsonArray list = doc["recordings"].to<JsonArray>();
    for (const RecordingInfo &r : recordings_list()) {
        if ((int)list.size() >= limit) break;
        if (!in_range(r, since_days)) continue;
        JsonObject o = list.add<JsonObject>();
        add_recording(o, r);
        JsonDocument meta;
        if (recording_load_meta(r.id, meta) && meta["tags"].size()) o["tags"] = meta["tags"];
    }
    doc["total"] = recordings_list().size();
    serializeJson(doc, out);
    return true;
}

static bool tool_get_recording(JsonObjectConst args, String &out)
{
    RecordingInfo info;
    if (!find_recording(args, info, out)) return false;
    JsonDocument meta;
    recording_load_meta(info.id, meta);

    String &s = out;
    s = "# " + recording_display_title(info) + "\n\n";
    s += "- id: " + info.id + "\n";
    s += "- recorded: " + recording_display_date(info.created_unix) + "\n";
    s += "- duration: " + format_duration(info.duration_s) + "\n";
    s += "- state: " + recording_state_label(info.state) + "\n";
    if (!info.error.isEmpty()) s += "- error: " + info.error + "\n";
    JsonArrayConst hl = meta["highlights"];
    if (hl.size()) {
        s += "- highlights at: ";
        for (size_t i = 0; i < hl.size(); i++) s += (i ? ", " : "") + format_duration(hl[i] | 0.0f);
        s += "\n";
    }
    if (meta["template"].is<const char *>()) s += "- template: " + String(meta["template"] | "") + "\n";
    if (meta["model"].is<const char *>()) s += "- summary model: " + String(meta["model"] | "") + "\n";

    String summary;
    if (read_file(recording_path(info.id, "summary.md"), summary))
        s += "\n" + summary;
    else
        s += "\nNo summary yet; use get_transcript for the text.\n";

    String qa;
    if (read_file(recording_path(info.id, "qa.md"), qa)) s += "\n# Questions asked on the device\n\n" + qa;
    return true;
}

static bool tool_get_transcript(JsonObjectConst args, String &out)
{
    RecordingInfo info;
    if (!find_recording(args, info, out)) return false;
    String text;
    if (!read_file(recording_path(info.id, "transcript.md"), text)) {
        out = "No transcript yet (state: " + recording_state_label(info.state) + ").";
        return false;
    }

    int offset = max(0, (int)(args["offset"] | 0));
    int max_chars = constrain((int)(args["max_chars"] | TRANSCRIPT_PAGE_CHARS), 100, 100000);
    if (offset >= (int)text.length()) {
        out = "(offset beyond end; transcript has " + String(text.length()) + " characters)";
        return true;
    }

    int end = min((int)text.length(), offset + max_chars);
    // Don't cut in the middle of a UTF-8 character
    while (end < (int)text.length() && (text[end] & 0xC0) == 0x80) end++;
    out = text.substring(offset, end);
    if (end < (int)text.length())
        out += "\n\n[... " + String(text.length() - end) + " more characters; call again with offset=" +
               String(end) + "]";
    return true;
}

static bool tool_search(JsonObjectConst args, String &out)
{
    String query = args["query"] | "";
    if (query.isEmpty()) {
        out = "query must not be empty";
        return false;
    }
    int limit = args["limit"] | 20;

    JsonDocument doc;
    JsonArray results = doc["results"].to<JsonArray>();
    for (const SearchHit &h : search_recordings(query, limit)) {
        JsonObject o = results.add<JsonObject>();
        add_recording(o, h.recording);
        o["found_in"] = h.file;
        o["snippet"] = "..." + h.before + h.match + h.after + "...";
    }
    serializeJson(doc, out);
    return true;
}

static bool tool_action_items(JsonObjectConst args, String &out)
{
    int since_days = args["since_days"] | 30;
    JsonDocument doc;
    JsonArray items = doc["action_items"].to<JsonArray>();
    for (const RecordingInfo &r : recordings_list()) {
        if (!in_range(r, since_days)) continue;
        String json;
        JsonDocument summary;
        if (!read_file(recording_path(r.id, "summary.json"), json) || deserializeJson(summary, json)) continue;
        for (JsonObjectConst a : summary["action_items"].as<JsonArrayConst>()) {
            JsonObject o = items.add<JsonObject>();
            o["task"] = a["task"];
            if (!String(a["owner"] | "").isEmpty()) o["owner"] = a["owner"];
            if (!String(a["due"] | "").isEmpty()) o["due"] = a["due"];
            o["recording_id"] = r.id;
            o["recording"] = recording_display_title(r);
            if (r.created_unix) o["date"] = recording_display_date(r.created_unix);
        }
    }
    serializeJson(doc, out);
    return true;
}

static bool tool_device_status(JsonObjectConst, String &out)
{
    JsonDocument doc;
    PowerStatus p = power_status();
    if (p.battery_percent >= 0) doc["battery_percent"] = p.battery_percent;
    doc["charging"] = p.charging;
    doc["usb_power"] = p.usb_connected;
    doc["time"] = clock_valid() ? clock_format("%Y-%m-%d %H:%M %Z") : String("not set");
    doc["wifi"] = wifi_ssid();
    doc["sd_free_gb"] = roundf((SD_MMC.totalBytes() - SD_MMC.usedBytes()) / 1e8) / 10;
    doc["sd_total_gb"] = roundf(SD_MMC.totalBytes() / 1e8) / 10;
    doc["recordings"] = recordings_list().size();
    doc["waiting_for_processing"] = worker_pending();
    doc["waiting_for_upload"] = worker_pending_uploads();
    String status = worker_status();
    if (!status.isEmpty()) doc["processing"] = status;
    serializeJson(doc, out);
    return true;
}

static bool call_tool(const String &name, JsonObjectConst args, String &out)
{
    if (name == "list_recordings")   return tool_list_recordings(args, out);
    if (name == "get_recording")     return tool_get_recording(args, out);
    if (name == "get_transcript")    return tool_get_transcript(args, out);
    if (name == "search_recordings") return tool_search(args, out);
    if (name == "get_action_items")  return tool_action_items(args, out);
    if (name == "get_device_status") return tool_device_status(args, out);
    out = "Unknown tool: " + name;
    return false;
}

// ============================================================
// JSON-RPC
// ============================================================

static void set_error(JsonDocument &resp, int code, const char *message)
{
    resp["error"]["code"] = code;
    resp["error"]["message"] = message;
}

int mcp_handle(const String &request, String &response)
{
    JsonDocument req;
    JsonDocument resp;
    resp["jsonrpc"] = "2.0";

    if (deserializeJson(req, request) || !req.is<JsonObject>()) {
        resp["id"] = nullptr;
        set_error(resp, -32700, "Parse error (batches are not supported)");
        serializeJson(resp, response);
        return 200;
    }

    // Notifications (no id) and client responses need no answer
    if (req["id"].isNull()) return 202;
    resp["id"] = req["id"];

    String method = req["method"] | "";
    JsonObjectConst params = req["params"];

    if (method == "initialize") {
        String requested = params["protocolVersion"] | "";
        const char *version = LATEST_PROTOCOL;
        for (const char *v : SUPPORTED_PROTOCOLS)
            if (requested == v) version = v;
        JsonObject result = resp["result"].to<JsonObject>();
        result["protocolVersion"] = version;
        result["capabilities"]["tools"]["listChanged"] = false;
        result["serverInfo"]["name"] = "knowpod";
        result["serverInfo"]["title"] = "knowpod voice recorder";
        result["serverInfo"]["version"] = SERVER_VERSION;
        result["instructions"] = INSTRUCTIONS;
    } else if (method == "ping") {
        resp["result"].to<JsonObject>();
    } else if (method == "tools/list") {
        JsonDocument tools;
        deserializeJson(tools, TOOLS);
        resp["result"]["tools"] = tools;
    } else if (method == "tools/call") {
        String name = params["name"] | "";
        JsonObjectConst args = params["arguments"];
        JsonObject result = resp["result"].to<JsonObject>();
        JsonObject content = result["content"].to<JsonArray>().add<JsonObject>();
        content["type"] = "text";
        String text;
        bool ok = call_tool(name, args, text);
        content["text"] = text;
        result["isError"] = !ok;
        Serial.printf("MCP tool %s%s\n", name.c_str(), result["isError"] ? " (error)" : "");
    } else {
        set_error(resp, -32601, "Method not found");
    }

    serializeJson(resp, response);
    return 200;
}
