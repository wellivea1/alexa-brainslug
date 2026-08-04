package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeadersSetHardeningHeaders(t *testing.T) {
	handler := Headers("https://brain-slug.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff header")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store header")
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("missing HSTS header for https public URL")
	}
}

func TestLimitRequestBodyRejectsLargePost(t *testing.T) {
	handler := LimitRequestBody(4, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader("12345"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
}
