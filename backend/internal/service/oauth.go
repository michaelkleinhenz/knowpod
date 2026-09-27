package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/oauth"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// knowpod is an OAuth 2.1 authorization server for its own MCP server, so AI assistants that
// only connect through OAuth (Claude's custom connectors, ChatGPT apps, …) can reach a user's
// notes: the assistant registers itself (dynamic client registration), sends the user to
// knowpod to sign in and allow it, and exchanges the code it gets back (with PKCE) for an
// access token and a refresh token.

// OAuthScope is the one scope there is: using the MCP server.
const OAuthScope = "mcp"

const (
	oauthAccessPrefix  = "kpo_"
	oauthRefreshPrefix = "kpr_"
	oauthCodeTTL       = 5 * time.Minute
	oauthAccessTTL     = time.Hour
	oauthRefreshTTL    = 90 * 24 * time.Hour
	// oauthTouchEvery limits how often a grant's last use is written.
	oauthTouchEvery = 5 * time.Minute
	maxRedirectURIs = 10
	maxClientName   = 200
)

// OAuthError is an error with an OAuth error code (RFC 6749 §5.2, RFC 7591 §3.2.2). The HTTP
// layer answers it in the OAuth form.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

func oauthErr(code, desc string) *OAuthError { return &OAuthError{Code: code, Description: desc} }

// ClientRegistration is a dynamic client registration request (RFC 7591).
type ClientRegistration struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
}

// RegisteredClient answers a registration. The secret is only returned here.
type RegisteredClient struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientSecretExpiresAt   *int64   `json:"client_secret_expires_at,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
}

// AuthorizeRequest holds the parameters of an authorization request, named as in OAuth.
// Resource (RFC 8707) is the MCP server the client wants a token for.
type AuthorizeRequest struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	Resource            string `json:"resource"`
}

// AuthorizeView tells the user who asks for access. RedirectTo is set instead when the
// request can't be granted but the client may be told so: the browser goes back to it.
type AuthorizeView struct {
	ClientName  string `json:"clientName,omitempty"`
	RedirectURI string `json:"redirectUri,omitempty"`
	RedirectTo  string `json:"redirectTo,omitempty"`
}

// TokenRequest is a request to the token endpoint, named as in OAuth.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	CodeVerifier string
	RefreshToken string
	ClientID     string
	ClientSecret string
	Resource     string
}

// TokenResponse is a successful token response (RFC 6749 §5.1).
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// MCPApp is an AI assistant a user connected through OAuth.
type MCPApp struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// OAuthService is the authorization server for the MCP server.
type OAuthService struct {
	repo  ports.OAuthRepository
	users ports.UserRepository
	clock func() time.Time
}

// NewOAuthService builds the service.
func NewOAuthService(repo ports.OAuthRepository, users ports.UserRepository) *OAuthService {
	return &OAuthService{repo: repo, users: users, clock: time.Now}
}

// Register registers a client (RFC 7591). Clients that authenticate at the token endpoint
// get a secret; public clients ("none") rely on PKCE alone.
func (s *OAuthService) Register(ctx context.Context, req ClientRegistration) (*RegisteredClient, error) {
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > maxRedirectURIs {
		return nil, oauthErr("invalid_redirect_uri", "one to ten redirect_uris are required")
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			return nil, oauthErr("invalid_redirect_uri", "redirect URI not allowed: "+u)
		}
	}
	method := req.TokenEndpointAuthMethod
	if method == "" {
		method = oauth.AuthSecretBasic // the default of RFC 7591
	}
	if method != oauth.AuthNone && method != oauth.AuthSecretPost && method != oauth.AuthSecretBasic {
		return nil, oauthErr("invalid_client_metadata", "unsupported token_endpoint_auth_method")
	}
	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{"authorization_code", "refresh_token"}
	}
	for _, g := range grantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return nil, oauthErr("invalid_client_metadata", "unsupported grant type: "+g)
		}
	}
	responseTypes := req.ResponseTypes
	if len(responseTypes) == 0 {
		responseTypes = []string{"code"}
	}
	for _, t := range responseTypes {
		if t != "code" {
			return nil, oauthErr("invalid_client_metadata", "unsupported response type: "+t)
		}
	}
	name := strings.TrimSpace(req.ClientName)
	if len([]rune(name)) > maxClientName {
		name = string([]rune(name)[:maxClientName])
	}
	now := s.clock().UTC()
	c := &oauth.Client{ID: newID(), Name: name, RedirectURIs: req.RedirectURIs, AuthMethod: method, CreatedAt: now}
	out := &RegisteredClient{
		ClientID: c.ID, ClientIDIssuedAt: now.Unix(), RedirectURIs: c.RedirectURIs, ClientName: name,
		TokenEndpointAuthMethod: method, GrantTypes: grantTypes, ResponseTypes: responseTypes, Scope: OAuthScope,
	}
	if method != oauth.AuthNone {
		out.ClientSecret = randomToken("kps_")
		c.SecretHash = hashToken(out.ClientSecret)
		never := int64(0)
		out.ClientSecretExpiresAt = &never
	}
	if err := s.repo.CreateClient(ctx, c); err != nil {
		return nil, err
	}
	return out, nil
}

// CheckAuthorization validates an authorization request before the user is asked.
// mcpResource is the MCP server's URL. An unknown client or redirect URI is an *OAuthError
// (the browser must not be sent there); other problems are sent back to the client.
func (s *OAuthService) CheckAuthorization(ctx context.Context, req AuthorizeRequest, mcpResource string) (*AuthorizeView, error) {
	c, err := s.checkClient(ctx, req)
	if err != nil {
		return nil, err
	}
	if e := checkAuthorizeParams(req, mcpResource); e != nil {
		return &AuthorizeView{RedirectTo: authorizeRedirect(req, url.Values{"error": {e.Code}, "error_description": {e.Description}})}, nil
	}
	name := c.Name
	if name == "" {
		name = redirectHost(req.RedirectURI)
	}
	return &AuthorizeView{ClientName: name, RedirectURI: req.RedirectURI}, nil
}

// Authorize answers the user's decision on an authorization request with the address the
// browser goes back to the client with: an authorization code, or the refusal.
func (s *OAuthService) Authorize(ctx context.Context, acc *Account, req AuthorizeRequest, approve bool, mcpResource string) (string, error) {
	if acc.ID == "" {
		return "", errors.Join(ErrForbidden, errors.New("MCP access belongs to a user; sign in"))
	}
	if _, err := s.checkClient(ctx, req); err != nil {
		return "", err
	}
	if e := checkAuthorizeParams(req, mcpResource); e != nil {
		return authorizeRedirect(req, url.Values{"error": {e.Code}, "error_description": {e.Description}}), nil
	}
	if !approve {
		return authorizeRedirect(req, url.Values{"error": {"access_denied"}, "error_description": {"the user declined"}}), nil
	}
	code := randomToken("")
	now := s.clock().UTC()
	g := &oauth.Grant{
		ID: newID(), UserID: acc.ID, ClientID: req.ClientID, Scope: OAuthScope, RedirectURI: req.RedirectURI,
		CodeChallenge: req.CodeChallenge, CodeHash: hashToken(code), CreatedAt: now, ExpiresAt: now.Add(oauthCodeTTL),
	}
	if err := s.repo.CreateGrant(ctx, g); err != nil {
		return "", err
	}
	return authorizeRedirect(req, url.Values{"code": {code}}), nil
}

// Token answers the token endpoint: it exchanges an authorization code, or a refresh token,
// for new tokens.
func (s *OAuthService) Token(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	c, err := s.authenticateClient(ctx, req.ClientID, req.ClientSecret)
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	switch req.GrantType {
	case "authorization_code":
		g, err := s.repo.ClaimCode(ctx, hashToken(req.Code))
		if errors.Is(err, ErrNotFound) || (err == nil && (g.ClientID != c.ID || !now.Before(g.ExpiresAt) || g.Issued())) {
			return nil, oauthErr("invalid_grant", "invalid or expired authorization code")
		}
		if err != nil {
			return nil, err
		}
		if req.RedirectURI != "" && req.RedirectURI != g.RedirectURI {
			return nil, oauthErr("invalid_grant", "redirect_uri does not match the authorization request")
		}
		if !verifyPKCE(req.CodeVerifier, g.CodeChallenge) {
			return nil, oauthErr("invalid_grant", "invalid code_verifier")
		}
		resp := s.issue(g, now)
		if err := s.repo.UpdateGrant(ctx, g); err != nil {
			return nil, err
		}
		return resp, nil
	case "refresh_token":
		prev := hashToken(req.RefreshToken)
		g, err := s.repo.GetGrantByRefreshHash(ctx, prev)
		if errors.Is(err, ErrNotFound) || (err == nil && (g.ClientID != c.ID || !now.Before(g.ExpiresAt))) {
			return nil, oauthErr("invalid_grant", "invalid or expired refresh token")
		}
		if err != nil {
			return nil, err
		}
		resp := s.issue(g, now)
		if err := s.repo.RotateGrant(ctx, g, prev); errors.Is(err, ErrNotFound) {
			return nil, oauthErr("invalid_grant", "invalid or expired refresh token")
		} else if err != nil {
			return nil, err
		}
		return resp, nil
	case "":
		return nil, oauthErr("invalid_request", "grant_type is required")
	}
	return nil, oauthErr("unsupported_grant_type", "unsupported grant type: "+req.GrantType)
}

// Revoke ends the grant of an access or refresh token of the client (RFC 7009). Unknown
// tokens are not an error.
func (s *OAuthService) Revoke(ctx context.Context, token, clientID, clientSecret string) error {
	c, err := s.authenticateClient(ctx, clientID, clientSecret)
	if err != nil {
		return err
	}
	h := hashToken(token)
	g, err := s.repo.GetGrantByAccessHash(ctx, h)
	if errors.Is(err, ErrNotFound) {
		g, err = s.repo.GetGrantByRefreshHash(ctx, h)
	}
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if g.ClientID != c.ID {
		return nil
	}
	if err := s.repo.DeleteGrant(ctx, g.ID); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// IsAccessToken reports whether token has the form of an OAuth access token.
func IsAccessToken(token string) bool { return strings.HasPrefix(token, oauthAccessPrefix) }

// Authenticate resolves an OAuth access token to the account of its user. Unknown and
// expired tokens return ErrUnauthorized.
func (s *OAuthService) Authenticate(ctx context.Context, token string) (*Account, error) {
	if !IsAccessToken(token) {
		return nil, ErrUnauthorized
	}
	g, err := s.repo.GetGrantByAccessHash(ctx, hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	if g.AccessExpiresAt == nil || !now.Before(*g.AccessExpiresAt) {
		return nil, ErrUnauthorized
	}
	u, err := s.users.Get(ctx, g.UserID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if g.LastUsedAt == nil || now.Sub(*g.LastUsedAt) >= oauthTouchEvery {
		// Only shown to the user; a failure doesn't stop the request.
		_ = s.repo.TouchGrant(ctx, g.ID, now)
	}
	return account(u), nil
}

// Apps lists the AI assistants the user connected, newest first.
func (s *OAuthService) Apps(ctx context.Context, acc *Account) ([]MCPApp, error) {
	if acc.ID == "" {
		return []MCPApp{}, nil
	}
	grants, err := s.repo.ListGrants(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	out := []MCPApp{}
	for _, g := range grants {
		if !g.Issued() {
			continue
		}
		name, ok := names[g.ClientID]
		if !ok {
			if c, err := s.repo.GetClient(ctx, g.ClientID); err == nil {
				name = c.Name
				if name == "" && len(c.RedirectURIs) > 0 {
					name = redirectHost(c.RedirectURIs[0])
				}
			} else if !errors.Is(err, ErrNotFound) {
				return nil, err
			}
			names[g.ClientID] = name
		}
		out = append(out, MCPApp{ID: g.ID, Name: name, CreatedAt: g.CreatedAt, LastUsedAt: g.LastUsedAt})
	}
	return out, nil
}

// Disconnect revokes the user's grant id: the assistant's tokens stop working.
func (s *OAuthService) Disconnect(ctx context.Context, acc *Account, id string) error {
	if acc.ID == "" {
		return ErrNotFound
	}
	grants, err := s.repo.ListGrants(ctx, acc.ID)
	if err != nil {
		return err
	}
	for _, g := range grants {
		if g.ID == id {
			return s.repo.DeleteGrant(ctx, id)
		}
	}
	return ErrNotFound
}

// issue gives the grant new tokens, replacing any it had.
func (s *OAuthService) issue(g *oauth.Grant, now time.Time) *TokenResponse {
	access, refresh := randomToken(oauthAccessPrefix), randomToken(oauthRefreshPrefix)
	expires := now.Add(oauthAccessTTL)
	g.CodeHash = ""
	g.AccessHash, g.AccessExpiresAt = hashToken(access), &expires
	g.RefreshHash = hashToken(refresh)
	g.ExpiresAt = now.Add(oauthRefreshTTL)
	return &TokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: int(oauthAccessTTL / time.Second),
		RefreshToken: refresh, Scope: g.Scope,
	}
}

// checkClient finds the request's client and checks its redirect URI.
func (s *OAuthService) checkClient(ctx context.Context, req AuthorizeRequest) (*oauth.Client, error) {
	c, err := s.repo.GetClient(ctx, req.ClientID)
	if errors.Is(err, ErrNotFound) || (err == nil && req.ClientID == "") {
		return nil, oauthErr("invalid_client", "unknown client")
	}
	if err != nil {
		return nil, err
	}
	for _, u := range c.RedirectURIs {
		if redirectMatches(u, req.RedirectURI) {
			return c, nil
		}
	}
	return nil, oauthErr("invalid_redirect_uri", "redirect_uri is not registered for this client")
}

// authenticateClient finds the client and checks its secret, unless it is a public client.
func (s *OAuthService) authenticateClient(ctx context.Context, id, secret string) (*oauth.Client, error) {
	if id == "" {
		return nil, oauthErr("invalid_client", "client_id is required")
	}
	c, err := s.repo.GetClient(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, oauthErr("invalid_client", "unknown client")
	}
	if err != nil {
		return nil, err
	}
	if c.AuthMethod != oauth.AuthNone &&
		subtle.ConstantTimeCompare([]byte(hashToken(secret)), []byte(c.SecretHash)) != 1 {
		return nil, oauthErr("invalid_client", "invalid client credentials")
	}
	return c, nil
}

// checkAuthorizeParams checks what the client may be told about: the response type, PKCE and
// the resource.
func checkAuthorizeParams(req AuthorizeRequest, mcpResource string) *OAuthError {
	switch {
	case req.ResponseType != "code":
		return oauthErr("unsupported_response_type", "response_type must be code")
	case req.CodeChallenge == "" || req.CodeChallengeMethod != "S256":
		return oauthErr("invalid_request", "PKCE with code_challenge_method S256 is required")
	case !sameResource(req.Resource, mcpResource):
		return oauthErr("invalid_target", "unknown resource; this server issues tokens for "+mcpResource)
	}
	return nil
}

// authorizeRedirect adds the parameters (and the client's state) to its redirect URI.
func authorizeRedirect(req AuthorizeRequest, params url.Values) string {
	u, err := url.Parse(req.RedirectURI)
	if err != nil {
		return req.RedirectURI
	}
	q := u.Query()
	for k, v := range params {
		q[k] = v
	}
	if req.State != "" {
		q.Set("state", req.State)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// sameResource reports whether a requested resource (empty: none named) is the MCP server.
// The scheme isn't compared, as a proxy in front of knowpod may hide it.
func sameResource(resource, mcpResource string) bool {
	if resource == "" {
		return true
	}
	a, err1 := url.Parse(resource)
	b, err2 := url.Parse(mcpResource)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(a.Host, b.Host) && strings.TrimSuffix(a.Path, "/") == strings.TrimSuffix(b.Path, "/")
}

// verifyPKCE checks a code verifier against its S256 challenge (RFC 7636).
func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// validRedirectURI allows https addresses, http on the own computer (loopback) and the
// custom schemes of native apps, without fragments.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || strings.Contains(raw, "#") {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return u.Host != ""
	case "http":
		return isLoopback(u.Hostname())
	case "javascript", "data", "file", "vbscript", "blob", "about", "ftp", "ws", "wss":
		return false
	}
	return true
}

// redirectMatches compares a redirect URI with a registered one. Loopback addresses may use
// any port (RFC 8252 §7.3), as native apps listen on whatever port is free.
func redirectMatches(registered, given string) bool {
	if registered == given {
		return true
	}
	r, err1 := url.Parse(registered)
	g, err2 := url.Parse(given)
	if err1 != nil || err2 != nil || r.Scheme != "http" || g.Scheme != "http" || !isLoopback(r.Hostname()) {
		return false
	}
	return r.Hostname() == g.Hostname() && r.Path == g.Path && r.RawQuery == g.RawQuery
}

func isLoopback(host string) bool {
	return slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, host)
}

// redirectHost names a client without a name by where it sends the user back to.
func redirectHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// randomToken returns a new 256-bit random token with the prefix.
func randomToken(prefix string) string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return prefix + base64.RawURLEncoding.EncodeToString(b[:])
}
