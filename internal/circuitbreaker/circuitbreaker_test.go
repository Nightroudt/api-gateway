package circuitbreaker

import (
	"sync"
	"testing"
	"time"
)

func TestClosedAllowsRequests(t *testing.T) {
	b := New(3, 50*time.Millisecond)
	if allowed, _ := b.Allow(); !allowed {
		t.Fatal("expected CLOSED breaker to allow requests")
	}
	if b.State() != Closed {
		t.Fatalf("expected CLOSED, got %s", b.State())
	}
}

func TestOpensAfterThresholdConsecutiveFailures(t *testing.T) {
	b := New(3, time.Hour)
	for i := 0; i < 2; i++ {
		_, gen := b.Allow()
		b.RecordFailure(gen)
	}
	if b.State() != Closed {
		t.Fatalf("expected still CLOSED after 2/3 failures, got %s", b.State())
	}

	_, gen := b.Allow()
	b.RecordFailure(gen)
	if b.State() != Open {
		t.Fatalf("expected OPEN after 3 consecutive failures, got %s", b.State())
	}
	if allowed, _ := b.Allow(); allowed {
		t.Fatal("expected OPEN breaker to reject requests before cooldown elapses")
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	b := New(3, time.Hour)
	_, gen := b.Allow()
	b.RecordFailure(gen)
	_, gen = b.Allow()
	b.RecordFailure(gen)
	_, gen = b.Allow()
	b.RecordSuccess(gen) // resets the streak

	_, gen = b.Allow()
	b.RecordFailure(gen)
	_, gen = b.Allow()
	b.RecordFailure(gen)
	if b.State() != Closed {
		t.Fatalf("expected CLOSED (streak was reset by the success), got %s", b.State())
	}
}

func TestHalfOpenAfterCooldownThenCloses(t *testing.T) {
	b := New(1, 20*time.Millisecond)
	_, gen := b.Allow()
	b.RecordFailure(gen)
	if b.State() != Open {
		t.Fatalf("expected OPEN, got %s", b.State())
	}

	time.Sleep(30 * time.Millisecond)

	allowed, gen := b.Allow()
	if !allowed {
		t.Fatal("expected one trial request to be allowed once cooldown elapsed")
	}
	if b.State() != HalfOpen {
		t.Fatalf("expected HALF_OPEN, got %s", b.State())
	}

	b.RecordSuccess(gen)
	if b.State() != Closed {
		t.Fatalf("expected CLOSED after a successful trial, got %s", b.State())
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	b := New(1, 20*time.Millisecond)
	_, gen := b.Allow()
	b.RecordFailure(gen)
	time.Sleep(30 * time.Millisecond)

	_, gen = b.Allow() // enters HALF_OPEN
	b.RecordFailure(gen)

	if b.State() != Open {
		t.Fatalf("expected OPEN after a failed trial, got %s", b.State())
	}
}

func TestHalfOpenOnlyLetsOneTrialThrough(t *testing.T) {
	b := New(1, 20*time.Millisecond)
	_, gen := b.Allow()
	b.RecordFailure(gen)
	time.Sleep(30 * time.Millisecond)

	const goroutines = 50
	var allowedCount int
	var mu sync.Mutex
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if allowed, _ := b.Allow(); allowed {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowedCount != 1 {
		t.Fatalf("expected exactly 1 trial request to be let through concurrently, got %d", allowedCount)
	}
}

func TestConcurrentFailuresDoNotRace(t *testing.T) {
	b := New(1000, time.Hour)
	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, gen := b.Allow()
			b.RecordFailure(gen)
		}()
	}
	wg.Wait()

	// Run with `go test -race` to catch data races; this assertion just
	// confirms every failure was actually counted (no lost updates).
	b.mu.Lock()
	got := b.consecutiveFails
	b.mu.Unlock()
	if got != goroutines {
		t.Fatalf("expected %d consecutive failures counted, got %d", goroutines, got)
	}
}

func TestStaleGenerationResultIsIgnored(t *testing.T) {
	// Simulates a slow request: admitted while CLOSED, but doesn't resolve
	// until after the breaker has since opened and moved into a HALF_OPEN
	// trial. Its stale success/failure must not corrupt that later state.
	b := New(1, 20*time.Millisecond)

	_, staleGen := b.Allow() // admitted while CLOSED, generation 0

	// A different request fails, opening the breaker (new generation).
	_, gen := b.Allow()
	b.RecordFailure(gen)
	if b.State() != Open {
		t.Fatalf("expected OPEN, got %s", b.State())
	}

	time.Sleep(30 * time.Millisecond)
	_, halfOpenGen := b.Allow() // the one live HALF_OPEN trial
	if b.State() != HalfOpen {
		t.Fatalf("expected HALF_OPEN, got %s", b.State())
	}

	// The slow request from CLOSED finally "completes" and reports success
	// — but it's stale (wrong generation) and must be ignored.
	b.RecordSuccess(staleGen)
	if b.State() != HalfOpen {
		t.Fatalf("stale success must not close the breaker mid-trial, got %s", b.State())
	}

	// The real trial then fails, and that must still correctly reopen it.
	b.RecordFailure(halfOpenGen)
	if b.State() != Open {
		t.Fatalf("expected OPEN after the actual trial failed, got %s", b.State())
	}
}
