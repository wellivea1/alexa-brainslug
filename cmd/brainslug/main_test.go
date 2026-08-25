package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wellivea1/alexa-brainslug/internal/config"
	"github.com/wellivea1/alexa-brainslug/internal/store"
)

func TestPublicHandlerExposesOnlyAlexa(t *testing.T) {
	alexaHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := buildPublicHandler("", alexaHandler)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /alexa status = %d", rec.Code)
	}

	for _, path := range []string{"/", "/health", "/history"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want %d", path, rec.Code, http.StatusNotFound)
			}
		})
	}
}

func TestAdminHandlerDoesNotExposeAlexa(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "brainslug.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("store.Close() error = %v", err)
		}
	})

	cfg := config.Config{
		Provider: config.ProviderOpenAI,
		OpenAI: config.OpenAIConfig{
			Model: "gpt-test",
		},
	}
	handler := buildAdminHandler(&cfg, "fake", db)

	for _, path := range []string{"/", "/health", "/history"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("GET %s status = %d, body = %s", path, rec.Code, rec.Body.String())
			}
		})
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST /alexa status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
