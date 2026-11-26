// Package middleware holds the HTTP middleware shared by the JMAP endpoints and the admin API.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/woodchen-ink/go-web-utils/resputil"
)

type ctxKey int

const traceKey ctxKey = 0

// TraceID returns the request's trace id, or "" outside a request.
func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(traceKey).(string)
	return id
}

// ClientIPFunc resolves the client address for logs and security decisions.
type ClientIPFunc func(*http.Request) string

// Logging assigns a trace id (echoed as X-Request-Id) and writes one JSON line per request.
// EventSource streams are logged when they end.
func Logging(log *slog.Logger, clientIP ClientIPFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := newTraceID()
			w.Header().Set("X-Request-Id", id)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), traceKey, id)))
			log.LogAttrs(r.Context(), slog.LevelInfo, "request",
				slog.String("trace_id", id),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("ip", clientIP(r)),
			)
		})
	}
}

// Recover turns a panic into a 500 response and logs the stack.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					log.Error("panic", "trace_id", TraceID(r.Context()), "panic", v, "stack", string(debug.Stack()))
					resputil.FailStatus(w, http.StatusInternalServerError, 50000, "internal_error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Chain applies middleware so that the first one is the outermost.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush keeps EventSource streaming through the recorder.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func newTraceID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
