package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/push"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/webpush"
)

// Pusher delivers Web Push messages (webpush.Sender).
type Pusher interface {
	PublicKey() string
	Send(ctx context.Context, sub webpush.Subscription, payload []byte, ttl time.Duration) error
}

// pushServiceHosts are the push services of the browsers (Chrome, Edge, Firefox, Safari and
// the browsers built on them). Subscriptions must point there, so the server never sends
// requests to hosts a user picked.
var pushServiceHosts = []string{
	"fcm.googleapis.com", "android.googleapis.com", // Chrome, Android, Opera, Samsung Internet
	"push.services.mozilla.com", // Firefox
	"push.apple.com",            // Safari, iOS
	"notify.windows.com",        // Edge (legacy)
}

// knownPushService reports whether host is (a subdomain of) a browser push service.
func knownPushService(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range pushServiceHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// maxPushSubscriptions bounds the browsers one user can receive notifications on.
const maxPushSubscriptions = 20

// reminderTTL is how long a push service keeps a reminder for a device that is offline.
const reminderTTL = 24 * time.Hour

// maxListeners bounds the apps of one user that receive notifications (or note events)
// over a live connection at the same time.
const maxListeners = 10

// NotificationService sends users' notifications (task reminders) to the browsers and
// installed apps they turned them on in, through Web Push, and to the apps that listen for
// them over a live connection (the desktop app, which has no push service).
type NotificationService struct {
	subs  ports.PushSubscriptionRepository
	users ports.UserRepository
	recs  ports.RecordingRepository
	push  Pusher
	log   *slog.Logger
	clock func() time.Time
	// allowHost says which push services subscriptions may point to.
	allowHost func(host string) bool
	// live are the connections that receive notifications as they happen.
	live *hub[Message]
}

// NewNotificationService builds the service. Without a pusher, notifications are off.
func NewNotificationService(subs ports.PushSubscriptionRepository, users ports.UserRepository, recs ports.RecordingRepository, p Pusher, log *slog.Logger) *NotificationService {
	return &NotificationService{
		subs: subs, users: users, recs: recs, push: p, log: log, clock: time.Now, allowHost: knownPushService,
		live: newHub[Message](),
	}
}

// Listen makes a live connection receive the account's notifications until stop is called
// or the service shuts down, which closes the channel. A user with too many connections
// loses one of the others (usually a stale one of an app that went away).
func (s *NotificationService) Listen(acc *Account) (msgs <-chan Message, stop func(), err error) {
	if acc.ID == "" {
		return nil, nil, errors.Join(ErrForbidden, errors.New("notifications belong to a user; sign in"))
	}
	return s.live.listen(acc.ID)
}

// Shutdown ends all live connections (for the HTTP server's shutdown, which doesn't end
// long-lived requests by itself).
func (s *NotificationService) Shutdown() { s.live.shutdown() }

// listening says how many live connections receive the user's notifications.
func (s *NotificationService) listening(userID string) int { return s.live.count(userID) }

// notifyLive hands a message to the user's live connections and returns how many took it.
func (s *NotificationService) notifyLive(userID string, m Message) int { return s.live.send(userID, m) }

// PushDevice is a browser that receives the user's notifications.
type PushDevice struct {
	ID        string    `json:"id"`
	UserAgent string    `json:"userAgent,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// NotificationStatus says whether notifications can be sent (through Web Push), the key
// browsers subscribe with, and where the user gets them.
type NotificationStatus struct {
	Available bool         `json:"available"`
	PublicKey string       `json:"publicKey,omitempty"`
	Devices   []PushDevice `json:"devices"`
	// Listening counts the apps that receive them over a live connection right now.
	Listening int `json:"listening"`
}

// PushSubscriptionInput is a browser's PushSubscription.toJSON().
type PushSubscriptionInput struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// Status returns the account's notification setup.
func (s *NotificationService) Status(ctx context.Context, acc *Account) (*NotificationStatus, error) {
	out := &NotificationStatus{Devices: []PushDevice{}}
	if s.push != nil {
		out.Available, out.PublicKey = true, s.push.PublicKey()
	}
	if acc.ID == "" {
		return out, nil
	}
	out.Listening = s.listening(acc.ID)
	subs, err := s.subs.List(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		out.Devices = append(out.Devices, PushDevice{ID: sub.ID, UserAgent: sub.UserAgent, CreatedAt: sub.CreatedAt})
	}
	return out, nil
}

// subscriptionID identifies a subscription by its endpoint.
func subscriptionID(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:16])
}

// Subscribe makes a browser receive the account's notifications.
func (s *NotificationService) Subscribe(ctx context.Context, acc *Account, in PushSubscriptionInput, userAgent string) (*PushDevice, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("notifications belong to a user; sign in"))
	}
	if s.push == nil {
		return nil, errors.Join(ErrNotReady, errors.New("notifications are not available on this server"))
	}
	endpoint := strings.TrimSpace(in.Endpoint)
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(endpoint) > 2048 {
		return nil, invalid("the endpoint must be an https URL")
	}
	if u.Port() != "" || !s.allowHost(u.Hostname()) {
		return nil, invalid("the endpoint must be a browser push service")
	}
	if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(in.Keys.P256dh, "=")); err != nil || len(b) != 65 {
		return nil, invalid("keys.p256dh must be a P-256 public key")
	}
	if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(in.Keys.Auth, "=")); err != nil || len(b) < 16 || len(b) > 64 {
		return nil, invalid("keys.auth must be the subscription's authentication secret")
	}
	subs, err := s.subs.List(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	id := subscriptionID(endpoint)
	// The oldest subscriptions give way to new ones.
	for i := 0; len(subs)-i >= maxPushSubscriptions; i++ {
		if subs[i].ID != id {
			if err := s.subs.Delete(ctx, subs[i].ID); err != nil {
				return nil, err
			}
		}
	}
	sub := &push.Subscription{
		ID: id, UserID: acc.ID, Endpoint: endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth,
		UserAgent: truncateRunes(userAgent, 300), CreatedAt: s.clock().UTC(),
	}
	if err := s.subs.Save(ctx, sub); err != nil {
		return nil, err
	}
	return &PushDevice{ID: sub.ID, UserAgent: sub.UserAgent, CreatedAt: sub.CreatedAt}, nil
}

// Unsubscribe stops notifications to a browser, named by its endpoint or device ID.
func (s *NotificationService) Unsubscribe(ctx context.Context, acc *Account, endpointOrID string) error {
	id := endpointOrID
	if strings.Contains(endpointOrID, "://") {
		id = subscriptionID(strings.TrimSpace(endpointOrID))
	}
	subs, err := s.subs.List(ctx, acc.ID)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		if sub.ID == id {
			return s.subs.Delete(ctx, id)
		}
	}
	return nil
}

// Message is a notification.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// URL is the page of the web app that opens when the notification is clicked.
	URL string `json:"url,omitempty"`
	// Tag makes a newer notification with the same tag replace the older one.
	Tag string `json:"tag,omitempty"`
}

// Notify sends a message to all of the user's browsers and live connections and returns
// how many took it. Subscriptions the push service reports as gone are forgotten.
func (s *NotificationService) Notify(ctx context.Context, userID string, m Message, ttl time.Duration) (int, error) {
	sent := s.notifyLive(userID, m)
	if s.push == nil {
		return sent, nil
	}
	subs, err := s.subs.List(ctx, userID)
	if err != nil {
		return sent, err
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return sent, err
	}
	var errs []error
	for _, sub := range subs {
		err := s.push.Send(ctx, webpush.Subscription{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth}, payload, ttl)
		switch {
		case errors.Is(err, webpush.ErrGone):
			if err := s.subs.Delete(ctx, sub.ID); err != nil {
				errs = append(errs, err)
			}
		case err != nil:
			errs = append(errs, err)
		default:
			sent++
		}
	}
	if sent == 0 && len(errs) > 0 {
		return 0, errors.Join(errs...)
	}
	if len(errs) > 0 {
		s.log.Warn("some notifications were not delivered", "user", userID, "err", errors.Join(errs...))
	}
	return sent, nil
}

// SendTest sends a test notification to the account's browsers.
func (s *NotificationService) SendTest(ctx context.Context, acc *Account) (int, error) {
	if acc.ID == "" {
		return 0, errors.Join(ErrForbidden, errors.New("notifications belong to a user; sign in"))
	}
	if s.push == nil && s.listening(acc.ID) == 0 {
		return 0, errors.Join(ErrNotReady, errors.New("notifications are not available on this server"))
	}
	m := Message{Title: "knowpod", Body: "Notifications work. Reminders for your tasks will appear like this.", URL: "/", Tag: "test"}
	if acc.Language == "de" {
		m.Body = "Benachrichtigungen funktionieren. So erscheinen Erinnerungen an deine Aufgaben."
	}
	return s.Notify(ctx, acc.ID, m, time.Hour)
}

// SendDueReminders sends the reminders that are due and returns how many tasks were
// reminded of. Each reminder is taken from the database before it is sent, so it goes out
// at most once.
func (s *NotificationService) SendDueReminders(ctx context.Context) (int, error) {
	n := 0
	for ctx.Err() == nil {
		rec, err := s.recs.ClaimReminder(ctx, s.clock())
		if errors.Is(err, ErrNotFound) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if s.remind(ctx, rec, rec.OwnerID) {
			n++
		}
	}
	return n, ctx.Err()
}

// SendDueMemberReminders sends the reminders that are due to the users tasks are shared
// with, like SendDueReminders.
func (s *NotificationService) SendDueMemberReminders(ctx context.Context) (int, error) {
	n := 0
	for ctx.Err() == nil {
		rec, userID, err := s.recs.ClaimMemberReminder(ctx, s.clock())
		if errors.Is(err, ErrNotFound) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if s.remind(ctx, rec, userID) {
			n++
		}
	}
	return n, ctx.Err()
}

// remind sends the reminder of a task to a user and reports whether it went out.
func (s *NotificationService) remind(ctx context.Context, rec *recording.Recording, userID string) bool {
	if rec.Done || rec.Due == nil || rec.DeletedAt != nil {
		return false
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return false
	}
	if _, err := s.Notify(ctx, u.ID, reminderMessage(rec, u, s.clock()), reminderTTL); err != nil {
		s.log.Warn("sending a reminder failed", "id", rec.ID, "err", err)
		return false
	}
	return true
}

// Run sends due reminders every interval until ctx ends.
func (s *NotificationService) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		n, err := s.SendDueReminders(ctx)
		if err == nil {
			var m int
			m, err = s.SendDueMemberReminders(ctx)
			n += m
		}
		if err != nil && ctx.Err() == nil {
			s.log.Error("sending reminders failed", "err", err)
		} else if n > 0 {
			s.log.Info("sent reminders", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reminderMessage is the notification for a task, in the owner's language.
func reminderMessage(rec *recording.Recording, u *user.User, now time.Time) Message {
	title := "knowpod"
	if rec.Summary != nil && rec.Summary.Title != "" {
		title = rec.Summary.Title
	} else if rec.Title != "" {
		title = rec.Title
	}
	return Message{Title: title, Body: describeDue(rec.Due, u.Language, now.In(u.Location())), URL: "/conversations/" + rec.ID, Tag: "task-" + rec.ID}
}

var weekdaysDE = []string{"So.", "Mo.", "Di.", "Mi.", "Do.", "Fr.", "Sa."}

// describeDue says when a task is due, relative to now (in the owner's time zone), e.g.
// "Due today at 15:00" or "Fällig am Mo., 28.09.".
func describeDue(d *recording.Due, lang string, now time.Time) string {
	day, err := recording.ParseDate(d.Date)
	if err != nil {
		return ""
	}
	today := now.Format(recording.DateLayout)
	tomorrow := now.AddDate(0, 0, 1).Format(recording.DateLayout)
	if lang == "de" {
		when := fmt.Sprintf("am %s %s", weekdaysDE[day.Weekday()], day.Format("02.01."))
		switch d.Date {
		case today:
			when = "heute"
		case tomorrow:
			when = "morgen"
		}
		if d.Time != "" {
			when += " um " + d.Time
		}
		return "Fällig " + when
	}
	when := "on " + day.Format("Mon, Jan 2")
	switch d.Date {
	case today:
		when = "today"
	case tomorrow:
		when = "tomorrow"
	}
	if d.Time != "" {
		when += " at " + d.Time
	}
	return "Due " + when
}
