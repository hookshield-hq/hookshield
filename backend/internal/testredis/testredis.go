// Package testredis gives integration tests a Redis client plus a unique key
// namespace, so tests never collide with each other or with development data.
//
// Set HS_TEST_REDIS_URL (e.g. redis://localhost:6379/15) and run with
// `-tags integration`; without it the tests are skipped. All keys created under
// the namespace are deleted when the test ends.
package testredis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
)

// EnvVar is the environment variable holding the test Redis URL.
const EnvVar = "HS_TEST_REDIS_URL"

// New returns a connected client and a fresh, isolated key namespace.
func New(t testing.TB) (*redis.Client, redisx.Keys) {
	t.Helper()
	url := os.Getenv(EnvVar)
	if url == "" {
		t.Skipf("%s not set; skipping integration test", EnvVar)
	}
	client, err := redisx.Open(context.Background(), url)
	if err != nil {
		t.Fatalf("open redis: %v", err)
	}

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	keys := redisx.NewKeys("hstest:" + hex.EncodeToString(b) + ":")

	t.Cleanup(func() {
		ctx := context.Background()
		iter := client.Scan(ctx, 0, keys.Prefix()+"*", 500).Iterator()
		for iter.Next(ctx) {
			client.Del(ctx, iter.Val())
		}
		_ = client.Close()
	})
	return client, keys
}
