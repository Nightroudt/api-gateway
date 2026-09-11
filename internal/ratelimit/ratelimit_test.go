package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowsWithinBurstThenRejects(t *testing.T) {
	l := New(1, 3) // 1 req/s refill, burst of 3

	for i := 0; i < 3; i++ {
		if !l.Allow("client-a") {
			t.Fatalf("expected request %d within burst to be allowed", i+1)
		}
	}
	if l.Allow("client-a") {
		t.Fatal("expected 4th immediate request to exceed the burst")
	}
}

func TestClientsAreIsolated(t *testing.T) {
	l := New(1, 1)

	if !l.Allow("client-a") {
		t.Fatal("expected client-a's first request to be allowed")
	}
	if l.Allow("client-a") {
		t.Fatal("expected client-a's second immediate request to be rejected")
	}
	if !l.Allow("client-b") {
		t.Fatal("expected client-b to have its own independent bucket")
	}
}

func TestMiddlewareReturns429WhenExceeded(t *testing.T) {
	l := New(1, 1)
	handler := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5555"

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected first request to pass, got %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec2.Code)
	}
}

func TestClientKeyIsIPRegardlessOfHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5555"
	// A client-supplied header must not override the key: it's unverified
	// by this gateway, so honoring it would let anyone bypass the limit by
	// sending a different value on every request.
	req.Header.Set("X-API-Key", "abc123")

	if got := ClientKey(req); got != "1.2.3.4" {
		t.Fatalf("expected client key to be the IP without port, got %q", got)
	}
}

func TestClientKeyFallsBackToRemoteAddrIfUnparsable(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "not-a-valid-host-port"

	if got := ClientKey(req); got != "not-a-valid-host-port" {
		t.Fatalf("expected the raw RemoteAddr as a fallback, got %q", got)
	}
}
