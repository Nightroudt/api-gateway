// Package middleware provides cross-cutting HTTP middleware: request IDs,
// structured request logging (log/slog), and panic recovery.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Nightroudt/api-gateway/internal/httprecorder"
)

type contextKey string

const requestIDKey contextKey = "requestID"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

var fallbackRequestIDSeq atomic.Uint64

// newRequestID generates a random 8-byte hex ID. On the extremely rare
// chance the OS entropy source is unavailable, it falls back to a
// monotonic counter (logged) rather than silently returning an all-zero ID
// that would make every request in that window collide in the logs.
func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		slog.Error("crypto/rand unavailable, falling back to a counter-based request ID", "error", err)
		return fmt.Sprintf("fallback-%d", fallbackRequestIDSeq.Add(1))
	}
	return hex.EncodeToString(b)
}

// Logging returns middleware that logs one structured line per request.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := httprecorder.Wrap(w)

			next.ServeHTTP(rec, r)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.Status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFromContext(r.Context()),
			)
		})
	}
}

// Recover converts a panic in any downstream handler into a 500 response
// instead of crashing the whole gateway process.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered", "error", err, "path", r.URL.Path)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
