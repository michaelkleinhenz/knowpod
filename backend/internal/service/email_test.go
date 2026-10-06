package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

type sentMail struct{ to, subject, body string }

type fakeMailer struct {
	sent []sentMail
	err  error
}

func (m *fakeMailer) Send(_ context.Context, _ *settings.Email, to, subject, body string) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, sentMail{to, subject, body})
	return nil
}

func configureEmail(t *testing.T, s *EmailService) {
	t.Helper()
	r, k, id, from := "eu-central-1", "secret-key-1234", "AKIA123", "KnowPod <noreply@example.com>"
	if _, err := s.UpdateSettings(context.Background(), EmailUpdate{Region: &r, AccessKeyID: &id, SecretAccessKey: &k, From: &from}); err != nil {
		t.Fatal(err)
	}
}

func TestEmailSettingsHideTheSecret(t *testing.T) {
	s := NewEmailService(memory.NewSettings(), &fakeMailer{})
	ctx := context.Background()
	if v, _ := s.Settings(ctx); v.Configured {
		t.Fatal("configured from the start")
	}
	configureEmail(t, s)
	v, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Configured || !v.SecretAccessKeyIsSet || v.SecretAccessKeyHint != "…1234" || v.Region != "eu-central-1" {
		t.Fatalf("view: %+v", v)
	}
	// Changing another field keeps the secret.
	r := "us-east-1"
	if _, err := s.UpdateSettings(ctx, EmailUpdate{Region: &r}); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Settings(ctx); !v.Configured || v.Region != "us-east-1" {
		t.Fatalf("secret lost: %+v", v)
	}
	bad := "not an address"
	if _, err := s.UpdateSettings(ctx, EmailUpdate{From: &bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad sender: %v", err)
	}
}

func TestSendTestEmail(t *testing.T) {
	m := &fakeMailer{}
	s := NewEmailService(memory.NewSettings(), m)
	ctx := context.Background()
	if err := s.SendTest(ctx, "a@example.com"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unconfigured: %v", err)
	}
	configureEmail(t, s)
	for _, bad := range []string{"", "nope", "Bob <b@example.com>"} {
		if err := s.SendTest(ctx, bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("recipient %q: %v", bad, err)
		}
	}
	if err := s.SendTest(ctx, " a@example.com "); err != nil {
		t.Fatal(err)
	}
	if len(m.sent) != 1 || m.sent[0].to != "a@example.com" {
		t.Fatalf("sent: %+v", m.sent)
	}
	m.err = errors.New("boom")
	if err := s.SendTest(ctx, "a@example.com"); err == nil || errors.Is(err, ErrInvalidInput) {
		t.Fatalf("delivery error: %v", err)
	}
}

func TestDailyBriefingIsEmailedOnlyWhenAskedFor(t *testing.T) {
	f := newBriefingFixture(t)
	ctx := context.Background()
	f.s.ai = nil
	m := &fakeMailer{}
	emails := NewEmailService(memory.NewSettings(), m)
	f.s.Emails = emails
	// Daily 11:00 is past at 12:00; settings saved earlier, so move the clock a day on.
	save := func(email bool) {
		t.Helper()
		if s, _ := f.s.Settings(ctx, f.acc); s.Email {
			t.Fatal("email is on by default")
		}
		if _, err := f.s.UpdateSettings(ctx, f.acc, BriefingSettings{Daily: true, Time: "11:00", Notify: true, Email: email}); err != nil {
			t.Fatal(err)
		}
	}
	day := 0
	run := func() {
		t.Helper()
		day++
		f.s.clock = func() time.Time { return f.now.AddDate(0, 0, day) }
		if _, err := f.s.MakeDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	save(false)
	configureEmail(t, emails)
	run()
	if len(m.sent) != 0 {
		t.Fatalf("emailed without being asked: %+v", m.sent)
	}
	if _, err := f.s.UpdateSettings(ctx, f.acc, BriefingSettings{Daily: true, Time: "11:00", Notify: true, Email: true}); err != nil {
		t.Fatal(err)
	}
	if s, _ := f.s.Settings(ctx, f.acc); !s.Email {
		t.Fatal("email setting not kept")
	}
	run()
	if len(m.sent) != 1 || m.sent[0].to != "a@example.com" || !strings.Contains(m.sent[0].body, "Call Anna") {
		t.Fatalf("sent: %+v", m.sent)
	}
	// Not configured by the admin: nothing is sent, the briefing is made anyway.
	f.s.Emails = NewEmailService(memory.NewSettings(), m)
	run()
	if len(m.sent) != 1 {
		t.Fatalf("sent without configuration: %+v", m.sent)
	}
}
