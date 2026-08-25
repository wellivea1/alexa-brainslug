package providerauth

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStorePersistsAndRemovesProviderAPIKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brainslug.provider-auth.json")
	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("Anthropic", "sk-ant-test-value"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai", "sk-openai-test-value"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if key, ok := reloaded.Get("anthropic"); !ok || key != "sk-ant-test-value" {
		t.Fatalf("Anthropic credential = %q, %v", key, ok)
	}
	if key, ok := reloaded.Get("openai"); !ok || key != "sk-openai-test-value" {
		t.Fatalf("OpenAI credential = %q, %v", key, ok)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if permission := info.Mode().Perm(); permission != 0o600 {
			t.Fatalf("credential permissions = %o", permission)
		}
	}

	if err := reloaded.Remove("anthropic"); err != nil {
		t.Fatal(err)
	}
	afterRemoval, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := afterRemoval.Get("anthropic"); ok {
		t.Fatal("Anthropic credential survived removal")
	}
	if key, ok := afterRemoval.Get("openai"); !ok || key != "sk-openai-test-value" {
		t.Fatal("removing Anthropic also removed OpenAI")
	}
}

func TestStoreRejectsInvalidFilesAndUnavailablePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"credentials":{"anthropic":{"type":"oauth","key":"secret"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("invalid file error = %v", err)
	}

	store, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("anthropic", "secret-value"); err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("unavailable storage error = %v", err)
	}
}
