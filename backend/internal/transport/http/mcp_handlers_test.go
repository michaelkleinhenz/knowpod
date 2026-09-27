package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// mcpCall sends one JSON-RPC request to the MCP server and returns its response.
func (c *client) mcpCall(method string, params any) (int, rpcTestResponse) {
	c.f.t.Helper()
	var out rpcTestResponse
	res := c.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params},
		map[string]string{"Accept": "application/json, text/event-stream"}, &out)
	return res.StatusCode, out
}

// mcpStatus sends a ping and returns the HTTP status.
func (c *client) mcpStatus() int {
	c.f.t.Helper()
	return c.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, nil, nil).StatusCode
}

type rpcTestResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// tool calls a tool and returns its structured result, failing on errors unless wantErr.
func (c *client) tool(name string, args any, wantErr bool) map[string]any {
	c.f.t.Helper()
	code, res := c.mcpCall("tools/call", map[string]any{"name": name, "arguments": args})
	if code != 200 || res.Error != nil {
		c.f.t.Fatalf("%s: %d %+v", name, code, res.Error)
	}
	var out struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Structured map[string]any `json:"structuredContent"`
		IsError    bool           `json:"isError"`
	}
	if err := json.Unmarshal(res.Result, &out); err != nil {
		c.f.t.Fatal(err)
	}
	if out.IsError != wantErr {
		c.f.t.Fatalf("%s: isError %v, want %v: %s", name, out.IsError, wantErr, out.Content)
	}
	if wantErr {
		return map[string]any{"error": out.Content[0].Text}
	}
	return out.Structured
}

func TestMCPAccessSettings(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	var v service.MCPView
	if admin.do("GET", "/api/v1/me/mcp", nil, nil, &v); v.Enabled {
		t.Fatalf("enabled from the start: %+v", v)
	}
	if res := f.script("kpm_nope").do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, nil, nil); res.StatusCode != 401 ||
		!strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("unknown token: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if res := f.script(adminToken).do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, nil, nil); res.StatusCode != 401 {
		t.Fatalf("ADMIN_TOKEN on the MCP server: %d", res.StatusCode)
	}

	admin.do("POST", "/api/v1/me/mcp", nil, nil, &v)
	if !v.Enabled || !strings.HasPrefix(v.Token, "kpm_") || v.CreatedAt == nil {
		t.Fatalf("enable: %+v", v)
	}
	first := v.Token
	if code, res := f.script(first).mcpCall("ping", nil); code != 200 || res.Error != nil {
		t.Fatalf("ping: %d %+v", code, res.Error)
	}
	v = service.MCPView{}
	if admin.do("GET", "/api/v1/me/mcp", nil, nil, &v); !v.Enabled || v.Token != "" {
		t.Fatalf("status shows the token again: %+v", v)
	}

	// A new token replaces the old one.
	admin.do("POST", "/api/v1/me/mcp", nil, nil, &v)
	if code := f.script(first).mcpStatus(); code != 401 {
		t.Fatalf("replaced token: %d", code)
	}
	if code := f.script(v.Token).mcpStatus(); code != 200 {
		t.Fatalf("new token: %d", code)
	}
	if res := admin.do("DELETE", "/api/v1/me/mcp", nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("disable: %d", res.StatusCode)
	}
	if code := f.script(v.Token).mcpStatus(); code != 401 {
		t.Fatalf("disabled token: %d", code)
	}
}

func TestMCPProtocol(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	var v service.MCPView
	admin.do("POST", "/api/v1/me/mcp", nil, nil, &v)
	ai := f.script(v.Token)

	code, res := ai.mcpCall("initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "1"},
	})
	var init struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct{ Name string }
	}
	_ = json.Unmarshal(res.Result, &init)
	if code != 200 || init.ProtocolVersion != "2025-06-18" || init.Capabilities["tools"] == nil || init.ServerInfo.Name != "knowpod" {
		t.Fatalf("initialize: %d %s", code, res.Result)
	}
	if _, res := ai.mcpCall("initialize", map[string]any{"protocolVersion": "1999-01-01"}); !strings.Contains(string(res.Result), mcpProtocolVersions[0]) {
		t.Fatalf("unknown version not answered with the latest: %s", res.Result)
	}

	// Notifications are accepted without an answer.
	if r := ai.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, nil, nil); r.StatusCode != 202 {
		t.Fatalf("notification: %d", r.StatusCode)
	}
	if r := ai.do("GET", "/mcp", nil, nil, nil); r.StatusCode != 405 {
		t.Fatalf("GET: %d", r.StatusCode)
	}
	if _, res := ai.mcpCall("nope", nil); res.Error == nil || res.Error.Code != rpcMethodNotFound {
		t.Fatalf("unknown method: %+v", res)
	}
	if _, res := ai.mcpCall("tools/call", map[string]any{"name": "nope"}); res.Error == nil || res.Error.Code != rpcInvalidParams {
		t.Fatalf("unknown tool: %+v", res)
	}

	_, res = ai.mcpCall("tools/list", nil)
	var tools struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(res.Result, &tools)
	if len(tools.Tools) != len(mcpTools) || tools.Tools[0].InputSchema["type"] != "object" {
		t.Fatalf("tools/list: %s", res.Result)
	}

	// A batch gets an array of answers.
	var batch []rpcTestResponse
	ai.do("POST", "/mcp", []byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/x"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`), nil, &batch)
	if len(batch) != 2 || string(batch[1].ID) != "2" {
		t.Fatalf("batch: %+v", batch)
	}
}

func TestMCPTools(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")
	var v service.MCPView
	admin.do("POST", "/api/v1/me/mcp", nil, nil, &v)
	ai := f.script(v.Token)
	bob.do("POST", "/api/v1/me/mcp", nil, nil, &v)
	bobAI := f.script(v.Token)

	var work struct{ ID string }
	admin.do("POST", "/api/v1/folders", map[string]string{"name": "Work"}, nil, &work)
	var clients struct{ ID string }
	admin.do("POST", "/api/v1/folders", map[string]string{"name": "Clients", "parentId": work.ID}, nil, &clients)

	created := ai.tool("create_note", map[string]any{
		"title": "Call Anna", "markdown": "About the **offer**", "folder": "work/clients", "dueDate": "2026-10-01", "priority": 1,
	}, false)
	note := created["note"].(map[string]any)
	if note["folder"] != "Work/Clients" || note["task"].(map[string]any)["dueDate"] != "2026-10-01" || note["markdown"] != "About the **offer**" {
		t.Fatalf("create_note: %+v", note)
	}
	number := int(note["number"].(float64))
	ai.tool("create_note", map[string]any{"title": "Shopping list", "markdown": "milk, eggs"}, false)
	ai.tool("create_note", map[string]any{"title": "Sub", "parent": "#" + jsonNumber(number)}, false)

	if got := ai.tool("create_note", map[string]any{"title": "x", "folder": "Nowhere"}, true); !strings.Contains(got["error"].(string), "no folder") {
		t.Fatalf("unknown folder: %+v", got)
	}
	if got := ai.tool("create_note", map[string]any{"title": "x", "bogus": 1}, true); !strings.Contains(got["error"].(string), "bogus") {
		t.Fatalf("unknown argument: %+v", got)
	}

	found := ai.tool("search_notes", map[string]any{"query": "OFFER"}, false)["notes"].([]any)
	if len(found) != 1 || found[0].(map[string]any)["title"] != "Call Anna" {
		t.Fatalf("search: %+v", found)
	}
	if found := ai.tool("search_notes", map[string]any{"folder": "Work"}, false)["notes"].([]any); len(found) != 1 {
		t.Fatalf("search in folder and sub-folders: %+v", found)
	}
	if found := ai.tool("search_notes", map[string]any{"query": "#" + jsonNumber(number)}, false)["notes"].([]any); len(found) != 1 {
		t.Fatalf("search by number: %+v", found)
	}
	if found := ai.tool("search_notes", nil, false)["notes"].([]any); len(found) != 3 {
		t.Fatalf("recent notes: %+v", found)
	}

	got := ai.tool("get_note", map[string]any{"note": jsonNumber(number)}, false)["note"].(map[string]any)
	if got["title"] != "Call Anna" || got["labels"].([]any)[0] != "Task" {
		t.Fatalf("get_note: %+v", got)
	}

	tasks := ai.tool("list_tasks", nil, false)["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("list_tasks: %+v", tasks)
	}
	ai.tool("update_task", map[string]any{"note": note["id"], "done": true}, false)
	if tasks := ai.tool("list_tasks", nil, false)["tasks"].([]any); len(tasks) != 0 {
		t.Fatalf("done task still listed: %+v", tasks)
	}
	if tasks := ai.tool("list_tasks", map[string]any{"includeDone": true}, false)["tasks"].([]any); len(tasks) != 1 {
		t.Fatalf("includeDone: %+v", tasks)
	}

	updated := ai.tool("update_note", map[string]any{"note": note["id"], "title": "Call Anna back", "folder": ""}, false)["note"].(map[string]any)
	if updated["title"] != "Call Anna back" || updated["folderId"] != nil {
		t.Fatalf("update_note: %+v", updated)
	}
	if got := ai.tool("get_note", map[string]any{"note": note["id"]}, false)["note"].(map[string]any); got["markdown"] != "About the **offer**" {
		t.Fatalf("text changed by a title edit: %+v", got)
	}

	if labels := ai.tool("list_labels", nil, false)["labels"].([]any); len(labels) == 0 {
		t.Fatal("no labels")
	}
	if folders := ai.tool("list_folders", nil, false)["folders"].([]any); len(folders) != 2 || folders[1].(map[string]any)["path"] != "Work/Clients" {
		t.Fatalf("list_folders: %+v", folders)
	}

	// Each token reaches only its own user's notes.
	if found := bobAI.tool("search_notes", nil, false)["notes"].([]any); len(found) != 0 {
		t.Fatalf("bob sees admin's notes: %+v", found)
	}
	bobAI.tool("get_note", map[string]any{"note": note["id"]}, true)
	bobAI.tool("update_task", map[string]any{"note": note["id"], "done": false}, true)
}

func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }
