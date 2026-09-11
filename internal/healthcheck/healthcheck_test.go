package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckOneRecordsHealthyStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	c := New(map[string]string{"svc": upstream.URL}, time.Hour, time.Second)
	c.checkOne(context.Background(), "svc", upstream.URL)

	got := c.Snapshot()["svc"]
	if !got.Healthy {
		t.Fatalf("expected healthy status, got %+v", got)
	}
}

func TestCheckOneRecordsUnhealthyOnNon200(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	c := New(map[string]string{"svc": upstream.URL}, time.Hour, time.Second)
	c.checkOne(context.Background(), "svc", upstream.URL)

	got := c.Snapshot()["svc"]
	if got.Healthy {
		t.Fatal("expected unhealthy status for a 503 response")
	}
	if got.Error == "" {
		t.Fatal("expected an error message describing the bad status")
	}
}

func TestCheckOneRecordsUnhealthyOnUnreachable(t *testing.T) {
	c := New(map[string]string{"svc": "http://127.0.0.1:1"}, time.Hour, 200*time.Millisecond)
	c.checkOne(context.Background(), "svc", "http://127.0.0.1:1")

	got := c.Snapshot()["svc"]
	if got.Healthy {
		t.Fatal("expected unhealthy status for an unreachable backend")
	}
}

func TestRunProbesPeriodicallyUntilCancelled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	c := New(map[string]string{"svc": upstream.URL}, 10*time.Millisecond, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	c.Run(ctx) // blocks until ctx is done; immediate check + a couple of ticks

	got := c.Snapshot()["svc"]
	if !got.Healthy {
		t.Fatalf("expected healthy snapshot after Run finished, got %+v", got)
	}
}

func TestSnapshotIsIndependentCopy(t *testing.T) {
	c := New(map[string]string{"svc": "http://example.invalid"}, time.Hour, time.Second)
	c.mu.Lock()
	c.statuses["svc"] = Status{Healthy: true}
	c.mu.Unlock()

	snap := c.Snapshot()
	snap["svc"] = Status{Healthy: false}

	if got := c.Snapshot()["svc"]; !got.Healthy {
		t.Fatal("mutating the returned snapshot must not affect internal state")
	}
}
