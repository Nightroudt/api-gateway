// Command gateway is the API Gateway entrypoint: wires up routing, rate
// limiting, circuit breakers, health checks, metrics and graceful shutdown.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Nightroudt/api-gateway/internal/circuitbreaker"
	"github.com/Nightroudt/api-gateway/internal/config"
	"github.com/Nightroudt/api-gateway/internal/healthcheck"
	"github.com/Nightroudt/api-gateway/internal/metrics"
	"github.com/Nightroudt/api-gateway/internal/middleware"
	"github.com/Nightroudt/api-gateway/internal/proxy"
	"github.com/Nightroudt/api-gateway/internal/ratelimit"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()

	tasksBreaker := circuitbreaker.New(cfg.CircuitFailThreshold, cfg.CircuitOpenTimeout)
	salesBreaker := circuitbreaker.New(cfg.CircuitFailThreshold, cfg.CircuitOpenTimeout)

	tasksBackend, err := proxy.NewBackend("tasks", cfg.BackendTasksURL, tasksBreaker)
	if err != nil {
		logger.Error("invalid BACKEND_TASKS_URL", "error", err)
		os.Exit(1)
	}
	salesBackend, err := proxy.NewBackend("sales", cfg.BackendSalesURL, salesBreaker)
	if err != nil {
		logger.Error("invalid BACKEND_SALES_URL", "error", err)
		os.Exit(1)
	}

	tasksHealthURL, err := url.JoinPath(cfg.BackendTasksURL, "health")
	if err != nil {
		logger.Error("invalid BACKEND_TASKS_URL", "error", err)
		os.Exit(1)
	}
	salesHealthURL, err := url.JoinPath(cfg.BackendSalesURL, "health")
	if err != nil {
		logger.Error("invalid BACKEND_SALES_URL", "error", err)
		os.Exit(1)
	}

	checker := healthcheck.New(map[string]string{
		"tasks": tasksHealthURL,
		"sales": salesHealthURL,
	}, cfg.HealthCheckInterval, cfg.HealthCheckTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go checker.Run(ctx)

	limiter := ratelimit.New(cfg.RateLimitRPS, cfg.RateLimitBurst)

	router := proxy.NewRouter([]proxy.Route{
		{PathPrefix: "/api/tasks/", Handler: metrics.Middleware("tasks", tasksBackend)},
		{PathPrefix: "/api/sales/", Handler: metrics.Middleware("sales", salesBackend)},
	})

	// Rate limiting applies only to proxied API traffic — /health and
	// /metrics are operational endpoints (load balancer probes, Prometheus
	// scrapes) that must not compete with API callers for the same budget.
	mux := http.NewServeMux()
	mux.Handle("/api/", limiter.Middleware(router))
	mux.HandleFunc("/health", healthHandler(checker))
	mux.Handle("/metrics", metrics.Handler())

	var handler http.Handler = mux
	handler = middleware.Logging(logger)(handler)
	handler = middleware.RequestID(handler)
	handler = middleware.Recover(logger)(handler)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		logger.Info("gateway starting", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received, draining connections")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

func healthHandler(checker *healthcheck.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		statuses := checker.Snapshot()

		allHealthy := len(statuses) > 0
		for _, s := range statuses {
			if !s.Healthy {
				allHealthy = false
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if !allHealthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"gateway":  "ok",
			"backends": statuses,
		})
	}
}
