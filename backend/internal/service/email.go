package service

import (
	"context"
	"net/mail"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// Mailer sends an email with the given configuration (ses.Sender).
type Mailer interface {
	Send(ctx context.Context, cfg *settings.Email, to, subject, body string) error
}

// EmailService holds the email (Amazon SES) configuration and sends emails with it.
type EmailService struct {
	settings ports.SettingsRepository
	mailer   Mailer
	clock    func() time.Time
}

// NewEmailService builds the service.
func NewEmailService(st ports.SettingsRepository, m Mailer) *EmailService {
	return &EmailService{settings: st, mailer: m, clock: time.Now}
}

// EmailView is the email configuration as shown to administrators: the secret key itself is
// never returned.
type EmailView struct {
	Configured           bool      `json:"configured"`
	Region               string    `json:"region"`
	AccessKeyID          string    `json:"accessKeyId"`
	SecretAccessKeyHint  string    `json:"secretAccessKeyHint,omitempty"`
	SecretAccessKeyIsSet bool      `json:"secretAccessKeyConfigured"`
	From                 string    `json:"from"`
	UpdatedAt            time.Time `json:"updatedAt,omitempty"`
}

// EmailUpdate changes the configuration. Nil fields are left unchanged; an empty
// SecretAccessKey removes the key.
type EmailUpdate struct {
	Region          *string `json:"region,omitempty"`
	AccessKeyID     *string `json:"accessKeyId,omitempty"`
	SecretAccessKey *string `json:"secretAccessKey,omitempty"`
	From            *string `json:"from,omitempty"`
}

func emailView(e *settings.Email) *EmailView {
	return &EmailView{Configured: e.Configured(), Region: e.Region, AccessKeyID: e.AccessKeyID,
		SecretAccessKeyIsSet: e.SecretAccessKey != "", SecretAccessKeyHint: keyHint(e.SecretAccessKey),
		From: e.From, UpdatedAt: e.UpdatedAt}
}

// Settings returns the current configuration.
func (s *EmailService) Settings(ctx context.Context) (*EmailView, error) {
	st, err := s.settings.Email(ctx)
	if err != nil {
		return nil, err
	}
	return emailView(st), nil
}

// UpdateSettings applies an update.
func (s *EmailService) UpdateSettings(ctx context.Context, u EmailUpdate) (*EmailView, error) {
	st, err := s.settings.Email(ctx)
	if err != nil {
		return nil, err
	}
	if u.Region != nil {
		st.Region = strings.TrimSpace(*u.Region)
	}
	if u.AccessKeyID != nil {
		st.AccessKeyID = strings.TrimSpace(*u.AccessKeyID)
	}
	if u.SecretAccessKey != nil {
		st.SecretAccessKey = strings.TrimSpace(*u.SecretAccessKey)
	}
	if u.From != nil {
		st.From = strings.TrimSpace(*u.From)
	}
	if st.Region != "" && strings.ContainsAny(st.Region, " \t\n/") {
		return nil, invalid("the region looks like \"eu-central-1\"")
	}
	if st.From != "" {
		if _, err := mail.ParseAddress(st.From); err != nil {
			return nil, invalid("the sender must be an email address, e.g. \"KnowPod <noreply@example.com>\"")
		}
	}
	st.UpdatedAt = s.clock().UTC()
	if err := s.settings.SaveEmail(ctx, st); err != nil {
		return nil, err
	}
	return emailView(st), nil
}

// Enabled reports whether emails can be sent.
func (s *EmailService) Enabled(ctx context.Context) bool {
	st, err := s.settings.Email(ctx)
	return err == nil && st.Configured()
}

// Send sends a plain-text email to one recipient.
func (s *EmailService) Send(ctx context.Context, to, subject, body string) error {
	st, err := s.settings.Email(ctx)
	if err != nil {
		return err
	}
	if !st.Configured() {
		return invalid("email is not configured; set region, keys and sender first")
	}
	return s.mailer.Send(ctx, st, to, subject, body)
}

// SendTest sends a test email to recipient.
func (s *EmailService) SendTest(ctx context.Context, recipient string) error {
	recipient = strings.TrimSpace(recipient)
	a, err := mail.ParseAddress(recipient)
	if err != nil || a.Address != recipient {
		return invalid("the recipient must be a plain email address")
	}
	return s.Send(ctx, recipient, "KnowPod test email", "This is a test email from KnowPod. Email sending works.")
}
