package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Payload log statuses (payload_logs.status).
const (
	LogQueued    = "queued"
	LogDelivered = "delivered"
	LogRetrying  = "retrying"
	LogFailed    = "failed"
	LogDead      = "dead" // failed and sent to the dead-letter queue
)

// DispatchConfig is the delivery configuration of an endpoint.
type DispatchConfig struct {
	EndpointID             string
	UserID                 string
	PlanType               string
	Status                 string
	TargetURL              string
	OutboundHeaders        map[string]string
	MaxRetries             int
	RetryStrategy          string
	RetryInitialMs         int
	RetryMultiplier        float64
	RetryMaxDelayMs        int
	RetryCustomScheduleMs  []int32
	DLQEnabled             bool
	AlertThresholdFailures int
	AlertWebhookURL        *string
}

// GetDispatchConfig loads the delivery configuration of an endpoint.
func (s *Store) GetDispatchConfig(ctx context.Context, endpointID string) (DispatchConfig, error) {
	var c DispatchConfig
	err := s.pool.QueryRow(ctx, `
		SELECT e.id, e.user_id, u.plan_type, e.status, e.target_url, e.outbound_custom_headers,
		       e.max_retries, e.retry_strategy, e.retry_initial_interval_ms, e.retry_multiplier::float8,
		       e.retry_max_delay_ms, e.retry_custom_schedule_ms, e.dlq_enabled,
		       e.alert_threshold_failures, e.alert_webhook_url
		FROM endpoints e JOIN users u ON u.id = e.user_id
		WHERE e.id = $1`, endpointID,
	).Scan(&c.EndpointID, &c.UserID, &c.PlanType, &c.Status, &c.TargetURL, &c.OutboundHeaders,
		&c.MaxRetries, &c.RetryStrategy, &c.RetryInitialMs, &c.RetryMultiplier,
		&c.RetryMaxDelayMs, &c.RetryCustomScheduleMs, &c.DLQEnabled,
		&c.AlertThresholdFailures, &c.AlertWebhookURL)
	return c, mapErr(err)
}

// DeliveryState is what the dispatcher needs from a stored webhook.
type DeliveryState struct {
	ID                   string
	EndpointID           string
	Status               string
	AttemptCount         int
	EventType            *string
	KeyVersion           int16
	OriginalEncrypted    []byte
	OriginalNonce        []byte
	TransformedEncrypted []byte
	TransformedNonce     []byte
	ReceivedAt           time.Time
}

// Final reports whether no further delivery will happen.
func (d DeliveryState) Final() bool {
	return d.Status == LogDelivered || d.Status == LogFailed || d.Status == LogDead
}

// GetDeliveryState loads a stored webhook for delivery.
func (s *Store) GetDeliveryState(ctx context.Context, logID string) (DeliveryState, error) {
	var d DeliveryState
	err := s.pool.QueryRow(ctx, `
		SELECT id, endpoint_id, status, attempt_count, event_type, key_version,
		       original_encrypted, original_nonce, transformed_encrypted, transformed_nonce, received_at
		FROM payload_logs WHERE id = $1`, logID,
	).Scan(&d.ID, &d.EndpointID, &d.Status, &d.AttemptCount, &d.EventType, &d.KeyVersion,
		&d.OriginalEncrypted, &d.OriginalNonce, &d.TransformedEncrypted, &d.TransformedNonce, &d.ReceivedAt)
	return d, mapErr(err)
}

// AttemptRecord is the outcome of one delivery attempt.
type AttemptRecord struct {
	LogID             string
	AttemptNumber     int
	StartedAt         time.Time
	DurationMicros    int64
	StatusCode        int    // 0 = no HTTP response
	ErrorCode         string // "" on HTTP response
	KeyVersion        int16
	ResponseEncrypted []byte
	ResponseNonce     []byte
	NewStatus         string     // LogDelivered, LogRetrying, LogFailed or LogDead
	NextAttemptAt     *time.Time // set when NewStatus == LogRetrying
	PersistMicros     int64      // processor DB write time, stored with the first attempt
}

// RecordAttempt stores an attempt and updates the log in one transaction.
// It is idempotent: if this attempt number was already recorded (duplicate
// dispatch message) nothing changes and recorded is false.
func (s *Store) RecordAttempt(ctx context.Context, a AttemptRecord) (recorded bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO delivery_attempts (payload_log_id, attempt_number, started_at, duration_us, status_code,
		                               error_code, key_version, response_encrypted, response_nonce)
		VALUES ($1, $2, $3, $4, NULLIF($5, 0), NULLIF($6, ''), $7, $8, $9)
		ON CONFLICT (payload_log_id, attempt_number) DO NOTHING
		RETURNING id`,
		a.LogID, a.AttemptNumber, a.StartedAt, clampInt32(a.DurationMicros), a.StatusCode,
		a.ErrorCode, a.KeyVersion, a.ResponseEncrypted, a.ResponseNonce,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapErr(err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE payload_logs SET
			status           = $2,
			attempt_count    = GREATEST(attempt_count, $3),
			last_status_code = NULLIF($4, 0),
			next_attempt_at  = $5,
			delivered_at     = CASE WHEN $2 = 'delivered' THEN now() ELSE delivered_at END,
			persist_us       = COALESCE(persist_us, NULLIF($6, 0))
		WHERE id = $1`,
		a.LogID, a.NewStatus, a.AttemptNumber, a.StatusCode, a.NextAttemptAt, clampInt32(a.PersistMicros))
	if err != nil {
		return false, mapErr(err)
	}
	return true, mapErr(tx.Commit(ctx))
}

// MarkAlerted sets alerted_at once; it returns true only for the first caller,
// so an alert is never sent twice for the same webhook.
func (s *Store) MarkAlerted(ctx context.Context, logID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE payload_logs SET alerted_at = now() WHERE id = $1 AND alerted_at IS NULL`, logID)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

// OverdueRetry is a log whose scheduled retry was lost (e.g. crash between
// recording an attempt and scheduling the next one).
type OverdueRetry struct {
	LogID       string
	EndpointID  string
	NextAttempt int
}

// ListOverdueRetries returns logs in "retrying" whose next attempt is older than before.
func (s *Store) ListOverdueRetries(ctx context.Context, before time.Time, limit int) ([]OverdueRetry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, endpoint_id, attempt_count + 1
		FROM payload_logs
		WHERE status = 'retrying' AND next_attempt_at < $1
		ORDER BY next_attempt_at
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OverdueRetry
	for rows.Next() {
		var o OverdueRetry
		if err := rows.Scan(&o.LogID, &o.EndpointID, &o.NextAttempt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// TouchRetry pushes next_attempt_at forward after a lost retry was re-scheduled,
// so the sweeper does not pick it up again immediately.
func (s *Store) TouchRetry(ctx context.Context, logID string, next time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE payload_logs SET next_attempt_at = $2 WHERE id = $1 AND status = 'retrying'`, logID, next)
	return mapErr(err)
}

// UserSecret is an encrypted named secret.
type UserSecret struct {
	Name       string
	Encrypted  []byte
	Nonce      []byte
	KeyVersion int16
}

// SecretAAD binds a secret's ciphertext to its owner and name.
func SecretAAD(userID, name string) []byte { return []byte("secret:" + userID + ":" + name) }

// UpsertUserSecret creates or replaces a secret (value already encrypted with SecretAAD).
func (s *Store) UpsertUserSecret(ctx context.Context, userID string, sec UserSecret) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO user_secrets (user_id, name, value_encrypted, value_nonce, key_version)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, name) DO UPDATE
		SET value_encrypted = EXCLUDED.value_encrypted, value_nonce = EXCLUDED.value_nonce, key_version = EXCLUDED.key_version`,
		userID, sec.Name, sec.Encrypted, sec.Nonce, sec.KeyVersion)
	return mapErr(err)
}

// GetUserSecrets returns the requested secrets of a user (missing names are absent from the map).
func (s *Store) GetUserSecrets(ctx context.Context, userID string, names []string) (map[string]UserSecret, error) {
	out := map[string]UserSecret{}
	if len(names) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT name, value_encrypted, value_nonce, key_version
		FROM user_secrets WHERE user_id = $1 AND name = ANY($2)`, userID, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sec UserSecret
		if err := rows.Scan(&sec.Name, &sec.Encrypted, &sec.Nonce, &sec.KeyVersion); err != nil {
			return nil, err
		}
		out[sec.Name] = sec
	}
	return out, rows.Err()
}

// SetEndpointDelivery updates delivery settings (used by tests and, later, the API).
type SetEndpointDeliveryParams struct {
	TargetURL              string
	OutboundHeaders        map[string]string
	MaxRetries             int
	RetryStrategy          string
	RetryInitialMs         int
	RetryMultiplier        float64
	RetryMaxDelayMs        int
	RetryCustomScheduleMs  []int32
	DLQEnabled             bool
	AlertThresholdFailures int
}

// SetEndpointDelivery replaces the delivery settings of an endpoint.
func (s *Store) SetEndpointDelivery(ctx context.Context, endpointID string, p SetEndpointDeliveryParams) error {
	if p.OutboundHeaders == nil {
		p.OutboundHeaders = map[string]string{}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE endpoints SET target_url = $2, outbound_custom_headers = $3, max_retries = $4,
		       retry_strategy = $5, retry_initial_interval_ms = $6, retry_multiplier = $7,
		       retry_max_delay_ms = $8, retry_custom_schedule_ms = $9, dlq_enabled = $10,
		       alert_threshold_failures = $11
		WHERE id = $1`,
		endpointID, p.TargetURL, p.OutboundHeaders, p.MaxRetries, p.RetryStrategy, p.RetryInitialMs,
		p.RetryMultiplier, p.RetryMaxDelayMs, p.RetryCustomScheduleMs, p.DLQEnabled, p.AlertThresholdFailures)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeliveryAttempt is a stored attempt (for the debugger timeline and tests).
type DeliveryAttempt struct {
	AttemptNumber     int
	StartedAt         time.Time
	StatusCode        *int32
	ErrorCode         *string
	DurationMicros    *int32
	ResponseEncrypted []byte
	ResponseNonce     []byte
	KeyVersion        int16
}

// ListDeliveryAttempts returns the attempts of a log in order.
func (s *Store) ListDeliveryAttempts(ctx context.Context, logID string) ([]DeliveryAttempt, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT attempt_number, started_at, status_code, error_code, duration_us, response_encrypted, response_nonce, key_version
		FROM delivery_attempts WHERE payload_log_id = $1 ORDER BY attempt_number`, logID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeliveryAttempt
	for rows.Next() {
		var a DeliveryAttempt
		if err := rows.Scan(&a.AttemptNumber, &a.StartedAt, &a.StatusCode, &a.ErrorCode, &a.DurationMicros,
			&a.ResponseEncrypted, &a.ResponseNonce, &a.KeyVersion); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
