package http

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

const testVerifier = "a-code-verifier-that-is-long-enough-for-pkce-0123456789"

func testChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// form posts a form-encoded request and decodes the JSON answer into out.
func (c *client) form(path string, values url.Values, headers map[string]string, out any) int {
	c.f.t.Helper()
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	for k, v := range headers {
		h[k] = v
	}
	return c.do("POST", path, []byte(values.Encode()), h, out).StatusCode
}

// authorize runs the consent page's approval and returns the redirect it answers.
func authorize(t *testing.T, user *client, params map[string]any) *url.URL {
	t.Helper()
	var out struct{ RedirectTo string }
	if res := user.do("POST", "/api/v1/oauth/authorize", params, nil, &out); res.StatusCode != 200 {
		t.Fatalf("authorize: %d", res.StatusCode)
	}
	u, err := url.Parse(out.RedirectTo)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestOAuthFlow(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	anon := f.browser()

	// Without a token the MCP server points at its metadata.
	res := anon.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, nil, nil)
	want := `resource_metadata="` + f.srv.URL + "/.well-known/oauth-protected-resource/mcp" + `"`
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), want) {
		t.Fatalf("no token: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}

	var prm struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	anon.do("GET", "/.well-known/oauth-protected-resource/mcp", nil, nil, &prm)
	if prm.Resource != f.srv.URL+"/mcp" || len(prm.AuthorizationServers) != 1 || prm.AuthorizationServers[0] != f.srv.URL {
		t.Fatalf("resource metadata: %+v", prm)
	}
	var meta map[string]any
	anon.do("GET", "/.well-known/oauth-authorization-server", nil, nil, &meta)
	if meta["issuer"] != f.srv.URL || meta["token_endpoint"] != f.srv.URL+"/oauth/token" ||
		meta["registration_endpoint"] != f.srv.URL+"/oauth/register" || meta["authorization_endpoint"] != f.srv.URL+"/oauth/authorize" {
		t.Fatalf("server metadata: %v", meta)
	}

	// A browser-based assistant may read the metadata from anywhere.
	res = anon.do("GET", "/.well-known/oauth-authorization-server", nil, map[string]string{"Origin": "https://claude.ai"}, nil)
	if res.Header.Get("Access-Control-Allow-Origin") != "https://claude.ai" {
		t.Fatalf("CORS: %q", res.Header.Get("Access-Control-Allow-Origin"))
	}

	// Registration.
	var reg service.RegisteredClient
	res = anon.do("POST", "/oauth/register", map[string]any{
		"client_name": "Claude", "redirect_uris": []string{"https://claude.ai/api/mcp/auth_callback"},
		"token_endpoint_auth_method": "client_secret_post",
	}, nil, &reg)
	if res.StatusCode != 201 || reg.ClientID == "" || reg.ClientSecret == "" {
		t.Fatalf("register: %d %+v", res.StatusCode, reg)
	}
	var bad struct{ Error string }
	if res := anon.do("POST", "/oauth/register", map[string]any{"redirect_uris": []string{"http://evil.example/cb"}}, nil, &bad); res.StatusCode != 400 || bad.Error != "invalid_redirect_uri" {
		t.Fatalf("plain http redirect: %d %+v", res.StatusCode, bad)
	}

	q := url.Values{
		"client_id": {reg.ClientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "response_type": {"code"},
		"code_challenge": {testChallenge(testVerifier)}, "code_challenge_method": {"S256"}, "state": {"xyz"},
		"resource": {f.srv.URL + "/mcp"}, "scope": {"mcp"},
	}
	params := map[string]any{}
	for k := range q {
		params[k] = q.Get(k)
	}

	// The consent page needs a signed-in user and a known client.
	if res := anon.do("GET", "/api/v1/oauth/authorize?"+q.Encode(), nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("signed out: %d", res.StatusCode)
	}
	var info service.AuthorizeView
	if res := admin.do("GET", "/api/v1/oauth/authorize?"+q.Encode(), nil, nil, &info); res.StatusCode != 200 || info.ClientName != "Claude" || info.RedirectTo != "" {
		t.Fatalf("info: %d %+v", res.StatusCode, info)
	}
	wrong := url.Values{}
	for k, v := range q {
		wrong[k] = v
	}
	wrong.Set("redirect_uri", "https://evil.example/cb")
	var e struct{ Code string }
	if res := admin.do("GET", "/api/v1/oauth/authorize?"+wrong.Encode(), nil, nil, &e); res.StatusCode != 400 || e.Code != "invalid_redirect_uri" {
		t.Fatalf("unregistered redirect: %d %+v", res.StatusCode, e)
	}
	noPKCE := url.Values{}
	for k, v := range q {
		noPKCE[k] = v
	}
	noPKCE.Del("code_challenge")
	if admin.do("GET", "/api/v1/oauth/authorize?"+noPKCE.Encode(), nil, nil, &info); !strings.Contains(info.RedirectTo, "error=invalid_request") {
		t.Fatalf("without PKCE: %+v", info)
	}

	// Declining sends the refusal back.
	params["approve"] = false
	if u := authorize(t, admin, params); u.Query().Get("error") != "access_denied" || u.Query().Get("state") != "xyz" {
		t.Fatalf("declined: %s", u)
	}

	params["approve"] = true
	u := authorize(t, admin, params)
	code := u.Query().Get("code")
	if code == "" || u.Query().Get("state") != "xyz" || u.Host != "claude.ai" {
		t.Fatalf("approved: %s", u)
	}

	exchange := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"client_id": {reg.ClientID}, "client_secret": {"wrong"}, "code_verifier": {testVerifier},
	}
	var terr struct{ Error string }
	if code := anon.form("/oauth/token", exchange, nil, &terr); code != 401 || terr.Error != "invalid_client" {
		t.Fatalf("wrong secret: %d %+v", code, terr)
	}
	exchange.Set("client_secret", reg.ClientSecret)
	var tok service.TokenResponse
	if code := anon.form("/oauth/token", exchange, nil, &tok); code != 200 || !strings.HasPrefix(tok.AccessToken, "kpo_") ||
		tok.RefreshToken == "" || tok.TokenType != "Bearer" || tok.ExpiresIn != 3600 {
		t.Fatalf("exchange: %d %+v", code, tok)
	}
	// A code is exchanged once.
	if code := anon.form("/oauth/token", exchange, nil, &terr); code != 400 || terr.Error != "invalid_grant" {
		t.Fatalf("second exchange: %d %+v", code, terr)
	}

	if code, res := f.script(tok.AccessToken).mcpCall("tools/list", nil); code != 200 || res.Error != nil {
		t.Fatalf("MCP with OAuth token: %d %+v", code, res.Error)
	}
	// The token isn't an API token.
	if res := f.script(tok.AccessToken).do("GET", "/api/v1/auth/me", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("OAuth token on the API: %d", res.StatusCode)
	}

	// Refreshing replaces both tokens.
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok.RefreshToken}}
	var tok2 service.TokenResponse
	basic := map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(reg.ClientID+":"+reg.ClientSecret))}
	if code := anon.form("/oauth/token", refresh, basic, &tok2); code != 200 || tok2.AccessToken == tok.AccessToken || tok2.RefreshToken == tok.RefreshToken {
		t.Fatalf("refresh: %d %+v", code, tok2)
	}
	if code := anon.form("/oauth/token", refresh, basic, &terr); code != 400 || terr.Error != "invalid_grant" {
		t.Fatalf("old refresh token: %d %+v", code, terr)
	}
	if code := f.script(tok.AccessToken).mcpStatus(); code != 401 {
		t.Fatalf("old access token: %d", code)
	}
	if code := f.script(tok2.AccessToken).mcpStatus(); code != 200 {
		t.Fatalf("new access token: %d", code)
	}

	// The user sees the assistant and can disconnect it.
	var apps []service.MCPApp
	admin.do("GET", "/api/v1/me/mcp/apps", nil, nil, &apps)
	if len(apps) != 1 || apps[0].Name != "Claude" || apps[0].LastUsedAt == nil {
		t.Fatalf("apps: %+v", apps)
	}
	other := f.signedIn(adminEmail, adminPassword)
	if res := other.do("DELETE", "/api/v1/me/mcp/apps/nope", nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("unknown app: %d", res.StatusCode)
	}
	if res := admin.do("DELETE", "/api/v1/me/mcp/apps/"+apps[0].ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("disconnect: %d", res.StatusCode)
	}
	if code := f.script(tok2.AccessToken).mcpStatus(); code != 401 {
		t.Fatalf("disconnected token: %d", code)
	}
}

func TestOAuthPublicClient(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	anon := f.browser()

	var reg service.RegisteredClient
	anon.do("POST", "/oauth/register", map[string]any{
		"client_name": "Claude Code", "redirect_uris": []string{"http://localhost:33418/callback"}, "token_endpoint_auth_method": "none",
	}, nil, &reg)
	if reg.ClientID == "" || reg.ClientSecret != "" {
		t.Fatalf("register: %+v", reg)
	}
	// Loopback redirects may use any port.
	params := map[string]any{
		"client_id": reg.ClientID, "redirect_uri": "http://localhost:50123/callback", "response_type": "code",
		"code_challenge": testChallenge(testVerifier), "code_challenge_method": "S256", "approve": true,
	}
	u := authorize(t, admin, params)
	if u.Host != "localhost:50123" || u.Query().Get("code") == "" {
		t.Fatalf("authorize: %s", u)
	}
	exchange := url.Values{
		"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "client_id": {reg.ClientID},
		"code_verifier": {testVerifier + "x"},
	}
	var terr struct{ Error string }
	if code := anon.form("/oauth/token", exchange, nil, &terr); code != 400 || terr.Error != "invalid_grant" {
		t.Fatalf("wrong verifier: %d %+v", code, terr)
	}

	u = authorize(t, admin, params)
	exchange.Set("code", u.Query().Get("code"))
	exchange.Set("code_verifier", testVerifier)
	var tok service.TokenResponse
	if code := anon.form("/oauth/token", exchange, nil, &tok); code != 200 {
		t.Fatalf("exchange: %d", code)
	}
	if code := anon.form("/oauth/revoke", url.Values{"token": {tok.RefreshToken}, "client_id": {reg.ClientID}}, nil, nil); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if code := f.script(tok.AccessToken).mcpStatus(); code != 401 {
		t.Fatalf("revoked: %d", code)
	}

	// A resource other than this MCP server is refused.
	params["resource"] = "https://elsewhere.example/mcp"
	if u := authorize(t, admin, params); u.Query().Get("error") != "invalid_target" {
		t.Fatalf("other resource: %s", u)
	}
}
