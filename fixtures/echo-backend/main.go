// Command echo-backend is a minimal stand-in service used by the demo
// docker-compose and for manually exercising the gateway's circuit breaker
// (via the /admin/fail toggle) without needing the real task-tracker-api or
// sales-data-pipeline running. Not part of the gateway itself.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync/atomic"
)

func main() {
	name := os.Getenv("SERVICE_NAME")
	if name == "" {
		name = "echo-backend"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}

	var failing atomic.Bool

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Toggle failure mode on/off, so the live demo can show the gateway's
	// circuit breaker opening and later recovering without restarting anything.
	mux.HandleFunc("/admin/fail", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			failing.Store(true)
		case http.MethodDelete:
			failing.Store(false)
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"failing": failing.Load()})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": name + " is in forced-failure mode"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"service": name,
			"method":  r.Method,
			"path":    r.URL.Path,
		})
	})

	log.Printf("%s listening on :%s", name, port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
