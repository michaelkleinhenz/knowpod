package http

import "net/http"

func (s *Server) handleGetMCP(w http.ResponseWriter, r *http.Request) {
	v, err := s.mcp.Status(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleEnableMCP makes a new MCP access token, replacing the previous one.
func (s *Server) handleEnableMCP(w http.ResponseWriter, r *http.Request) {
	v, err := s.mcp.Enable(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleDisableMCP(w http.ResponseWriter, r *http.Request) {
	if err := s.mcp.Disable(r.Context(), accountFrom(r.Context())); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
