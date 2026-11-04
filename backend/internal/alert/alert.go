// Package alert notifies customers when deliveries keep failing.
//
// Flow:
//
//	dispatcher ──Notify──► Service (cooldown per endpoint) ──XADD──► hs:stream:alerts
//	                                                                     │
//	                         Sender (worker goroutine) ◄────────────────┘
//	                           ├─ webhook   (JSON, SSRF-protected client)
//	                           ├─ slack     (incoming webhook)
//	                           ├─ telegram  (bot sendMessage)
//	                           └─ email     (SMTP)
//
// The dispatcher raises an Alert once per webhook, when its failed attempts
// reach the endpoint's alert_threshold_failures or when delivery is abandoned.
// Service then applies a per-endpoint cooldown so a broken destination never
// floods the customer: alerts raised during the cooldown are counted and the
// next alert says how many were grouped. Messages are localized to the
// account's language (en, pt-BR, es).
package alert

import (
	"context"
	"time"
)

// Alert describes a delivery that keeps failing.
type Alert struct {
	LogID      string
	EndpointID string
	UserID     string
	EventType  string
	Attempts   int    // failed attempts so far
	LastStatus int    // last HTTP status (0 = no response)
	LastError  string // error class when there was no response (timeout, dns, ...)
	Final      bool   // true when no more retries will happen
	At         time.Time
	Suppressed int // alerts of the same endpoint grouped into this one (cooldown)
}

// Context is what a rendered message needs to know about the endpoint.
type Context struct {
	EndpointName string
	EndpointSlug string
}

// Notifier receives alerts.
type Notifier interface {
	Notify(ctx context.Context, a Alert) error
}

// Noop discards alerts.
type Noop struct{}

// Notify implements Notifier.
func (Noop) Notify(context.Context, Alert) error { return nil }
