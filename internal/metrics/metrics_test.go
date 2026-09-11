package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMiddlewareRecordsRequestCount(t *testing.T) {
	RequestsTotal.Reset()

	handler := Middleware("tasks-test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))

	got := testutil.ToFloat64(RequestsTotal.WithLabelValues("tasks-test", "201"))
	if got != 1 {
		t.Fatalf("expected 1 recorded request, got %v", got)
	}
}

func TestHandlerServesPrometheusFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from metrics handler, got %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected non-empty metrics body")
	}
}
