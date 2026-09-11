// Package healthcheck runs a background prober per backend and exposes a
// thread-safe snapshot of their status for the gateway's own /health
// endpoint.
package healthcheck

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Status struct {
	Healthy   bool      `json:"healthy"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

type Checker struct {
	mu       sync.RWMutex
	statuses map[string]Status

	targets  map[string]string
	client   *http.Client
	interval time.Duration
	timeout  time.Duration
}

func New(targets map[string]string, interval, timeout time.Duration) *Checker {
	return &Checker{
		statuses: make(map[string]Status, len(targets)),
		targets:  targets,
		client:   &http.Client{Timeout: timeout},
		interval: interval,
		timeout:  timeout,
	}
}

// Run blocks, probing all targets immediately and then on every tick, until
// ctx is cancelled. Intended to be started with `go checker.Run(ctx)`.
func (c *Checker) Run(ctx context.Context) {
	c.checkAll(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkAll(ctx)
		}
	}
}

func (c *Checker) checkAll(ctx context.Context) {
	var wg sync.WaitGroup
	for name, url := range c.targets {
		wg.Add(1)
		go func(name, url string) {
			defer wg.Done()
			c.checkOne(ctx, name, url)
		}(name, url)
	}
	wg.Wait()
}

func (c *Checker) checkOne(ctx context.Context, name, url string) {
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	status := Status{CheckedAt: time.Now()}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		status.Error = err.Error()
	} else {
		resp, err := c.client.Do(req)
		if err != nil {
			status.Error = err.Error()
		} else {
			defer resp.Body.Close() //nolint:errcheck // draining a health-check response body, nothing actionable if this fails
			status.Healthy = resp.StatusCode == http.StatusOK
			if !status.Healthy {
				status.Error = fmt.Sprintf("unexpected status %d", resp.StatusCode)
			}
		}
	}

	c.mu.Lock()
	c.statuses[name] = status
	c.mu.Unlock()
}

// Snapshot returns a copy of the current status map, safe to read/serialize
// without holding any lock the caller doesn't own.
func (c *Checker) Snapshot() map[string]Status {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]Status, len(c.statuses))
	for k, v := range c.statuses {
		out[k] = v
	}
	return out
}
