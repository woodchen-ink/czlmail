package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

// AuditRecorder stores audit entries.
type AuditRecorder interface {
	Record(*model.AuditLog) error
}

const (
	maxAdminBody   = 64 << 10 // admin request bodies are small JSON objects
	maxCapturedOut = 16 << 10 // write responses are single small objects; enough to read code and msg
)

// Audit records every admin write request: who, which endpoint, the masked body, and
// the response status, code and msg. The response body itself is never stored.
func Audit(rec AuditRecorder, clientIP ClientIPFunc, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAdminBody))
			if err != nil {
				body = nil
				r.Body = io.NopCloser(errReader{err})
			} else {
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			holder := &actorHolder{}
			cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(cw, r.WithContext(context.WithValue(r.Context(), actorKey, holder)))

			var resp struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
			}
			if json.Unmarshal(cw.buf.Bytes(), &resp) != nil {
				resp.Code = cw.status * 100
			}
			entry := &model.AuditLog{
				Method:  r.Method,
				Path:    r.URL.Path,
				Params:  service.MaskParams(body),
				Status:  cw.status,
				Code:    resp.Code,
				Msg:     truncate(resp.Msg, 64),
				IP:      clientIP(r),
				TraceID: TraceID(r.Context()),
			}
			if u := holder.user; u != nil {
				entry.ActorID, entry.ActorEmail = u.PublicID, u.Email
			}
			if err := rec.Record(entry); err != nil {
				log.Error("write audit log", "trace_id", entry.TraceID, "err", err)
			}
		})
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// captureWriter keeps the status and the start of the body.
type captureWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (c *captureWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if room := maxCapturedOut - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return c.ResponseWriter.Write(p)
}

func (c *captureWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
