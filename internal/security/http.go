// Package security holds the HTTP middleware and the secret redaction used
// before anything is logged or written to local history.
package security

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// contentSecurityPolicy allows only same-origin resources. The dashboard's
// styles are inline, which is why style-src permits 'unsafe-inline'. Its small
// form transport is served from the same origin, so script-src stays locked to
// 'self'.
const contentSecurityPolicy = "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; " +
	"form-action 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'"

// Headers applies response hardening to every route.
//
// The dashboard renders stored prompts and answers, so the policy is written
// for that: no framing, no sniffing, no referrer leakage, and no caching of a
// page full of private data. HSTS is emitted only when the operator has
// declared an HTTPS origin, because advertising it from a plain-HTTP
// deployment would lock clients out of a service that cannot serve them.
func Headers(publicBaseURL string, next http.Handler) http.Handler {
	emitHSTS := strings.HasPrefix(strings.ToLower(strings.TrimSpace(publicBaseURL)), "https://")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cache-Control", "no-store")
		if emitHSTS {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// LimitRequestBody caps request bodies on methods that carry one.
//
// The declared Content-Length is rejected early so an oversized upload is
// refused before it is read, and MaxBytesReader enforces the same limit
// against a chunked body that declares nothing.
func LimitRequestBody(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			if r.ContentLength > maxBytes {
				http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// RequestLogger logs one line per request at debug level.
//
// It records the status and response size, which the bare handler cannot
// report, so that a rejected request is distinguishable from a served one in
// the log. It deliberately logs no query string, no headers, and no body:
// every one of those can carry the question the user asked.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		slog.DebugContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"bytes", recorder.written,
			"remote", r.RemoteAddr,
			"duration", time.Since(started),
		)
	})
}

// statusRecorder captures the status code and byte count of a response.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.written += int64(n)
	return n, err
}

// Unwrap exposes the wrapped writer to http.ResponseController, so that
// wrapping does not disable flushing or deadline control.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
