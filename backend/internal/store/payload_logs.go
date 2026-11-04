package store

import (
	"context"
	"time"
)

// ProcessConfig is what the processor needs to handle a message of an endpoint.
type ProcessConfig struct {
	EndpointID     string
	UserID         string
	PlanType       string
	TransformRules []byte // raw JSONB
}

// GetProcessConfig loads the processing configuration of an endpoint.
func (s *Store) GetProcessConfig(ctx context.Context, endpointID string) (ProcessConfig, error) {
	var c ProcessConfig
	err := s.pool.QueryRow(ctx, `
		SELECT e.id, e.user_id, u.plan_type, e.transform_rules
		FROM endpoints e JOIN users u ON u.id = e.user_id
		WHERE e.id = $1`, endpointID,
	).Scan(&c.EndpointID, &c.UserID, &c.PlanType, &c.TransformRules)
	return c, mapErr(err)
}

// SetEndpointTransformRules replaces the transform rules of an endpoint.
// The caller must validate them first (transform.Parse).
func (s *Store) SetEndpointTransformRules(ctx context.Context, endpointID string, rules []byte) error {
	tag, err := s.pool.Exec(ctx, `UPDATE endpoints SET transform_rules = $2 WHERE id = $1`, endpointID, rules)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// NewPayloadLog is a processed webhook ready to be stored.
type NewPayloadLog struct {
	ID                   string
	EndpointID           string
	HTTPMethod           string
	EventType            string
	ContentType          string
	SizeBytes            int
	SourceIP             string // "" = unknown
	KeyVersion           int16
	OriginalEncrypted    []byte
	OriginalNonce        []byte
	TransformedEncrypted []byte
	TransformedNonce     []byte
	IngestMicros         int64
	TransformMicros      int64
	ReceivedAt           time.Time
	ExpiresAt            time.Time
	ReplayOf             string // "" = not a replay
}

// InsertPayloadLog stores a processed webhook with status "queued".
// It is idempotent on ID: re-processing a redelivered message returns
// inserted=false instead of failing, which makes the pipeline at-least-once safe.
func (s *Store) InsertPayloadLog(ctx context.Context, p NewPayloadLog) (inserted bool, err error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO payload_logs (
			id, endpoint_id, http_method, event_type, content_type, size_bytes, source_ip,
			key_version, original_encrypted, original_nonce, transformed_encrypted, transformed_nonce,
			status, ingest_us, transform_us, received_at, processed_at, expires_at, replay_of)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, NULLIF($7, '')::inet,
		        $8, $9, $10, $11, $12,
		        'queued', $13, $14, $15, now(), $16,
		        (SELECT id FROM payload_logs WHERE id = NULLIF($17, '')::uuid))
		ON CONFLICT (id) DO NOTHING`,
		p.ID, p.EndpointID, p.HTTPMethod, p.EventType, p.ContentType, p.SizeBytes, p.SourceIP,
		p.KeyVersion, p.OriginalEncrypted, p.OriginalNonce, p.TransformedEncrypted, p.TransformedNonce,
		clampInt32(p.IngestMicros), clampInt32(p.TransformMicros), p.ReceivedAt, p.ExpiresAt, p.ReplayOf,
	)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

// PayloadLog is a stored webhook (ciphertexts included; decrypt with crypto.Cipher).
type PayloadLog struct {
	ID                   string
	EndpointID           string
	HTTPMethod           string
	EventType            *string
	ContentType          *string
	SizeBytes            int
	SourceIP             *string
	Status               string
	AttemptCount         int
	KeyVersion           int16
	OriginalEncrypted    []byte
	OriginalNonce        []byte
	TransformedEncrypted []byte
	TransformedNonce     []byte
	IngestMicros         *int32
	TransformMicros      *int32
	PersistMicros        *int32
	ReceivedAt           time.Time
	ProcessedAt          *time.Time
	ExpiresAt            time.Time
}

// GetPayloadLog returns one stored webhook.
func (s *Store) GetPayloadLog(ctx context.Context, id string) (PayloadLog, error) {
	var l PayloadLog
	err := s.pool.QueryRow(ctx, `
		SELECT id, endpoint_id, http_method, event_type, content_type, size_bytes, host(source_ip),
		       status, attempt_count, key_version, original_encrypted, original_nonce,
		       transformed_encrypted, transformed_nonce, ingest_us, transform_us, persist_us,
		       received_at, processed_at, expires_at
		FROM payload_logs WHERE id = $1`, id,
	).Scan(&l.ID, &l.EndpointID, &l.HTTPMethod, &l.EventType, &l.ContentType, &l.SizeBytes, &l.SourceIP,
		&l.Status, &l.AttemptCount, &l.KeyVersion, &l.OriginalEncrypted, &l.OriginalNonce,
		&l.TransformedEncrypted, &l.TransformedNonce, &l.IngestMicros, &l.TransformMicros, &l.PersistMicros,
		&l.ReceivedAt, &l.ProcessedAt, &l.ExpiresAt)
	return l, mapErr(err)
}

func clampInt32(v int64) int64 {
	const max = 1<<31 - 1
	switch {
	case v < 0:
		return 0
	case v > max:
		return max
	}
	return v
}
