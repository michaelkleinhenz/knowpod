#pragma once

#include <Arduino.h>

// Model Context Protocol server (Streamable HTTP transport, JSON responses,
// stateless). Exposes the recordings as read-only tools: list, details,
// transcript, search, action items and device status.
//
// Handles one JSON-RPC message; returns the HTTP status (200 with `response`,
// or 202 with no body for notifications).
int mcp_handle(const String &request, String &response);
