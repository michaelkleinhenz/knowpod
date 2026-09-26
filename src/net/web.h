#pragma once

#include <Arduino.h>

// Local web page for browsing, downloading and searching recordings and for
// editing templates, glossary and config.json, plus an MCP server at /mcp.
// Runs in its own task and keeps Wi-Fi on while active.
//
// Authentication: HTTP basic auth (user "knowpod") or "Authorization: Bearer
// <password>" with the password from config.json "web_password" (generated
// on first use).

// Starts the server if it is enabled in config.json and not running, without
// blocking (connects Wi-Fi in the background). Call regularly.
void web_poll();

bool web_start(String &error);   // blocking; also used by the settings screen
void web_stop();                 // e.g. while recording; web_poll() restarts it later
bool web_active();
String web_url();                // "http://192.168.1.23/"
String web_mcp_url();            // "http://192.168.1.23/mcp"
