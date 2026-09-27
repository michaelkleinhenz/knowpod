// Package oauth models the OAuth 2.1 clients (AI assistants such as Claude or ChatGPT) that
// register with knowpod, and the grants users give them to reach their notes through the MCP
// server.
package oauth

import "time"

// Client authentication methods at the token endpoint (RFC 7591).
const (
	AuthNone        = "none"                // public client, PKCE only
	AuthSecretPost  = "client_secret_post"  // secret in the form body
	AuthSecretBasic = "client_secret_basic" // secret in HTTP Basic authentication
)

// Client is an application registered through dynamic client registration. Only the SHA-256
// of its secret is stored; public clients have none.
type Client struct {
	ID           string    `bson:"_id"`
	Name         string    `bson:"name,omitempty"`
	RedirectURIs []string  `bson:"redirectUris"`
	AuthMethod   string    `bson:"authMethod"`
	SecretHash   string    `bson:"secretHash,omitempty"`
	CreatedAt    time.Time `bson:"createdAt"`
}

// Grant is a user's permission for a client to use the MCP server on their behalf. It starts
// as an authorization code, which the client exchanges for an access token and a refresh
// token; refreshing replaces both. Only the SHA-256 of each is stored.
type Grant struct {
	ID       string `bson:"_id"`
	UserID   string `bson:"userId"`
	ClientID string `bson:"clientId"`
	Scope    string `bson:"scope,omitempty"`
	// RedirectURI and CodeChallenge (PKCE, S256) are checked when the code is exchanged.
	RedirectURI   string `bson:"redirectUri"`
	CodeChallenge string `bson:"codeChallenge,omitempty"`
	CodeHash      string `bson:"codeHash,omitempty"`

	AccessHash      string     `bson:"accessHash,omitempty"`
	AccessExpiresAt *time.Time `bson:"accessExpiresAt,omitempty"`
	RefreshHash     string     `bson:"refreshHash,omitempty"`

	CreatedAt  time.Time  `bson:"createdAt"`
	LastUsedAt *time.Time `bson:"lastUsedAt,omitempty"`
	// ExpiresAt is when the grant ends: the code's expiry until it is exchanged, then the
	// refresh token's. The database deletes expired grants.
	ExpiresAt time.Time `bson:"expiresAt"`
}

// Issued reports whether the grant's code was exchanged for tokens.
func (g *Grant) Issued() bool { return g.AccessHash != "" }
