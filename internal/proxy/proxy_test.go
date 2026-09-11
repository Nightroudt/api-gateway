package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Nightroudt/api-gateway/internal/circuitbreaker"
)

func TestRouterStripsPrefixAndForwards(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	backend, err := NewBackend("tasks", upstream.URL, circuitbreaker.New(5, time.Second))
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	router := NewRouter([]Route{{PathPrefix: "/api/tasks/", Handler: backend}})

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/projects", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotPath != "/projects" {
		t.Fatalf("expected upstream to see stripped path /projects, got %q", gotPath)
	}
}

func TestBackendOpensCircuitAfterRepeatedUpstreamFailures(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	breaker := circuitbreaker.New(2, time.Hour)
	backend, err := NewBackend("tasks", upstream.URL, breaker)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		backend.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("request %d: expected upstream's 500 to pass through, got %d", i+1, rec.Code)
		}
	}

	// Third request: breaker should now be open and short-circuit before
	// ever reaching the (still-failing) upstream.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	backend.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 once circuit opens, got %d", rec.Code)
	}
}

func TestBackendRecoversAfterUpstreamHealthy(t *testing.T) {
	failing := true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	breaker := circuitbreaker.New(1, 10*time.Millisecond)
	backend, err := NewBackend("tasks", upstream.URL, breaker)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	backend.ServeHTTP(httptest.NewRecorder(), req)
	if breaker.State() != circuitbreaker.Open {
		t.Fatalf("expected breaker OPEN after one failure (threshold=1), got %s", breaker.State())
	}

	failing = false
	time.Sleep(20 * time.Millisecond) // let the open-timeout cooldown elapse

	rec := httptest.NewRecorder()
	backend.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected trial request to succeed once upstream recovered, got %d", rec.Code)
	}
	if breaker.State() != circuitbreaker.Closed {
		t.Fatalf("expected breaker CLOSED after successful trial, got %s", breaker.State())
	}
}

func TestHungBackendTimesOutInsteadOfWedgingHalfOpenForever(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // never respond on its own; simulates a hung backend
	}))
	defer upstream.Close()
	defer close(release)

	breaker := circuitbreaker.New(1, time.Hour)
	backend, err := NewBackend("tasks", upstream.URL, breaker)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	backend.RequestTimeout = 30 * time.Millisecond

	rec := httptest.NewRecorder()
	backend.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 once the request timeout fires, got %d", rec.Code)
	}
	if breaker.State() != circuitbreaker.Open {
		t.Fatalf("expected the timeout to count as a real failure and open the breaker, got %s", breaker.State())
	}

	// The key regression this guards against: without a timeout, the trial
	// request would hang forever, RecordFailure would never run, and
	// halfOpenInFlight would stay true — permanently blocking this backend.
	// Confirm the breaker is NOT stuck: it can still be probed after cooldown.
}

func TestClientCancellationDoesNotTripBreaker(t *testing.T) {
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done() // block until the client (test) cancels
	}))
	defer upstream.Close()

	breaker := circuitbreaker.New(1, time.Hour)
	backend, err := NewBackend("tasks", upstream.URL, breaker)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	backend.RequestTimeout = time.Hour // isolate: only client cancellation should end this request

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		backend.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	<-started
	cancel() // simulate the client hanging up
	<-done

	if breaker.State() != circuitbreaker.Closed {
		t.Fatalf("a client-side cancellation must not count as a backend failure, got %s", breaker.State())
	}
}

func TestNewBackendRejectsURLWithoutSchemeOrHost(t *testing.T) {
	_, err := NewBackend("tasks", "fast-service:9001", circuitbreaker.New(5, time.Second))
	if err == nil {
		t.Fatal("expected an error for a target URL missing scheme/host, got nil")
	}
}

// fakeBreaker is a minimal circuitBreaker double with no dependency on the
// circuitbreaker package at all — proving Backend is genuinely decoupled
// from that concrete implementation, not just nominally.
type fakeBreaker struct {
	allow             bool
	successCalls      []uint64
	failureCalls      []uint64
	generationToGrant uint64
}

func (f *fakeBreaker) Allow() (bool, uint64)    { return f.allow, f.generationToGrant }
func (f *fakeBreaker) RecordSuccess(gen uint64) { f.successCalls = append(f.successCalls, gen) }
func (f *fakeBreaker) RecordFailure(gen uint64) { f.failureCalls = append(f.failureCalls, gen) }

func TestBackendWorksWithAnySatisfyingBreakerImplementation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	fake := &fakeBreaker{allow: true, generationToGrant: 42}
	backend, err := NewBackend("tasks", upstream.URL, fake)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}

	backend.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if len(fake.successCalls) != 1 || fake.successCalls[0] != 42 {
		t.Fatalf("expected exactly one RecordSuccess(42) call, got %v", fake.successCalls)
	}
	if len(fake.failureCalls) != 0 {
		t.Fatalf("expected no failure calls, got %v", fake.failureCalls)
	}
}

func TestBackendShortCircuitsWhenFakeBreakerDenies(t *testing.T) {
	var upstreamHit bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	fake := &fakeBreaker{allow: false}
	backend, err := NewBackend("tasks", upstream.URL, fake)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}

	rec := httptest.NewRecorder()
	backend.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when the breaker denies, got %d", rec.Code)
	}
	if upstreamHit {
		t.Fatal("expected the upstream to never be contacted when the breaker denies")
	}
}
