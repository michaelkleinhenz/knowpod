// Package push models the browsers and installed apps that receive a user's notifications
// (Web Push subscriptions).
package push

import "time"

// Subscription is one browser's push subscription. Its ID is derived from the endpoint, so
// subscribing the same browser again replaces it.
type Subscription struct {
	ID       string `bson:"_id"`
	UserID   string `bson:"userId"`
	Endpoint string `bson:"endpoint"`
	P256dh   string `bson:"p256dh"`
	Auth     string `bson:"auth"`
	// UserAgent describes the browser, to tell the user's devices apart.
	UserAgent string    `bson:"userAgent,omitempty"`
	CreatedAt time.Time `bson:"createdAt"`
}
