package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// mcpTokenPrefix starts every MCP access token, so they are recognized (and told apart
// from device tokens) at a glance.
const mcpTokenPrefix = "kpm_"

// MCPPath is the path of the MCP server endpoint.
const MCPPath = "/mcp"

// MCPView is the state of the account's MCP access. Token is only returned when it was
// just made: only its hash is stored.
type MCPView struct {
	Enabled   bool       `json:"enabled"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	Token     string     `json:"token,omitempty"`
}

// MCPAccessService manages the tokens AI assistants use to reach a user's notes through the
// MCP server, and signs those requests in.
type MCPAccessService struct {
	users ports.UserRepository
	clock func() time.Time
}

// NewMCPAccessService builds the service.
func NewMCPAccessService(users ports.UserRepository) *MCPAccessService {
	return &MCPAccessService{users: users, clock: time.Now}
}

// Status says whether the account has MCP access.
func (s *MCPAccessService) Status(ctx context.Context, acc *Account) (*MCPView, error) {
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	return &MCPView{Enabled: u.MCP.TokenHash != "", CreatedAt: u.MCP.CreatedAt}, nil
}

// Enable makes a new access token for the account, replacing the previous one (which stops
// working), and returns it.
func (s *MCPAccessService) Enable(ctx context.Context, acc *Account) (*MCPView, error) {
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	var b [32]byte
	_, _ = rand.Read(b[:])
	token := mcpTokenPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	now := s.clock().UTC()
	u.MCP = user.MCP{TokenHash: hashToken(token), CreatedAt: &now}
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	return &MCPView{Enabled: true, CreatedAt: &now, Token: token}, nil
}

// Disable turns the account's MCP access off; its token stops working.
func (s *MCPAccessService) Disable(ctx context.Context, acc *Account) error {
	u, err := s.user(ctx, acc)
	if err != nil {
		return err
	}
	u.MCP = user.MCP{}
	return s.users.Update(ctx, u)
}

// Authenticate resolves an MCP access token to the account of its user. Unknown tokens
// return ErrUnauthorized.
func (s *MCPAccessService) Authenticate(ctx context.Context, token string) (*Account, error) {
	if !strings.HasPrefix(token, mcpTokenPrefix) {
		return nil, ErrUnauthorized
	}
	u, err := s.users.GetByMCPTokenHash(ctx, hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	return account(u), nil
}

func (s *MCPAccessService) user(ctx context.Context, acc *Account) (*user.User, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("MCP access belongs to a user; sign in"))
	}
	return s.users.Get(ctx, acc.ID)
}
