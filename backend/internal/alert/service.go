package alert

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
)

// DefaultCooldown is the minimum time between two alerts of the same endpoint.
const DefaultCooldown = 10 * time.Minute

// Service is the Notifier used by the dispatcher: it applies the per-endpoint
// cooldown and enqueues alerts for the Sender. It never blocks on channels.
type Service struct {
	rdb      *redis.Client
	keys     redisx.Keys
	cooldown time.Duration
}

// NewService returns a Service. cooldown <= 0 disables grouping.
func NewService(rdb *redis.Client, keys redisx.Keys, cooldown time.Duration) *Service {
	return &Service{rdb: rdb, keys: keys, cooldown: cooldown}
}

// Notify implements Notifier.
func (s *Service) Notify(ctx context.Context, a Alert) error {
	if s.cooldown > 0 {
		first, err := s.rdb.SetNX(ctx, s.keys.AlertCooldown(a.EndpointID), "1", s.cooldown).Result()
		if err != nil {
			return fmt.Errorf("alert cooldown: %w", err)
		}
		if !first {
			pipe := s.rdb.TxPipeline()
			pipe.Incr(ctx, s.keys.AlertSuppressed(a.EndpointID))
			pipe.Expire(ctx, s.keys.AlertSuppressed(a.EndpointID), 7*24*time.Hour)
			_, err := pipe.Exec(ctx)
			return err
		}
		n, err := s.rdb.GetDel(ctx, s.keys.AlertSuppressed(a.EndpointID)).Int()
		if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("alert suppressed counter: %w", err)
		}
		a.Suppressed = n
	}
	return s.rdb.XAdd(ctx, &redis.XAddArgs{Stream: s.keys.AlertStream(), Values: encode(a)}).Err()
}

func encode(a Alert) map[string]any {
	return map[string]any{
		"log_id":      a.LogID,
		"endpoint_id": a.EndpointID,
		"user_id":     a.UserID,
		"event_type":  a.EventType,
		"attempts":    strconv.Itoa(a.Attempts),
		"last_status": strconv.Itoa(a.LastStatus),
		"last_error":  a.LastError,
		"final":       strconv.FormatBool(a.Final),
		"at":          strconv.FormatInt(a.At.UnixMilli(), 10),
		"suppressed":  strconv.Itoa(a.Suppressed),
	}
}

// ErrMalformed is returned for undecodable alert messages.
var ErrMalformed = errors.New("alert: malformed message")

func decode(v map[string]any) (Alert, error) {
	str := func(k string) string { s, _ := v[k].(string); return s }
	attempts, e1 := strconv.Atoi(str("attempts"))
	status, e2 := strconv.Atoi(str("last_status"))
	final, e3 := strconv.ParseBool(str("final"))
	at, e4 := strconv.ParseInt(str("at"), 10, 64)
	sup, e5 := strconv.Atoi(str("suppressed"))
	a := Alert{
		LogID: str("log_id"), EndpointID: str("endpoint_id"), UserID: str("user_id"),
		EventType: str("event_type"), Attempts: attempts, LastStatus: status, LastError: str("last_error"),
		Final: final, At: time.UnixMilli(at).UTC(), Suppressed: sup,
	}
	if err := errors.Join(e1, e2, e3, e4, e5); err != nil || a.EndpointID == "" {
		return Alert{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return a, nil
}
