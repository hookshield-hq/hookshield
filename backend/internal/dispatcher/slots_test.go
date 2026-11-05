package dispatcher

import (
	"sync"
	"testing"
)

func TestEndpointSlotsCapPerEndpoint(t *testing.T) {
	s := NewEndpointSlots(2)
	if !s.TryAcquire("a") || !s.TryAcquire("a") {
		t.Fatal("first two slots refused")
	}
	if s.TryAcquire("a") {
		t.Fatal("third slot granted")
	}
	if !s.TryAcquire("b") {
		t.Fatal("a busy endpoint must not block another one")
	}
	s.Release("a")
	if !s.TryAcquire("a") {
		t.Fatal("released slot not reusable")
	}
	s.Release("a")
	s.Release("a")
	s.Release("b")
	if len(s.used) != 0 {
		t.Fatalf("idle endpoints kept: %v", s.used)
	}
}

func TestEndpointSlotsConcurrent(t *testing.T) {
	s := NewEndpointSlots(0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	inFlight, peak := 0, 0
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if !s.TryAcquire("x") {
					continue
				}
				mu.Lock()
				inFlight++
				peak = max(peak, inFlight)
				mu.Unlock()
				mu.Lock()
				inFlight--
				mu.Unlock()
				s.Release("x")
			}
		}()
	}
	wg.Wait()
	if peak > DefaultMaxPerEndpoint {
		t.Fatalf("peak %d > %d", peak, DefaultMaxPerEndpoint)
	}
}

func TestEndpointSlotsClaimAttemptOnce(t *testing.T) {
	s := NewEndpointSlots(0)
	if !s.TryClaimAttempt("log#1") || s.TryClaimAttempt("log#1") || !s.TryClaimAttempt("log#2") {
		t.Fatal("an attempt can be claimed once at a time; others are independent")
	}
	s.ReleaseAttempt("log#1")
	if !s.TryClaimAttempt("log#1") {
		t.Fatal("released attempts can be claimed again")
	}
}
