// Package ratelimit provides a per-client token-bucket rate limiter
// middleware built on golang.org/x/time/rate (the standard extended-library
// implementation, not a third-party framework).
package ratelimit

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// Limiter tracks one token bucket per client key. Buckets are created
// lazily and never evicted — acceptable for a portfolio-scale demo, but a
// production version would need to expire idle entries to bound memory.
type Limiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

func New(rps float64, burst int) *Limiter {
	return &Limiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
}

func (l *Limiter) getLimiter(key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	lim, ok := l.limiters[key]
	if !ok {
		lim = rate.NewLimiter(l.rps, l.burst)
		l.limiters[key] = lim
	}
	return lim
}

// Allow reports whether a request from the given key may proceed right now.
func (l *Limiter) Allow(key string) bool {
	return l.getLimiter(key).Allow()
}

// Middleware rejects requests over the limit with 429, keyed by client IP.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(ClientKey(r)) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientKey identifies the caller by IP address only. An earlier version of
// this also preferred an X-API-Key header when present, but that key is
// entirely client-supplied and unverified by this gateway — a caller could
// bypass rate limiting for free by sending a different key (or none) on
// every request, since getLimiter hands out a fresh bucket with a full
// burst to every never-seen key. Keying by a client-controlled value only
// makes sense once the gateway actually authenticates that key against a
// known set; until then, IP is the only identifier the caller doesn't get
// to choose.
func ClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
