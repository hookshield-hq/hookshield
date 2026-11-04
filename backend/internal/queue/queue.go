// Package queue defines the messages exchanged between HookShield services
// through Redis Streams, and the producers/consumers that move them.
//
// Payloads travel encrypted: the ingest service seals each request into an
// Envelope with AES-256-GCM before XADD, so webhook bodies are never written
// to Redis (or its AOF/RDB files) in plain text.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
)

// Envelope is the plaintext form of a received webhook (encrypted in transit and at rest).
type Envelope struct {
	Method  string              `json:"method"`
	Query   string              `json:"query,omitempty"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"` // base64 in JSON
}

// Marshal encodes the envelope as JSON.
func (e Envelope) Marshal() ([]byte, error) { return json.Marshal(e) }

// UnmarshalEnvelope decodes an envelope produced by Marshal.
func UnmarshalEnvelope(b []byte) (Envelope, error) {
	var e Envelope
	err := json.Unmarshal(b, &e)
	return e, err
}

// IngestAAD is the additional authenticated data binding an ingest envelope to its request ID.
func IngestAAD(requestID string) []byte { return []byte("ingest:" + requestID) }

// IngestMessage is one accepted webhook waiting for processing.
type IngestMessage struct {
	ID           string // request ID (UUIDv7), becomes payload_logs.id
	EndpointID   string
	UserID       string
	KeyVersion   int16
	Ciphertext   []byte // sealed Envelope
	Nonce        []byte
	ReceivedAt   time.Time
	IngestMicros int64 // time spent in the ingest service
	SourceIP     string
	SizeBytes    int
	ContentType  string
	Method       string
	ReplayOf     string // payload_logs.id this request replays ("" = a new webhook)
}

func (m IngestMessage) values() map[string]any {
	return map[string]any{
		"id":          m.ID,
		"endpoint_id": m.EndpointID,
		"user_id":     m.UserID,
		"key_version": strconv.Itoa(int(m.KeyVersion)),
		"ciphertext":  m.Ciphertext,
		"nonce":       m.Nonce,
		"received_at": strconv.FormatInt(m.ReceivedAt.UnixMicro(), 10),
		"ingest_us":   strconv.FormatInt(m.IngestMicros, 10),
		"source_ip":   m.SourceIP,
		"size":        strconv.Itoa(m.SizeBytes),
		"ctype":       m.ContentType,
		"method":      m.Method,
		"replay_of":   m.ReplayOf,
	}
}

// ErrMalformed is returned for stream entries that cannot be decoded.
var ErrMalformed = errors.New("queue: malformed message")

// ParseIngestMessage decodes the fields of a stream entry.
func ParseIngestMessage(v map[string]any) (IngestMessage, error) {
	str := func(k string) string { s, _ := v[k].(string); return s }
	num := func(k string) (int64, error) { return strconv.ParseInt(str(k), 10, 64) }

	m := IngestMessage{
		ID:          str("id"),
		EndpointID:  str("endpoint_id"),
		UserID:      str("user_id"),
		Ciphertext:  []byte(str("ciphertext")),
		Nonce:       []byte(str("nonce")),
		SourceIP:    str("source_ip"),
		ContentType: str("ctype"),
		Method:      str("method"),
		ReplayOf:    str("replay_of"),
	}
	kv, err1 := num("key_version")
	recv, err2 := num("received_at")
	ing, err3 := num("ingest_us")
	size, err4 := num("size")
	if err := errors.Join(err1, err2, err3, err4); err != nil || m.ID == "" || m.EndpointID == "" || len(m.Nonce) == 0 {
		return IngestMessage{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	m.KeyVersion = int16(kv)
	m.ReceivedAt = time.UnixMicro(recv).UTC()
	m.IngestMicros = ing
	m.SizeBytes = int(size)
	return m, nil
}

// Producer publishes messages to the streams.
type Producer struct {
	rdb  *redis.Client
	keys redisx.Keys
}

// NewProducer returns a Producer.
func NewProducer(rdb *redis.Client, keys redisx.Keys) *Producer {
	return &Producer{rdb: rdb, keys: keys}
}

// PublishIngest appends m to the ingest stream and returns the stream entry ID.
// Entries are deleted by the processor after they are persisted, so the stream
// is not trimmed here (trimming could drop unprocessed webhooks).
func (p *Producer) PublishIngest(ctx context.Context, m IngestMessage) (string, error) {
	return p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: p.keys.IngestStream(),
		Values: m.values(),
	}).Result()
}
