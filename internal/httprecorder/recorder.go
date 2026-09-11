// Package httprecorder provides a tiny http.ResponseWriter wrapper that
// captures the status code, so middleware (logging, metrics) can observe
// it after the handler has written the response.
package httprecorder

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
)

type StatusRecorder struct {
	http.ResponseWriter
	Status int
}

// Wrap returns a StatusRecorder for w. If w is already one (e.g. an outer
// middleware already wrapped it), it's reused as-is instead of nesting
// another layer — nesting would double the allocations for no benefit and,
// more importantly, still only exposes whatever optional interfaces this
// wrapper implements to callers further down the chain.
func Wrap(w http.ResponseWriter) *StatusRecorder {
	if rec, ok := w.(*StatusRecorder); ok {
		return rec
	}
	return &StatusRecorder{ResponseWriter: w, Status: http.StatusOK}
}

func (r *StatusRecorder) WriteHeader(code int) {
	r.Status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush lets a wrapped writer keep working as an http.Flusher (needed for
// streamed/chunked responses proxied through httputil.ReverseProxy) instead
// of silently losing that capability behind this wrapper.
func (r *StatusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack passes through http.Hijacker support for the same reason.
func (r *StatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("httprecorder: underlying ResponseWriter does not support hijacking")
	}
	return h.Hijack()
}
