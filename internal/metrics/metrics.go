// Package metrics exposes Prometheus metrics for the gateway — a direct nod
// to the DevOps tooling (Prometheus itself is written in Go) this project's
// positioning is aimed at.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Nightroudt/api-gateway/internal/httprecorder"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_requests_total",
		Help: "Total requests proxied by the gateway, labeled by backend and status code.",
	}, []string{"backend", "status"})

	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gateway_request_duration_seconds",
		Help:    "Request duration in seconds, labeled by backend.",
		Buckets: prometheus.DefBuckets,
	}, []string{"backend"})
)

// Middleware records request count and latency for a single named backend.
func Middleware(backend string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := httprecorder.Wrap(w)

		next.ServeHTTP(rec, r)

		RequestsTotal.WithLabelValues(backend, strconv.Itoa(rec.Status)).Inc()
		RequestDuration.WithLabelValues(backend).Observe(time.Since(start).Seconds())
	})
}

// Handler serves metrics in the Prometheus text exposition format.
func Handler() http.Handler {
	return promhttp.Handler()
}
