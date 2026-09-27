package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// The MCP server lets AI assistants (Claude, ChatGPT, …) read and write a user's notes and
// tasks. It speaks the Model Context Protocol's Streamable HTTP transport in its simplest,
// stateless form: every JSON-RPC request is POSTed to MCPPath and answered with one JSON
// response; there are no sessions and no server-sent events. Each user enables it with an
// access token of their own (Settings → Account), sent as "Authorization: Bearer kpm_…".

// mcpProtocolVersions are the protocol versions the server speaks, newest first.
var mcpProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// mcpInstructions tell the assistant what the server is for.
const mcpInstructions = `knowpod holds the user's notes: text notes, transcribed and summarized recordings, ` +
	`documents from a reMarkable tablet and kanban boards, organized in folders and labels. Any note can ` +
	`be a task with a due date and priority. Notes are referred to by their ID or by their number ("#12"). ` +
	`Use search_notes to find notes, get_note to read one, and list_tasks for the user's todo list.`

// maxMCPBody bounds a request to the MCP server.
const maxMCPBody = 4 << 20

// JSON-RPC error codes.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message expects no response: a notification, or a
// client's response to a server request (which this server never sends).
func (m *rpcRequest) isNotification() bool { return len(m.ID) == 0 || string(m.ID) == "null" }

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// handleMCP answers the JSON-RPC messages POSTed to the MCP endpoint: one message, or a
// batch of them.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	acc, err := (*service.Account)(nil), service.ErrUnauthorized
	if s.mcp != nil {
		acc, err = s.mcp.Authenticate(r.Context(), bearer(r))
	}
	if err != nil {
		if !errors.Is(err, service.ErrUnauthorized) {
			s.log.Error("mcp authentication failed", "err", err)
			writeCode(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="knowpod", error="invalid_token"`)
		writeCode(w, http.StatusUnauthorized, "invalid_token", "missing or invalid MCP access token")
		return
	}
	ctx := context.WithValue(r.Context(), accountKey, acc)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, rpcFailure(nil, rpcInvalidRequest, "request too large"))
		return
	}
	var batch []json.RawMessage
	isBatch := len(body) > 0 && body[0] == '['
	if isBatch {
		if err := json.Unmarshal(body, &batch); err != nil {
			writeJSON(w, http.StatusBadRequest, rpcFailure(nil, rpcParseError, "parse error"))
			return
		}
	} else {
		batch = []json.RawMessage{body}
	}
	out := []*rpcResponse{}
	for _, raw := range batch {
		if resp := s.mcpMessage(ctx, raw); resp != nil {
			out = append(out, resp)
		}
	}
	switch {
	case len(out) == 0:
		// Only notifications (and responses): accepted, nothing to answer.
		w.WriteHeader(http.StatusAccepted)
	case isBatch:
		writeJSON(w, http.StatusOK, out)
	default:
		writeJSON(w, http.StatusOK, out[0])
	}
}

// handleMCPNotAllowed answers GET (a server-sent event stream) and DELETE (ending a
// session): the stateless server offers neither.
func (s *Server) handleMCPNotAllowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", http.MethodPost)
	writeCode(w, http.StatusMethodNotAllowed, "method_not_allowed", "the MCP endpoint accepts POST only")
}

// mcpMessage handles one JSON-RPC message; notifications return nil.
func (s *Server) mcpMessage(ctx context.Context, raw json.RawMessage) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return rpcFailure(nil, rpcParseError, "parse error")
	}
	if req.isNotification() {
		return nil
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return rpcFailure(req.ID, rpcInvalidRequest, "invalid request")
	}
	var result any
	var rerr *rpcError
	switch req.Method {
	case "initialize":
		result, rerr = s.mcpInitialize(req.Params)
	case "ping":
		result = struct{}{}
	case "tools/list":
		result = map[string]any{"tools": mcpToolList()}
	case "tools/call":
		result, rerr = s.mcpCallTool(ctx, req.Params)
	case "resources/list":
		result = map[string]any{"resources": []any{}}
	case "resources/templates/list":
		result = map[string]any{"resourceTemplates": []any{}}
	case "prompts/list":
		result = map[string]any{"prompts": []any{}}
	default:
		rerr = &rpcError{Code: rpcMethodNotFound, Message: "method not found: " + req.Method}
	}
	if rerr != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rerr}
	}
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func rpcFailure(id json.RawMessage, code int, msg string) *rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// mcpInitialize agrees on the protocol version (the client's, when the server speaks it)
// and describes the server.
func (s *Server) mcpInitialize(params json.RawMessage) (any, *rpcError) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: rpcInvalidParams, Message: "invalid params"}
		}
	}
	version := mcpProtocolVersions[0]
	if slices.Contains(mcpProtocolVersions, p.ProtocolVersion) {
		version = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": "knowpod", "title": "knowpod", "version": s.version},
		"instructions":    mcpInstructions,
	}, nil
}

// mcpCallTool runs a tool. Failures of the tool itself (a note that doesn't exist, invalid
// input) are reported in the result, so the assistant sees them and can correct itself.
func (s *Server) mcpCallTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: rpcInvalidParams, Message: "invalid params"}
	}
	tool := findMCPTool(p.Name)
	if tool == nil {
		return nil, &rpcError{Code: rpcInvalidParams, Message: "unknown tool: " + p.Name}
	}
	args := p.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	out, err := tool.run(s, ctx, accountFrom(ctx), args)
	if err != nil {
		return mcpToolError(s.mcpErrorText(err)), nil
	}
	text, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		s.log.Error("mcp tool result", "tool", p.Name, "err", err)
		return nil, &rpcError{Code: rpcInternalError, Message: "internal error"}
	}
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(text)}},
		"structuredContent": out,
	}, nil
}

func mcpToolError(msg string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": msg}},
		"isError": true,
	}
}

// mcpErrorText is the message an assistant is shown for a failed tool: the service's for
// errors the API reports to clients, a generic one (logged) for everything else.
func (s *Server) mcpErrorText(err error) string {
	var bad *mcpArgError
	if errors.As(err, &bad) {
		return bad.Error()
	}
	for _, e := range errorCodes {
		if errors.Is(err, e.err) {
			return err.Error()
		}
	}
	s.log.Error("mcp tool failed", "err", err)
	return "internal error"
}

// mcpArgError is a tool call with invalid arguments.
type mcpArgError struct{ msg string }

func (e *mcpArgError) Error() string { return e.msg }
