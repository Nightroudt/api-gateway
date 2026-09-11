// Package proxy implements path-based routing to backend services via the
// standard library's net/http/httputil.ReverseProxy, with a circuit breaker
// gating each backend.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/Nightroudt/api-gateway/internal/circuitbreaker"
)

type ctxKey int

const generationCtxKey ctxKey = 0

// DefaultRequestTimeout bounds how long a single proxied request may take.
// Without this, a backend that accepts a connection but never responds
// would hang the request indefinitely — RecordSuccess/RecordFailure would
// never be called, leaving a HALF_OPEN trial's halfOpenInFlight flag stuck
// forever and permanently blocking that backend even after it recovers.
const DefaultRequestTimeout = 10 * time.Second

// Backend proxies to a single upstream service, short-circuiting requests
// with 503 while its circuit breaker is open instead of hammering a
// downstream that has already signaled trouble.
type Backend struct {
	Name           string
	Target         *url.URL
	Breaker        *circuitbreaker.Breaker
	RequestTimeout time.Duration
	proxy          *httputil.ReverseProxy
}

func NewBackend(name, targetURL string, breaker *circuitbreaker.Breaker) (*Backend, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("backend %q: target URL %q must include a scheme and host (e.g. http://host:port)", name, targetURL)
	}

	rp := httputil.NewSingleHostReverseProxy(target)

	b := &Backend{Name: name, Target: target, Breaker: breaker, RequestTimeout: DefaultRequestTimeout}

	rp.ModifyResponse = func(resp *http.Response) error {
		generation, _ := resp.Request.Context().Value(generationCtxKey).(uint64)
		if resp.StatusCode >= http.StatusInternalServerError {
			breaker.RecordFailure(generation)
		} else {
			breaker.RecordSuccess(generation)
		}
		return nil
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		generation, _ := r.Context().Value(generationCtxKey).(uint64)

		// A client hanging up or our own per-request timeout firing isn't a
		// signal that the backend itself is unhealthy — don't let a burst of
		// client-side cancellations trip the breaker for a backend that
		// never actually failed.
		if !errors.Is(err, context.Canceled) {
			breaker.RecordFailure(generation)
		}

		// Don't leak internal topology (hostnames/ports/dial errors) from
		// err.Error() to the client — log it server-side instead.
		slog.Warn("backend request failed", "backend", name, "error", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}

	b.proxy = rp
	return b, nil
}

func (b *Backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	allowed, generation := b.Breaker.Allow()
	if !allowed {
		http.Error(w, "service unavailable: circuit open for backend "+b.Name, http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), b.RequestTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, generationCtxKey, generation)

	b.proxy.ServeHTTP(w, r.WithContext(ctx))
}

// Route maps a URL path prefix to a handler; the prefix is stripped before
// the request reaches Handler, so "/api/tasks/projects" is forwarded
// upstream as "/projects".
type Route struct {
	PathPrefix string
	Handler    http.Handler
}

func NewRouter(routes []Route) http.Handler {
	mux := http.NewServeMux()
	for _, route := range routes {
		trimmed := strings.TrimSuffix(route.PathPrefix, "/")
		mux.Handle(route.PathPrefix, http.StripPrefix(trimmed, route.Handler))
	}
	return mux
}
