// Package live carries real-time log events from the worker to the API's
// Server-Sent Events feed (the dashboard's Live Stream Feed).
//
// Events go through Redis pub/sub on one channel per account
// (hs:live:{user_id}). Delivery is best-effort by design: a dashboard that is
// not connected simply misses events and reads the logs API instead, so
// publishing never blocks or fails the pipeline.
package live

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
)

// Event types.
const (
	LogCreated = "log.created" // stored by the processor, waiting for delivery
	LogUpdated = "log.updated" // a delivery attempt was recorded
)

// Event is one change of a webhook log. It never contains payload data.
type Event struct {
	Type       string    `json:"type"`
	LogID      string    `json:"log_id"`
	EndpointID string    `json:"endpoint_id"`
	Status     string    `json:"status"`
	Method     string    `json:"method,omitempty"`
	EventType  string    `json:"event_type,omitempty"`
	Attempt    int       `json:"attempt,omitempty"`
	StatusCode int       `json:"status_code,omitempty"`
	LatencyMs  float64   `json:"latency_ms,omitempty"` // gateway (log.created) or delivery (log.updated) time
	ReplayOf   string    `json:"replay_of,omitempty"`
	At         time.Time `json:"at"`
}

// Publisher publishes events. The zero value (nil client) does nothing.
type Publisher struct {
	rdb  *redis.Client
	keys redisx.Keys
}

// NewPublisher returns a Publisher on rdb.
func NewPublisher(rdb *redis.Client, keys redisx.Keys) *Publisher {
	return &Publisher{rdb: rdb, keys: keys}
}

// Publish sends ev to userID's channel, best-effort.
func (p *Publisher) Publish(ctx context.Context, userID string, ev Event) {
	if p == nil || p.rdb == nil || userID == "" {
		return
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = p.rdb.Publish(ctx, p.keys.LiveChannel(userID), b).Err()
}

// Subscribe returns the raw JSON events of userID until ctx is done.
// The channel is closed when the subscription ends.
func Subscribe(ctx context.Context, rdb *redis.Client, keys redisx.Keys, userID string) (<-chan []byte, error) {
	sub := rdb.Subscribe(ctx, keys.LiveChannel(userID))
	if _, err := sub.Receive(ctx); err != nil { // wait for the subscription to be active
		_ = sub.Close()
		return nil, err
	}
	out := make(chan []byte, 64)
	go func() {
		defer close(out)
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- []byte(m.Payload):
				default: // slow client: drop rather than block Redis
				}
			}
		}
	}()
	return out, nil
}
