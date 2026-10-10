package securefile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrepareDirectoryPreservesOwnerTraversal(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "private")
			if existing {
				if err := os.Mkdir(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := PrepareDirectory(directory); err != nil {
				t.Fatalf("PrepareDirectory() error = %v", err)
			}
			if runtime.GOOS != "windows" {
				info, err := os.Stat(directory)
				if err != nil {
					t.Fatal(err)
				}
				if permission := info.Mode().Perm(); permission != 0o700 {
					t.Fatalf("directory permissions = %o, want 700", permission)
				}
			}
			path := filepath.Join(directory, "identity.key")
			if err := CreateExclusive(path, []byte("private content")); err != nil {
				t.Fatalf("CreateExclusive() in private directory error = %v", err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "private content" {
				t.Fatalf("private directory content = %q, %v", got, err)
			}
		})
	}
}

func TestWriteReplacesContentWithoutLeftovers(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "brainslug.settings.json")

	if err := Write(path, []byte("first")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if err := Write(path, []byte("second")); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("content = %q, want %q", got, "second")
	}
	assertOnlyEntries(t, directory, "brainslug.settings.json")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if permission := info.Mode().Perm(); permission != 0o600 {
			t.Errorf("permissions = %o, want 600", permission)
		}
	}
}

// The target is replaced only by a complete file. When the final step fails,
// the temporary file is removed and nothing else in the directory changes.
func TestWriteFailureLeavesDirectoryUnchanged(t *testing.T) {
	directory := t.TempDir()
	existing := filepath.Join(directory, "brainslug.provider-auth.json")
	if err := Write(existing, []byte("intact")); err != nil {
		t.Fatal(err)
	}
	// Renaming a file onto a non-empty directory fails on every platform.
	blocked := filepath.Join(directory, "blocked")
	if err := os.Mkdir(blocked, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "child"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Write(blocked, []byte("new")); err == nil {
		t.Fatal("Write() over a non-empty directory succeeded")
	}
	assertOnlyEntries(t, directory, "blocked", "brainslug.provider-auth.json")
	if got, err := os.ReadFile(existing); err != nil || string(got) != "intact" {
		t.Errorf("existing file = %q, %v", got, err)
	}
}

func TestCreateExclusiveClaimsPathOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "brainslug.admin-auth.json")

	if err := CreateExclusive(path, []byte("owner")); err != nil {
		t.Fatalf("CreateExclusive() error = %v", err)
	}
	if err := CreateExclusive(path, []byte("intruder")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second CreateExclusive() error = %v, want os.ErrExist", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "owner" {
		t.Errorf("content = %q, %v; the first claim must survive", got, err)
	}
}

func TestDecodeJSONIsStrict(t *testing.T) {
	var target struct {
		Version int `json:"version"`
	}
	tests := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{"single value", `{"version": 1}`, false},
		{"unknown field", `{"version": 1, "extra": true}`, true},
		{"trailing value", `{"version": 1} {"version": 2}`, true},
		{"truncated", `{"version": 1`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := DecodeJSON([]byte(tt.data), &target); (err != nil) != tt.wantErr {
				t.Errorf("DecodeJSON(%s) error = %v, want error %v", tt.data, err, tt.wantErr)
			}
		})
	}
}

func assertOnlyEntries(t *testing.T, directory string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != len(want) {
		t.Fatalf("directory entries = %q, want %q", names, want)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("directory entries = %q, want %q", names, want)
		}
	}
}
