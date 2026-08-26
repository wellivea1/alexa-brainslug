package adminauth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOptions(now *time.Time) Options {
	return Options{
		PasswordIterations:   1_000,
		SessionIdleTimeout:   10 * time.Minute,
		SessionAbsoluteLimit: time.Hour,
		FailureWindow:        time.Minute,
		LockoutDuration:      5 * time.Minute,
		MaxFailures:          3,
		Now:                  func() time.Time { return *now },
	}
}

func TestCreateOwnerPersistsHashAndLoginStartsSession(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "brainslug.admin-auth.json")
	manager, err := New(path, testOptions(&now))
	if err != nil {
		t.Fatal(err)
	}
	if manager.Configured() {
		t.Fatal("fresh manager is configured")
	}

	created, err := manager.CreateOwner("Guardian", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateOwner() error = %v", err)
	}
	if created.Token == "" || created.Username != "Guardian" || !manager.Configured() {
		t.Fatalf("created session = %#v", created)
	}
	if username, ok := manager.ValidateSession(created.Token); !ok || username != "Guardian" {
		t.Fatalf("ValidateSession() = %q, %v", username, ok)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "correct horse") || !strings.Contains(string(data), hashAlgorithm) {
		t.Fatalf("credential file did not contain only a password hash: %s", data)
	}

	reloaded, err := New(path, testOptions(&now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Login("guardian", "wrong password", "127.0.0.1:1000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	loggedIn, err := reloaded.Login("GUARDIAN", "correct horse battery staple", "127.0.0.1:1000")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if username, ok := reloaded.ValidateSession(loggedIn.Token); !ok || username != "Guardian" {
		t.Fatalf("reloaded session = %q, %v", username, ok)
	}
	reloaded.Logout(loggedIn.Token)
	if _, ok := reloaded.ValidateSession(loggedIn.Token); ok {
		t.Fatal("logged-out session remained valid")
	}
}

func TestSetupValidatesAccountAndCannotBeRepeated(t *testing.T) {
	now := time.Now().UTC()
	manager, err := New(filepath.Join(t.TempDir(), "admin.json"), testOptions(&now))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		username string
		password string
	}{
		{username: "ab", password: "correct horse battery staple"},
		{username: "bad name", password: "correct horse battery staple"},
		{username: "owner", password: "too-short"},
	} {
		if _, err := manager.CreateOwner(test.username, test.password); err == nil {
			t.Fatalf("CreateOwner(%q) accepted invalid account", test.username)
		}
	}
	if _, err := manager.CreateOwner("owner", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateOwner("other", "another correct horse password"); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("second owner error = %v", err)
	}
}

func TestSessionsExpireOnIdleAndAbsoluteLimits(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	manager, err := New(filepath.Join(t.TempDir(), "admin.json"), testOptions(&now))
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.CreateOwner("owner", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(9 * time.Minute)
	if _, ok := manager.ValidateSession(session.Token); !ok {
		t.Fatal("session expired before idle timeout")
	}
	now = now.Add(11 * time.Minute)
	if _, ok := manager.ValidateSession(session.Token); ok {
		t.Fatal("session survived idle timeout")
	}

	session, err = manager.Login("owner", "correct horse battery staple", "127.0.0.1:1000")
	if err != nil {
		t.Fatal(err)
	}
	now = session.ExpiresAt
	if _, ok := manager.ValidateSession(session.Token); ok {
		t.Fatal("session survived absolute timeout")
	}
}

func TestLoginThrottlesUsernameAndRemoteIndependently(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	manager, err := New(filepath.Join(t.TempDir(), "admin.json"), testOptions(&now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateOwner("owner", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		_, err := manager.Login("owner", "wrong password", "192.0.2."+string(rune('0'+attempt))+":1000")
		if attempt < 3 && !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
		if attempt == 3 && !errors.Is(err, ErrRateLimited) {
			t.Fatalf("username was not rate limited: %v", err)
		}
	}
	if _, err := manager.Login("owner", "correct horse battery staple", "198.51.100.1:1000"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("username lockout bypassed with another remote: %v", err)
	}

	now = now.Add(6 * time.Minute)
	for attempt := 1; attempt <= 3; attempt++ {
		_, err := manager.Login("unknown"+string(rune('0'+attempt)), "wrong password", "203.0.113.5:1000")
		if attempt == 3 && !errors.Is(err, ErrRateLimited) {
			t.Fatalf("remote was not rate limited: %v", err)
		}
	}
}

func TestEmptyStoragePathFailsClosedOnSetup(t *testing.T) {
	now := time.Now().UTC()
	manager, err := New("", testOptions(&now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateOwner("owner", "correct horse battery staple"); err == nil {
		t.Fatal("setup succeeded without durable storage")
	}
}

func TestMalformedCredentialFileFailsClosed(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "admin.json")
	if err := os.WriteFile(path, []byte(`{"version":1} trailing`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path, testOptions(&now)); err == nil {
		t.Fatal("New() accepted a malformed management credential file")
	}
}
