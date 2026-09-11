package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDIsSetAndAccessible(t *testing.T) {
	var seenInHandler string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenInHandler = RequestIDFromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if seenInHandler == "" {
		t.Fatal("expected a non-empty request ID inside the handler")
	}
	if rec.Header().Get("X-Request-ID") != seenInHandler {
		t.Fatal("expected the response header to match the context value")
	}
}

func TestTwoRequestsGetDifferentIDs(t *testing.T) {
	var ids []string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, RequestIDFromContext(r.Context()))
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if ids[0] == ids[1] {
		t.Fatal("expected two different requests to get different IDs")
	}
}

func TestRecoverConvertsPanicTo500(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	handler := Recover(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 after recovering from panic, got %d", rec.Code)
	}
}

func TestRecoverDoesNothingWhenNoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	handler := Recover(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected the handler's own status to pass through, got %d", rec.Code)
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
