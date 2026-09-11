// Package config loads gateway settings from environment variables
// (12-factor style), with sane defaults for local/demo use.
package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port                 string
	BackendTasksURL      string
	BackendSalesURL      string
	RateLimitRPS         float64
	RateLimitBurst       int
	CircuitFailThreshold int
	CircuitOpenTimeout   time.Duration
	HealthCheckInterval  time.Duration
	HealthCheckTimeout   time.Duration
	ShutdownTimeout      time.Duration
}

func Load() Config {
	return Config{
		Port: getEnv("PORT", "8080"),
		// Defaults point at the lightweight demo stand-ins from
		// fixtures/echo-backend (see docker-compose.yml). Point these at the
		// real task-tracker-api / sales-data-pipeline containers instead —
		// see README "Wiring up the real portfolio services".
		BackendTasksURL:      getEnv("BACKEND_TASKS_URL", "http://fast-service:9001"),
		BackendSalesURL:      getEnv("BACKEND_SALES_URL", "http://heavy-service:9002"),
		RateLimitRPS:         getEnvFloat("RATE_LIMIT_RPS", 10),
		RateLimitBurst:       getEnvInt("RATE_LIMIT_BURST", 20),
		CircuitFailThreshold: getEnvInt("CIRCUIT_FAIL_THRESHOLD", 5),
		CircuitOpenTimeout:   getEnvDuration("CIRCUIT_OPEN_TIMEOUT", 10*time.Second),
		HealthCheckInterval:  getEnvDuration("HEALTH_CHECK_INTERVAL", 5*time.Second),
		HealthCheckTimeout:   getEnvDuration("HEALTH_CHECK_TIMEOUT", 2*time.Second),
		ShutdownTimeout:      getEnvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
	}
}

func getEnv(key, fallback string) string {
	v, present := os.LookupEnv(key)
	if !present {
		return fallback
	}
	if v == "" {
		// Present-but-empty almost always means a misconfigured
		// orchestration/secret-injection step, not an intentional choice —
		// worth a loud warning rather than silently using the demo default.
		slog.Warn("env var is set but empty, using default", "key", key, "default", fallback)
		return fallback
	}
	return v
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid env value, using default", "key", key, "value", v, "default", fallback, "error", err)
		return fallback
	}
	return n
}

func getEnvFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		slog.Warn("invalid env value, using default", "key", key, "value", v, "default", fallback, "error", err)
		return fallback
	}
	return f
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("invalid env value, using default", "key", key, "value", v, "default", fallback.String(), "error", err)
		return fallback
	}
	return d
}
