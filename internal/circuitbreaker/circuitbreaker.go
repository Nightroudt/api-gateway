// Package circuitbreaker implements a minimal CLOSED -> OPEN -> HALF_OPEN
// circuit breaker from scratch (not sony/gobreaker), specifically to
// demonstrate Go's concurrency primitives (mutex-guarded state machine)
// rather than just wiring up a library.
package circuitbreaker

import (
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "CLOSED"
	case Open:
		return "OPEN"
	case HalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// Breaker is safe for concurrent use by multiple goroutines.
//
// Every state transition bumps generation. Allow returns the generation a
// request was admitted under; RecordSuccess/RecordFailure must be given
// that same value back, and discard the result if the breaker has since
// moved to a different generation. Without this, a slow request admitted
// while CLOSED could resolve after the breaker had already opened and
// moved through a HALF_OPEN trial, and its stale RecordSuccess/Failure call
// would corrupt whatever state the breaker had reached in the meantime.
type Breaker struct {
	mu sync.Mutex

	failureThreshold int
	openTimeout      time.Duration

	state            State
	generation       uint64
	consecutiveFails int
	openedAt         time.Time
	halfOpenInFlight bool
}

func New(failureThreshold int, openTimeout time.Duration) *Breaker {
	return &Breaker{
		failureThreshold: failureThreshold,
		openTimeout:      openTimeout,
		state:            Closed,
	}
}

// Allow reports whether the caller should proceed with the request right
// now, and returns the generation to pass back to RecordSuccess/
// RecordFailure. It also performs the OPEN -> HALF_OPEN transition once the
// cooldown has elapsed, letting through exactly one trial request at a time
// while half-open (concurrent callers racing this transition are
// serialized by the mutex, so only the first one gets "true").
func (b *Breaker) Allow() (allowed bool, generation uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Closed:
		return true, b.generation
	case Open:
		if time.Since(b.openedAt) < b.openTimeout {
			return false, b.generation
		}
		b.transitionTo(HalfOpen)
		b.halfOpenInFlight = true
		return true, b.generation
	case HalfOpen:
		if b.halfOpenInFlight {
			return false, b.generation
		}
		b.halfOpenInFlight = true
		return true, b.generation
	default:
		return false, b.generation
	}
}

// RecordSuccess closes the breaker and resets the failure count, unless
// generation is stale (the breaker has moved on since this request was
// admitted), in which case the result is discarded.
func (b *Breaker) RecordSuccess(generation uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if generation != b.generation {
		return
	}
	b.consecutiveFails = 0
	b.halfOpenInFlight = false
	b.transitionTo(Closed)
}

// RecordFailure counts a failed request, unless generation is stale. In
// HALF_OPEN, a single failure re-opens the breaker immediately (the trial
// didn't pan out); in CLOSED, the breaker opens once failureThreshold
// consecutive failures accumulate.
func (b *Breaker) RecordFailure(generation uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if generation != b.generation {
		return
	}
	b.halfOpenInFlight = false

	if b.state == HalfOpen {
		b.transitionTo(Open)
		b.openedAt = time.Now()
		return
	}

	b.consecutiveFails++
	if b.consecutiveFails >= b.failureThreshold {
		b.transitionTo(Open)
		b.openedAt = time.Now()
	}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// transitionTo changes state and bumps the generation, invalidating any
// request admitted under the previous generation. A no-op transition (same
// state) does not bump the generation, since nothing about the current
// attempt round has actually changed.
func (b *Breaker) transitionTo(s State) {
	if b.state == s {
		return
	}
	b.state = s
	b.generation++
}
