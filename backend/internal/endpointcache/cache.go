// Package endpointcache keeps the ingest configuration of endpoints in Redis
// so the ingest hot path normally costs one Redis GET instead of a SQL query.
//
//   - Hits are served from Redis (TTL, default 10 min).
//   - Unknown slugs are cached as a negative marker (default 30 s), so floods
//     against random slugs never reach PostgreSQL.
//   - Concurrent misses for the same slug are collapsed into one database
//     query (singleflight).
//   - The control-plane API calls Invalidate whenever an endpoint changes
//     (token rotation, pause, auth mode), so changes apply immediately.
//     Invalidate also bumps a per-slug generation counter, and a miss only
//     writes what it loaded if the generation did not change meanwhile: a
//     lookup that read the database just before an update can never put the
//     old configuration back after the Invalidate.
//
// Only hashes and ciphertexts are cached — never plaintext secrets.
package endpointcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
)

// negativeMarker is stored for slugs that do not exist.
const negativeMarker = "!"

// genTTL keeps the generation counter far longer than any load can take; it
// only exists so counters of deleted endpoints do not stay forever.
const genTTL = 24 * time.Hour

// setIfGen writes KEYS[1] = ARGV[2] (TTL ARGV[3] ms) only if the generation
// KEYS[2] still equals ARGV[1] (a missing counter is generation "0").
var setIfGen = redis.NewScript(`
local g = redis.call('GET', KEYS[2]) or '0'
if g ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1`)

// slugRE mirrors the CHECK constraint on endpoints.slug.
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,99}$`)

// Loader fetches the configuration from the source of truth.
type Loader interface {
	GetIngestConfig(ctx context.Context, slug string) (store.IngestConfig, error)
}

// Cache is a read-through Redis cache of store.IngestConfig.
type Cache struct {
	rdb         *redis.Client
	keys        redisx.Keys
	loader      Loader
	ttl         time.Duration
	negativeTTL time.Duration
	group       singleflight.Group
}

// Option customises a Cache.
type Option func(*Cache)

// WithTTL sets how long a found configuration stays cached.
func WithTTL(d time.Duration) Option { return func(c *Cache) { c.ttl = d } }

// WithNegativeTTL sets how long an unknown slug stays cached as "not found".
func WithNegativeTTL(d time.Duration) Option { return func(c *Cache) { c.negativeTTL = d } }

// New builds a cache.
func New(rdb *redis.Client, keys redisx.Keys, loader Loader, opts ...Option) *Cache {
	c := &Cache{rdb: rdb, keys: keys, loader: loader, ttl: 10 * time.Minute, negativeTTL: 30 * time.Second}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Lookup returns the configuration for slug, or store.ErrNotFound.
func (c *Cache) Lookup(ctx context.Context, slug string) (store.IngestConfig, error) {
	if !slugRE.MatchString(slug) {
		return store.IngestConfig{}, store.ErrNotFound
	}
	key := c.keys.EndpointCache(slug)

	raw, err := c.rdb.Get(ctx, key).Bytes()
	switch {
	case err == nil:
		if string(raw) == negativeMarker {
			return store.IngestConfig{}, store.ErrNotFound
		}
		var cfg store.IngestConfig
		if err := json.Unmarshal(raw, &cfg); err == nil {
			return cfg, nil
		}
		// Corrupt or outdated entry: fall through and reload it.
	case !errors.Is(err, redis.Nil):
		return store.IngestConfig{}, fmt.Errorf("endpoint cache get: %w", err)
	}

	v, err, _ := c.group.Do(slug, func() (any, error) {
		// Read the generation before the database: if an Invalidate happens
		// while we load, the write-back below is skipped (the next lookup
		// loads the new configuration).
		genKey := c.keys.EndpointCacheGen(slug)
		gen, err := c.rdb.Get(ctx, genKey).Result()
		if errors.Is(err, redis.Nil) {
			gen = "0"
		} else if err != nil {
			return nil, fmt.Errorf("endpoint cache generation: %w", err)
		}
		save := func(val any, ttl time.Duration) {
			_ = setIfGen.Run(ctx, c.rdb, []string{key, genKey}, gen, val, ttl.Milliseconds()).Err()
		}
		cfg, err := c.loader.GetIngestConfig(ctx, slug)
		if errors.Is(err, store.ErrNotFound) {
			save(negativeMarker, c.negativeTTL)
			return nil, store.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if b, err := json.Marshal(cfg); err == nil {
			save(b, c.ttl)
		}
		return cfg, nil
	})
	if err != nil {
		return store.IngestConfig{}, err
	}
	return v.(store.IngestConfig), nil
}

// Invalidate removes the cached configuration of slug and bumps its
// generation, so a lookup that loaded the old configuration before this call
// does not cache it again.
func (c *Cache) Invalidate(ctx context.Context, slug string) error {
	genKey := c.keys.EndpointCacheGen(slug)
	pipe := c.rdb.TxPipeline()
	pipe.Incr(ctx, genKey)
	pipe.Expire(ctx, genKey, genTTL)
	pipe.Del(ctx, c.keys.EndpointCache(slug))
	_, err := pipe.Exec(ctx)
	return err
}
