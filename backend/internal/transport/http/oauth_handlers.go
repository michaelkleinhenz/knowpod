package http

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// Most AI assistants only connect to MCP servers through OAuth 2.1, as the MCP authorization
// spec describes: a request to /mcp without a token is answered 401 with a link to the
// protected resource metadata (RFC 9728), which names knowpod itself as the authorization
// server; its metadata (RFC 8414) lists the endpoints. The assistant registers itself
// (RFC 7591) and opens /oauth/authorize in the browser, a page of the web app where the
// signed-in user allows it; it then exchanges the code for tokens at /oauth/token.

const (
	oauthProtectedResourcePath = "/.well-known/oauth-protected-resource"
	oauthServerMetadataPath    = "/.well-known/oauth-authorization-server"
	oauthAuthorizePath         = "/oauth/authorize"
	oauthTokenPath             = "/oauth/token"
	oauthRegisterPath          = "/oauth/register"
	oauthRevokePath            = "/oauth/revoke"
)

// oauthPublicPath reports whether path is called by AI assistants rather than the web app:
// the MCP server and the OAuth endpoints. Any origin may call them (none uses cookies).
func oauthPublicPath(path string) bool {
	switch path {
	case service.MCPPath, oauthProtectedResourcePath, oauthProtectedResourcePath + service.MCPPath,
		oauthServerMetadataPath, oauthTokenPath, oauthRegisterPath, oauthRevokePath:
		return true
	}
	return false
}

// mcpResource is the MCP server's URL as the client sees it.
func mcpResource(r *http.Request) string { return baseURL(r) + service.MCPPath }

// resourceMetadataURL is where the MCP server's protected resource metadata is.
func resourceMetadataURL(r *http.Request) string {
	return baseURL(r) + oauthProtectedResourcePath + service.MCPPath
}

func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeCode(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 mcpResource(r),
		"authorization_servers":    []string{baseURL(r)},
		"scopes_supported":         []string{service.OAuthScope},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "knowpod",
	})
}

func (s *Server) handleAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeCode(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	base := baseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     base,
		"authorization_endpoint":                     base + oauthAuthorizePath,
		"token_endpoint":                             base + oauthTokenPath,
		"registration_endpoint":                      base + oauthRegisterPath,
		"revocation_endpoint":                        base + oauthRevokePath,
		"scopes_supported":                           []string{service.OAuthScope},
		"response_types_supported":                   []string{"code"},
		"response_modes_supported":                   []string{"query"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none", "client_secret_post", "client_secret_basic"},
		"revocation_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
	})
}

// handleOAuthRegister registers a client (dynamic client registration).
func (s *Server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeCode(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req service.ClientRegistration
	if !decode(w, r, &req) {
		return
	}
	c, err := s.oauth.Register(r.Context(), req)
	if err != nil {
		s.writeOAuthErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, c)
}

// handleOAuthToken is the token endpoint: authorization codes and refresh tokens are
// exchanged for new tokens.
func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeCode(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if !parseOAuthForm(w, r) {
		return
	}
	id, secret := clientCredentials(r)
	res, err := s.oauth.Token(r.Context(), service.TokenRequest{
		GrantType: r.PostForm.Get("grant_type"), Code: r.PostForm.Get("code"),
		RedirectURI: r.PostForm.Get("redirect_uri"), CodeVerifier: r.PostForm.Get("code_verifier"),
		RefreshToken: r.PostForm.Get("refresh_token"), ClientID: id, ClientSecret: secret,
		Resource: r.PostForm.Get("resource"),
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if err != nil {
		s.writeOAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleOAuthRevoke revokes an access or refresh token (RFC 7009).
func (s *Server) handleOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeCode(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if !parseOAuthForm(w, r) {
		return
	}
	id, secret := clientCredentials(r)
	if err := s.oauth.Revoke(r.Context(), r.PostForm.Get("token"), id, secret); err != nil {
		s.writeOAuthErr(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleOAuthAuthorizeInfo checks an authorization request for the web app's consent page
// and tells it which assistant asks.
func (s *Server) handleOAuthAuthorizeInfo(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeCode(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	q := r.URL.Query()
	v, err := s.oauth.CheckAuthorization(r.Context(), authorizeRequest(q), mcpResource(r))
	if err != nil {
		s.writeOAuthAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleOAuthAuthorize takes the user's decision on the consent page and answers with the
// address the browser returns to the assistant with.
func (s *Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeCode(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var body struct {
		service.AuthorizeRequest
		Approve bool `json:"approve"`
	}
	if !decode(w, r, &body) {
		return
	}
	to, err := s.oauth.Authorize(r.Context(), accountFrom(r.Context()), body.AuthorizeRequest, body.Approve, mcpResource(r))
	if err != nil {
		s.writeOAuthAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirectTo": to})
}

func (s *Server) handleListMCPApps(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeJSON(w, http.StatusOK, []service.MCPApp{})
		return
	}
	apps, err := s.oauth.Apps(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

// handleDisconnectMCPApp revokes an assistant's access.
func (s *Server) handleDisconnectMCPApp(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeCode(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := s.oauth.Disconnect(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func authorizeRequest(q url.Values) service.AuthorizeRequest {
	return service.AuthorizeRequest{
		ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), ResponseType: q.Get("response_type"),
		CodeChallenge: q.Get("code_challenge"), CodeChallengeMethod: q.Get("code_challenge_method"),
		Scope: q.Get("scope"), State: q.Get("state"), Resource: q.Get("resource"),
	}
}

// parseOAuthForm reads a form-encoded OAuth request, answering invalid_request on failure.
func parseOAuthForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, oauthErrorBody{Error: "invalid_request", Description: "invalid form body"})
		return false
	}
	return true
}

// clientCredentials returns the client's ID and secret from HTTP Basic authentication or
// the form body.
func clientCredentials(r *http.Request) (string, string) {
	if id, secret, ok := r.BasicAuth(); ok {
		// Both are form-encoded in the header (RFC 6749 §2.3.1).
		if v, err := url.QueryUnescape(id); err == nil {
			id = v
		}
		if v, err := url.QueryUnescape(secret); err == nil {
			secret = v
		}
		return id, secret
	}
	return r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
}

type oauthErrorBody struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

// writeOAuthErr answers an OAuth endpoint's error in the OAuth form.
func (s *Server) writeOAuthErr(w http.ResponseWriter, err error) {
	var oe *service.OAuthError
	if !errors.As(err, &oe) {
		s.log.Error("oauth request failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, oauthErrorBody{Error: "server_error", Description: "internal error"})
		return
	}
	status := http.StatusBadRequest
	if oe.Code == "invalid_client" {
		status = http.StatusUnauthorized
		w.Header().Set("WWW-Authenticate", `Basic realm="knowpod"`)
	}
	writeJSON(w, status, oauthErrorBody{Error: oe.Code, Description: oe.Description})
}

// writeOAuthAPIErr answers the web app's consent page: OAuth errors as the API's errors.
func (s *Server) writeOAuthAPIErr(w http.ResponseWriter, err error) {
	var oe *service.OAuthError
	if errors.As(err, &oe) {
		writeCode(w, http.StatusBadRequest, oe.Code, oe.Description)
		return
	}
	s.writeErr(w, err)
}
