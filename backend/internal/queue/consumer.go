package queue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// DispatchMessage asks the dispatcher to deliver a stored webhook.
// It carries only identifiers: the payload is read (encrypted) from PostgreSQL.
type DispatchMessage struct {
	LogID         string // payload_logs.id
	EndpointID    string
	Attempt       int   // 1 for the first delivery
	PersistMicros int64 // database write time measured by the processor
}

func (m DispatchMessage) values() map[string]any {
	return map[string]any{
		"log_id":      m.LogID,
		"endpoint_id": m.EndpointID,
		"attempt":     strconv.Itoa(m.Attempt),
		"persist_us":  strconv.FormatInt(m.PersistMicros, 10),
	}
}

// ParseDispatchMessage decodes the fields of a dispatch stream entry.
func ParseDispatchMessage(v map[string]any) (DispatchMessage, error) {
	str := func(k string) string { s, _ := v[k].(string); return s }
	attempt, err1 := strconv.Atoi(str("attempt"))
	persist, err2 := strconv.ParseInt(str("persist_us"), 10, 64)
	m := DispatchMessage{LogID: str("log_id"), EndpointID: str("endpoint_id"), Attempt: attempt, PersistMicros: persist}
	if err := errors.Join(err1, err2); err != nil || m.LogID == "" || m.EndpointID == "" || m.Attempt < 1 {
		return DispatchMessage{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return m, nil
}

// PublishDispatch appends m to the dispatch stream.
func (p *Producer) PublishDispatch(ctx context.Context, m DispatchMessage) (string, error) {
	return p.rdb.XAdd(ctx, &redis.XAddArgs{Stream: p.keys.DispatchStream(), Values: m.values()}).Result()
}

// DeadLetterMaxLen caps the dead-letter stream (approximately). Only poison
// messages land there (transient failures never do), so the cap only bounds
// memory if something keeps producing undecodable entries.
const DeadLetterMaxLen = 100_000

// DeadLetterMaxAge is how long a dead-lettered message is kept: never longer
// than the shortest retention any account can have (24 hours on HookShield
// Cloud's free plan), since it still holds the sealed payload.
const DeadLetterMaxAge = 24 * time.Hour

// TrimDeadLetters deletes dead-lettered messages older than DeadLetterMaxAge
// (stream IDs carry their insertion time).
func (p *Producer) TrimDeadLetters(ctx context.Context, now time.Time) (int64, error) {
	minID := fmt.Sprintf("%d-0", now.Add(-DeadLetterMaxAge).UnixMilli())
	return p.rdb.XTrimMinID(ctx, p.keys.DeadLetterStream(), minID).Result()
}

// DeadLetter records a message that can never be processed (malformed,
// undecryptable, crashing its consumer). The original stream fields are kept
// unchanged next to the metadata (source, entry_id, reason, at) so the entry
// can be inspected and replayed once the cause is fixed: XADD the original
// fields back to the source stream. Payload fields are the sealed envelope
// exactly as it was in the source stream: nothing is decrypted here, so no
// plaintext reaches Redis. On a name clash the metadata wins.
func (p *Producer) DeadLetter(ctx context.Context, source, entryID, reason string, fields map[string]any) error {
	values := make(map[string]any, len(fields)+4)
	for k, v := range fields {
		values[k] = v
	}
	values["source"] = source
	values["entry_id"] = entryID
	values["reason"] = reason
	values["at"] = time.Now().UTC().Format(time.RFC3339)
	return p.rdb.XAdd(ctx, &redis.XAddArgs{Stream: p.keys.DeadLetterStream(), MaxLen: DeadLetterMaxLen, Approx: true, Values: values}).Err()
}

// Consumer reads a stream as a member of a consumer group (at-least-once).
type Consumer struct {
	rdb    *redis.Client
	stream string
	group  string
	name   string
}

// NewConsumer returns a consumer; name must be unique per running goroutine.
func NewConsumer(rdb *redis.Client, stream, group, name string) *Consumer {
	return &Consumer{rdb: rdb, stream: stream, group: group, name: name}
}

// EnsureGroup creates the consumer group (and the stream) if needed.
// New groups start at the beginning of the stream so nothing queued is skipped.
func (c *Consumer) EnsureGroup(ctx context.Context) error {
	err := c.rdb.XGroupCreateMkStream(ctx, c.stream, c.group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create consumer group %s/%s: %w", c.stream, c.group, err)
	}
	return nil
}

// Read returns up to count new messages, waiting at most block.
func (c *Consumer) Read(ctx context.Context, count int64, block time.Duration) ([]redis.XMessage, error) {
	res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: c.group, Consumer: c.name, Streams: []string{c.stream, ">"}, Count: count, Block: block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []redis.XMessage
	for _, s := range res {
		out = append(out, s.Messages...)
	}
	return out, nil
}

// Claim takes over messages that another consumer read but did not acknowledge
// for at least minIdle (e.g. it crashed). Returns up to count messages.
func (c *Consumer) Claim(ctx context.Context, minIdle time.Duration, count int64) ([]redis.XMessage, error) {
	msgs, _, err := c.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: c.stream, Group: c.group, Consumer: c.name, MinIdle: minIdle, Start: "0-0", Count: count,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return msgs, err
}

// MaxDeliveries is how many times a message may be handed to a consumer
// (first read + reclaims) before it is treated as poison.
const MaxDeliveries = 5

// Deliveries returns how many times the pending message id was delivered.
func (c *Consumer) Deliveries(ctx context.Context, id string) (int64, error) {
	ps, err := c.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: c.stream, Group: c.group, Start: id, End: id, Count: 1}).Result()
	if err != nil {
		return 0, err
	}
	if len(ps) == 0 {
		return 0, nil
	}
	return ps[0].RetryCount, nil
}

// Postpone keeps entries pending for a later reclaim (after the claim idle
// time) and resets their delivery counter. Use it when handling failed for a
// transient reason (database or Redis unavailable): such a failure says
// nothing about the message itself, so it must not count toward
// MaxDeliveries — only deliveries that never came back (crashes) do.
func (c *Consumer) Postpone(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	// XCLAIM with min-idle 0 re-assigns the entries to this consumer, sets the
	// idle time to 0 (so the next reclaim waits a full ClaimMinIdle: a natural
	// backoff) and RETRYCOUNT 0 resets the counter; JUSTID does not increment it.
	args := []any{"XCLAIM", c.stream, c.group, c.name, 0}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, "RETRYCOUNT", 0, "JUSTID")
	return c.rdb.Do(ctx, args...).Err()
}

// Touch resets the idle time of entries this consumer is still working on, so
// that no other consumer reclaims them (XAUTOCLAIM only takes entries idle for
// at least its min-idle) while they are in progress. The delivery counter is
// not incremented (JUSTID). Acknowledged entries are ignored.
func (c *Consumer) Touch(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{"XCLAIM", c.stream, c.group, c.name, 0}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, "JUSTID")
	return c.rdb.Do(ctx, args...).Err()
}

// Done acknowledges and deletes processed entries, keeping the stream small.
func (c *Consumer) Done(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	pipe := c.rdb.TxPipeline()
	pipe.XAck(ctx, c.stream, c.group, ids...)
	pipe.XDel(ctx, c.stream, ids...)
	_, err := pipe.Exec(ctx)
	return err
}
