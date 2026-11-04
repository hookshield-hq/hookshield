//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
	"github.com/hookshield-hq/hookshield/backend/internal/uuid"
)

func TestPurgeExpiredLogsAndReplayLink(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, store.CreateUserParams{Email: "p@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := s.CreateEndpoint(ctx, store.CreateEndpointParams{UserID: u.ID, Name: "x", Slug: "purge-test",
		TargetURL: "https://x.example.com", AuthTokenHash: token.Hash("hs_in_x"), AuthTokenPrefix: "hs_in_x"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	insert := func(expires time.Time, replayOf string) string {
		id := uuid.NewV7()
		if _, err := s.InsertPayloadLog(ctx, store.NewPayloadLog{ID: id, EndpointID: ep.ID, HTTPMethod: "POST",
			OriginalEncrypted: []byte{1}, OriginalNonce: []byte{2}, KeyVersion: 1,
			ReceivedAt: now, ExpiresAt: expires, ReplayOf: replayOf}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	old1, old2 := insert(now.Add(-time.Hour), ""), insert(now.Add(-time.Minute), "")
	keep := insert(now.Add(time.Hour), old1)
	ghost := insert(now.Add(time.Hour), uuid.NewV7()) // replay of a log that no longer exists

	if _, err := s.RecordAttempt(ctx, store.AttemptRecord{LogID: old1, AttemptNumber: 1, StartedAt: now, NewStatus: store.LogDelivered, StatusCode: 200}); err != nil {
		t.Fatal(err)
	}

	n, err := s.PurgeExpiredLogs(ctx, now, 1)
	if err != nil || n != 1 {
		t.Fatalf("first batch: %d %v", n, err)
	}
	if n, _ = s.PurgeExpiredLogs(ctx, now, 100); n != 1 {
		t.Fatalf("second batch: %d", n)
	}
	for _, id := range []string{old1, old2} {
		if _, err := s.GetPayloadLog(ctx, id); err != store.ErrNotFound {
			t.Fatalf("expired log %s still there: %v", id, err)
		}
	}
	if a, _ := s.ListDeliveryAttempts(ctx, old1); len(a) != 0 {
		t.Fatal("attempts of purged logs must cascade")
	}
	d, err := s.GetLogDetail(ctx, u.ID, keep)
	if err != nil || d.ReplayOf != nil {
		t.Fatalf("replay link must become NULL when the original is purged: %v %v", d.ReplayOf, err)
	}
	if g, _ := s.GetLogDetail(ctx, u.ID, ghost); g.ReplayOf != nil {
		t.Fatal("replay_of of a missing log must be stored as NULL, not fail")
	}
	if _, err := s.GetLogDetail(ctx, uuid.NewV7(), keep); err != store.ErrNotFound {
		t.Fatalf("logs of other users must be invisible: %v", err)
	}
}
