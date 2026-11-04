// Package redisx opens the Redis client and centralises every Redis key
// HookShield uses, so key names are documented and namespaced in one place.
package redisx

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultPrefix namespaces every HookShield key in a shared Redis.
const DefaultPrefix = "hs:"

// Open parses a redis:// or rediss:// URL, connects and pings.
func Open(ctx context.Context, url string) (*redis.Client, error) {
	return OpenFor(ctx, url, 0)
}

// OpenFor is Open for a process with blockingReaders goroutines that wait on
// XREADGROUP BLOCK. Each holds a pooled connection while it waits, so the pool
// gets that many connections on top of go-redis' default (10 per CPU):
// otherwise idle dispatchers can hold every connection and starve the
// processors that would feed them (seen under load on a 2-CPU host).
// A pool_size in the URL wins.
func OpenFor(ctx context.Context, url string, blockingReaders int) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	if blockingReaders > 0 && !strings.Contains(url, "pool_size=") {
		opts.PoolSize = PoolSize(blockingReaders)
	}
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	return client, nil
}

// PoolSize is the connection pool of a process with blockingReaders blocking readers.
func PoolSize(blockingReaders int) int { return 10*runtime.GOMAXPROCS(0) + blockingReaders }

// AlertEmailLimitPerHour caps the alert emails one account can send.
const AlertEmailLimitPerHour = 100

// AllowAlertEmail counts an alert email of userID in the current hour and
// reports whether it is within AlertEmailLimitPerHour (Redis errors allow it).
func AllowAlertEmail(ctx context.Context, rdb *redis.Client, keys Keys, userID string, now time.Time) bool {
	k := keys.prefix + "alert:emails:" + userID + ":" + now.UTC().Format("2006010215")
	pipe := rdb.TxPipeline()
	n := pipe.Incr(ctx, k)
	pipe.Expire(ctx, k, 2*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		return true
	}
	return n.Val() <= AlertEmailLimitPerHour
}

// ReadyCheck returns a readiness probe that pings Redis.
func ReadyCheck(c *redis.Client) func(context.Context) error {
	return func(ctx context.Context) error { return c.Ping(ctx).Err() }
}

// Keys builds namespaced key names. The zero value is not usable; use NewKeys.
type Keys struct{ prefix string }

// NewKeys returns a key builder; an empty prefix means DefaultPrefix.
func NewKeys(prefix string) Keys {
	if prefix == "" {
		prefix = DefaultPrefix
	}
	return Keys{prefix: prefix}
}

// Prefix returns the namespace prefix.
func (k Keys) Prefix() string { return k.prefix }

// EndpointCache holds the cached ingest config of an endpoint (string, JSON).
func (k Keys) EndpointCache(slug string) string { return k.prefix + "cache:ep:" + slug }

// EndpointCacheGen counts the invalidations of an endpoint's cache entry
// (string, integer): a loader only writes the entry back if it is unchanged.
func (k Keys) EndpointCacheGen(slug string) string { return k.prefix + "cache:epgen:" + slug }

// IngestStream is the processing queue fed by the ingest service (stream).
func (k Keys) LiveChannel(userID string) string { return k.prefix + "live:" + userID }

// IngestStream is the processing queue.
func (k Keys) IngestStream() string { return k.prefix + "stream:ingest" }

// DispatchStream is the delivery queue fed by the processor (stream).
func (k Keys) DispatchStream() string { return k.prefix + "stream:dispatch" }

// RetrySchedule holds scheduled retries; score = next attempt (unix ms) (sorted set).
func (k Keys) RetrySchedule() string { return k.prefix + "zset:retry" }

// DeadLetterStream receives deliveries that exhausted their retries (stream).
func (k Keys) DeadLetterStream() string { return k.prefix + "stream:dlq" }

// AlertStream holds alerts waiting to be sent to their channels (stream).
func (k Keys) AlertStream() string { return k.prefix + "stream:alerts" }

// AlertCooldown marks that an endpoint was alerted recently (string with TTL).
func (k Keys) AlertCooldown(endpointID string) string {
	return k.prefix + "alert:cooldown:" + endpointID
}

// AlertSuppressed counts alerts skipped during an endpoint's cooldown (string counter).
func (k Keys) AlertSuppressed(endpointID string) string {
	return k.prefix + "alert:suppressed:" + endpointID
}

// AuthFailures counts failed logins for a subject ("email:<sha256>" or "ip:<addr>") (string counter, TTL window).
func (k Keys) AuthFailures(subject string) string { return k.prefix + "auth:fail:" + subject }

// AuthMailCooldown throttles account emails per purpose and recipient (string with TTL).
func (k Keys) AuthMailCooldown(purpose, subject string) string {
	return k.prefix + "auth:mail:" + purpose + ":" + subject
}

// OAuthState holds a pending OAuth login (PKCE verifier + redirect), TTL 10 min (string, JSON).
func (k Keys) OAuthState(state string) string { return k.prefix + "oauth:" + state }

// JobLock makes a periodic job run on one worker at a time.
func (k Keys) JobLock(name string) string { return k.prefix + "lock:job:" + name }

// UsagePattern matches every MonthlyUsage key (SCAN).
func (k Keys) UsagePattern() string { return k.prefix + "usage:*" }

// ParseUsageKey splits a MonthlyUsage key into user ID and period.
func (k Keys) ParseUsageKey(key string) (userID, period string, ok bool) {
	rest, found := strings.CutPrefix(key, k.prefix+"usage:")
	if !found {
		return "", "", false
	}
	i := strings.LastIndexByte(rest, ':')
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// MonthlyUsage counts accepted requests of a user in a month, e.g. period "202611" (string counter).
func (k Keys) MonthlyUsage(userID, period string) string {
	return k.prefix + "usage:" + userID + ":" + period
}

// RateWindow counts requests of an endpoint within one second (string counter).
func (k Keys) RateWindow(endpointID string, unixSecond int64) string {
	return fmt.Sprintf("%srl:%s:%d", k.prefix, endpointID, unixSecond)
}
