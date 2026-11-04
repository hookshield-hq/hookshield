package store

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Queries behind the logs, metrics and search screens (Phase 6c). Every
// query joins endpoints on user_id, so logs of other accounts are invisible.

// LogFilter selects logs. Zero values mean "no filter".
type LogFilter struct {
	EndpointID string
	Statuses   []string
	EventType  string
	Query      string // log ID prefix or event type substring
	From, To   *time.Time
	Limit      int
	// Keyset cursor: logs strictly older than (BeforeTime, BeforeID).
	BeforeTime *time.Time
	BeforeID   string
}

// LogSummary is one row of the logs table (no payload data).
type LogSummary struct {
	ID             string
	EndpointID     string
	EndpointName   string
	EndpointSlug   string
	ReplayOf       *string
	HTTPMethod     string
	EventType      *string
	ContentType    *string
	SizeBytes      int
	SourceIP       *string
	Status         string
	AttemptCount   int
	LastStatusCode *int32
	NextAttemptAt  *time.Time
	IngestMicros   *int32
	TransformMs    *int32
	PersistMicros  *int32
	ReceivedAt     time.Time
	DeliveredAt    *time.Time
	ExpiresAt      time.Time
}

const logSummaryColumns = `l.id, l.endpoint_id, e.name, e.slug, l.replay_of, l.http_method, l.event_type, l.content_type,
	l.size_bytes, host(l.source_ip), l.status, l.attempt_count, l.last_status_code, l.next_attempt_at,
	l.ingest_us, l.transform_us, l.persist_us, l.received_at, l.delivered_at, l.expires_at`

func scanLogSummary(r rowScanner) (LogSummary, error) {
	var l LogSummary
	err := r.Scan(&l.ID, &l.EndpointID, &l.EndpointName, &l.EndpointSlug, &l.ReplayOf, &l.HTTPMethod, &l.EventType,
		&l.ContentType, &l.SizeBytes, &l.SourceIP, &l.Status, &l.AttemptCount, &l.LastStatusCode, &l.NextAttemptAt,
		&l.IngestMicros, &l.TransformMs, &l.PersistMicros, &l.ReceivedAt, &l.DeliveredAt, &l.ExpiresAt)
	return l, mapErr(err)
}

// likeEscape escapes LIKE wildcards in user input.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListLogs returns logs of userID, newest first.
func (s *Store) ListLogs(ctx context.Context, userID string, f LogFilter) ([]LogSummary, error) {
	where := []string{"e.user_id = $1"}
	args := []any{userID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if f.EndpointID != "" {
		add("l.endpoint_id = ?::uuid", f.EndpointID)
	}
	if len(f.Statuses) > 0 {
		add("l.status = ANY(?)", f.Statuses)
	}
	if f.EventType != "" {
		add("l.event_type = ?", f.EventType)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		// Both ? refer to the same argument.
		add(`(l.id::text LIKE ? || '%' OR l.event_type ILIKE '%' || ? || '%')`, likeEscape(strings.ToLower(q)))
	}
	if f.From != nil {
		add("l.received_at >= ?", *f.From)
	}
	if f.To != nil {
		add("l.received_at < ?", *f.To)
	}
	if f.BeforeTime != nil && f.BeforeID != "" {
		args = append(args, *f.BeforeTime, f.BeforeID)
		where = append(where, "(l.received_at, l.id) < ($"+itoa(len(args)-1)+", $"+itoa(len(args))+"::uuid)")
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `
		SELECT `+logSummaryColumns+`
		FROM payload_logs l JOIN endpoints e ON e.id = l.endpoint_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY l.received_at DESC, l.id DESC
		LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogSummary{}
	for rows.Next() {
		l, err := scanLogSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func itoa(n int) string { return strconv.Itoa(n) }

// LogDetail is a log with its encrypted envelopes, for the debugger.
type LogDetail struct {
	LogSummary
	KeyVersion           int16
	OriginalEncrypted    []byte
	OriginalNonce        []byte
	TransformedEncrypted []byte
	TransformedNonce     []byte
	ProcessedAt          *time.Time
	UserID               string
}

// GetLogDetail returns a log of userID.
func (s *Store) GetLogDetail(ctx context.Context, userID, id string) (LogDetail, error) {
	var d LogDetail
	l := &d.LogSummary
	err := s.pool.QueryRow(ctx, `
		SELECT `+logSummaryColumns+`, l.key_version, l.original_encrypted, l.original_nonce,
		       l.transformed_encrypted, l.transformed_nonce, l.processed_at, e.user_id
		FROM payload_logs l JOIN endpoints e ON e.id = l.endpoint_id
		WHERE l.id = $1 AND e.user_id = $2`, id, userID).
		Scan(&l.ID, &l.EndpointID, &l.EndpointName, &l.EndpointSlug, &l.ReplayOf, &l.HTTPMethod, &l.EventType,
			&l.ContentType, &l.SizeBytes, &l.SourceIP, &l.Status, &l.AttemptCount, &l.LastStatusCode, &l.NextAttemptAt,
			&l.IngestMicros, &l.TransformMs, &l.PersistMicros, &l.ReceivedAt, &l.DeliveredAt, &l.ExpiresAt,
			&d.KeyVersion, &d.OriginalEncrypted, &d.OriginalNonce, &d.TransformedEncrypted, &d.TransformedNonce,
			&d.ProcessedAt, &d.UserID)
	return d, mapErr(err)
}

// ListReplays returns the IDs of the replays of a log, oldest first.
func (s *Store) ListReplays(ctx context.Context, logID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM payload_logs WHERE replay_of = $1 ORDER BY received_at, id`, logID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MetricsSummary aggregates logs received since a point in time.
type MetricsSummary struct {
	Received     int64
	Delivered    int64
	Failed       int64 // failed + dead
	InFlight     int64 // queued + processing + retrying
	Retrying     int64 // currently waiting for a retry (any age)
	AvgGatewayUs float64
	P95GatewayUs float64
	AvgDeliverUs float64
}

// GetMetricsSummary aggregates userID's logs since since (optionally one endpoint).
func (s *Store) GetMetricsSummary(ctx context.Context, userID, endpointID string, since time.Time) (MetricsSummary, error) {
	var m MetricsSummary
	err := s.pool.QueryRow(ctx, `
		WITH l AS (
			SELECT l.* FROM payload_logs l JOIN endpoints e ON e.id = l.endpoint_id
			WHERE e.user_id = $1 AND ($2 = '' OR l.endpoint_id = NULLIF($2, '')::uuid)
		)
		SELECT
			count(*) FILTER (WHERE received_at >= $3),
			count(*) FILTER (WHERE received_at >= $3 AND status = 'delivered'),
			count(*) FILTER (WHERE received_at >= $3 AND status IN ('failed', 'dead')),
			count(*) FILTER (WHERE received_at >= $3 AND status IN ('queued', 'processing', 'retrying')),
			count(*) FILTER (WHERE status = 'retrying'),
			coalesce(avg(coalesce(ingest_us, 0) + coalesce(transform_us, 0) + coalesce(persist_us, 0))
				FILTER (WHERE received_at >= $3 AND ingest_us IS NOT NULL), 0),
			coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY coalesce(ingest_us, 0) + coalesce(transform_us, 0) + coalesce(persist_us, 0))
				FILTER (WHERE received_at >= $3 AND ingest_us IS NOT NULL), 0),
			coalesce((SELECT avg(a.duration_us) FROM delivery_attempts a JOIN l ON l.id = a.payload_log_id
				WHERE a.started_at >= $3), 0)
		FROM l`, userID, endpointID, since).
		Scan(&m.Received, &m.Delivered, &m.Failed, &m.InFlight, &m.Retrying, &m.AvgGatewayUs, &m.P95GatewayUs, &m.AvgDeliverUs)
	return m, err
}

// MetricsPoint is one time bucket of the traffic chart.
type MetricsPoint struct {
	Bucket       time.Time
	Received     int64
	Delivered    int64
	Failed       int64
	AvgGatewayUs float64
}

// GetMetricsSeries returns traffic per bucket since since, empty buckets included.
func (s *Store) GetMetricsSeries(ctx context.Context, userID, endpointID string, since time.Time, bucket time.Duration) ([]MetricsPoint, error) {
	rows, err := s.pool.Query(ctx, `
		WITH buckets AS (
			SELECT generate_series(date_bin($4::interval, $3::timestamptz, 'epoch'::timestamptz), now(), $4::interval) AS b
		), l AS (
			SELECT date_bin($4::interval, l.received_at, 'epoch'::timestamptz) AS b, l.status,
			       coalesce(l.ingest_us, 0) + coalesce(l.transform_us, 0) + coalesce(l.persist_us, 0) AS gw,
			       l.ingest_us IS NOT NULL AS has_gw
			FROM payload_logs l JOIN endpoints e ON e.id = l.endpoint_id
			WHERE e.user_id = $1 AND ($2 = '' OR l.endpoint_id = NULLIF($2, '')::uuid) AND l.received_at >= $3
		)
		SELECT buckets.b, count(l.b), count(l.b) FILTER (WHERE l.status = 'delivered'),
		       count(l.b) FILTER (WHERE l.status IN ('failed', 'dead')),
		       coalesce(avg(l.gw) FILTER (WHERE l.has_gw), 0)
		FROM buckets LEFT JOIN l ON l.b = buckets.b
		GROUP BY buckets.b ORDER BY buckets.b`, userID, endpointID, since, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricsPoint{}
	for rows.Next() {
		var p MetricsPoint
		if err := rows.Scan(&p.Bucket, &p.Received, &p.Delivered, &p.Failed, &p.AvgGatewayUs); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// EndpointStats are the per-endpoint numbers of the endpoints list.
type EndpointStats struct {
	EndpointID   string
	Received     int64
	Delivered    int64
	Failed       int64
	LastReceived *time.Time
}

// GetEndpointStats returns stats since since for every endpoint of userID (zero rows included).
func (s *Store) GetEndpointStats(ctx context.Context, userID string, since time.Time) ([]EndpointStats, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id,
		       count(l.id) FILTER (WHERE l.received_at >= $2),
		       count(l.id) FILTER (WHERE l.received_at >= $2 AND l.status = 'delivered'),
		       count(l.id) FILTER (WHERE l.received_at >= $2 AND l.status IN ('failed', 'dead')),
		       max(l.received_at)
		FROM endpoints e LEFT JOIN payload_logs l ON l.endpoint_id = e.id
		WHERE e.user_id = $1
		GROUP BY e.id ORDER BY e.id`, userID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EndpointStats{}
	for rows.Next() {
		var st EndpointStats
		if err := rows.Scan(&st.EndpointID, &st.Received, &st.Delivered, &st.Failed, &st.LastReceived); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SearchResults groups global search hits (⌘K).
type SearchResults struct {
	Endpoints []EndpointRecord
	Logs      []LogSummary
	Secrets   []SecretInfo
}

// Search finds endpoints (name, slug, target), logs (ID prefix, event type) and
// secrets (name) of userID. At most perKind hits per kind.
func (s *Store) Search(ctx context.Context, userID, q string, perKind int) (SearchResults, error) {
	var out SearchResults
	pat := "%" + likeEscape(q) + "%"
	rows, err := s.pool.Query(ctx, `SELECT `+endpointColumns+` FROM endpoints
		WHERE user_id = $1 AND (name ILIKE $2 OR slug ILIKE $2 OR target_url ILIKE $2)
		ORDER BY created_at DESC LIMIT $3`, userID, pat, perKind)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Endpoints = append(out.Endpoints, e)
	}
	rows.Close()

	out.Logs, err = s.ListLogs(ctx, userID, LogFilter{Query: q, Limit: perKind})
	if err != nil {
		return out, err
	}

	rows, err = s.pool.Query(ctx, `SELECT name, created_at, updated_at FROM user_secrets
		WHERE user_id = $1 AND name ILIKE $2 ORDER BY name LIMIT $3`, userID, pat, perKind)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var i SecretInfo
		if err := rows.Scan(&i.Name, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return out, err
		}
		out.Secrets = append(out.Secrets, i)
	}
	return out, rows.Err()
}

// PurgeExpiredLogs deletes up to limit logs past their retention deadline
// (their attempts cascade) and returns how many were deleted. Safe to run
// from several workers at once (SKIP LOCKED).
func (s *Store) PurgeExpiredLogs(ctx context.Context, now time.Time, limit int) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM payload_logs WHERE id IN (
			SELECT id FROM payload_logs WHERE expires_at < $1
			ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED)`, now, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
