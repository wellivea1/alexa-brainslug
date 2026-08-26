// Package adminauth owns the single local management account and its
// short-lived browser sessions. Password material is never exposed to the web
// package after verification.
package adminauth

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	credentialVersion         = 1
	hashAlgorithm             = "pbkdf2-sha256"
	defaultPasswordIterations = 600_000
	passwordSaltBytes         = 16
	passwordDigestBytes       = 32
	minimumPasswordBytes      = 12
	maximumPasswordBytes      = 128
	sessionTokenBytes         = 32
	defaultSessionIdle        = 30 * time.Minute
	defaultSessionAbsolute    = 8 * time.Hour
	defaultFailureWindow      = 5 * time.Minute
	defaultLockout            = 10 * time.Minute
	defaultMaxFailures        = 5
	maximumSessions           = 10
)

var (
	// ErrAlreadyConfigured means first-run ownership has already been claimed.
	ErrAlreadyConfigured = errors.New("management account is already configured")
	// ErrNotConfigured means the owner must be created before login.
	ErrNotConfigured = errors.New("management account is not configured")
	// ErrInvalidCredentials is deliberately generic to avoid username discovery.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrRateLimited means too many recent login failures were recorded.
	ErrRateLimited = errors.New("too many login attempts")
)

// Options overrides production costs and clocks in tests.
type Options struct {
	PasswordIterations   int
	SessionIdleTimeout   time.Duration
	SessionAbsoluteLimit time.Duration
	FailureWindow        time.Duration
	LockoutDuration      time.Duration
	MaxFailures          int
	Now                  func() time.Time
}

type passwordRecord struct {
	Algorithm  string `json:"algorithm"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Digest     string `json:"digest"`
}

type credentialFile struct {
	Version      int            `json:"version"`
	Username     string         `json:"username"`
	Password     passwordRecord `json:"password"`
	CreatedAtUTC time.Time      `json:"created_at_utc"`
}

type storedSession struct {
	Username  string
	CreatedAt time.Time
	LastSeen  time.Time
	ExpiresAt time.Time
}

type attemptBucket struct {
	StartedAt   time.Time
	Failures    int
	LockedUntil time.Time
}

// Session is returned only when a setup or login succeeds.
type Session struct {
	Token     string
	Username  string
	ExpiresAt time.Time
}

// Manager is safe for concurrent HTTP handlers.
type Manager struct {
	mu                     sync.Mutex
	path                   string
	owner                  *credentialFile
	csrfToken              string
	sessions               map[[sha256.Size]byte]storedSession
	attempts               map[string]attemptBucket
	passwordIterations     int
	sessionIdleTimeout     time.Duration
	sessionAbsoluteTimeout time.Duration
	failureWindow          time.Duration
	lockoutDuration        time.Duration
	maxFailures            int
	now                    func() time.Time
}

// New loads the management credential when present. An empty path remains
// readable but first-run setup fails closed because it cannot persist safely.
func New(path string, configured ...Options) (*Manager, error) {
	options := Options{}
	if len(configured) > 0 {
		options = configured[0]
	}
	applyOptionDefaults(&options)
	csrfToken, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("generate management CSRF token: %w", err)
	}
	manager := &Manager{
		path:                   strings.TrimSpace(path),
		csrfToken:              csrfToken,
		sessions:               make(map[[sha256.Size]byte]storedSession),
		attempts:               make(map[string]attemptBucket),
		passwordIterations:     options.PasswordIterations,
		sessionIdleTimeout:     options.SessionIdleTimeout,
		sessionAbsoluteTimeout: options.SessionAbsoluteLimit,
		failureWindow:          options.FailureWindow,
		lockoutDuration:        options.LockoutDuration,
		maxFailures:            options.MaxFailures,
		now:                    options.Now,
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

// Configured reports whether the first owner exists.
func (m *Manager) Configured() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.owner != nil
}

// CSRFToken returns the process-scoped secret embedded in same-origin forms.
func (m *Manager) CSRFToken() string { return m.csrfToken }

// CreateOwner atomically claims first-run ownership and starts a session.
func (m *Manager) CreateOwner(username, password string) (Session, error) {
	username, err := validateUsername(username)
	if err != nil {
		return Session{}, err
	}
	if err := validatePassword(password); err != nil {
		return Session{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != nil {
		return Session{}, ErrAlreadyConfigured
	}
	if m.path == "" {
		return Session{}, errors.New("management credential storage is unavailable")
	}
	record, err := derivePassword(password, m.passwordIterations)
	if err != nil {
		return Session{}, err
	}
	owner := credentialFile{
		Version:      credentialVersion,
		Username:     username,
		Password:     record,
		CreatedAtUTC: m.now().UTC(),
	}
	if err := writeCredentialExclusive(m.path, owner); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Session{}, ErrAlreadyConfigured
		}
		return Session{}, err
	}
	m.owner = &owner
	return m.newSessionLocked(username)
}

// Login verifies both account and source throttles before starting a session.
func (m *Manager) Login(username, password, remoteAddr string) (Session, error) {
	normalizedUsername := normalizeUsername(username)
	remote := normalizeRemote(remoteAddr)
	now := m.now()
	userKey := "user:" + normalizedUsername
	remoteKey := "remote:" + remote

	m.mu.Lock()
	if m.owner == nil {
		m.mu.Unlock()
		return Session{}, ErrNotConfigured
	}
	m.cleanupLocked(now)
	if m.bucketLockedLocked(userKey, now) || m.bucketLockedLocked(remoteKey, now) {
		m.mu.Unlock()
		return Session{}, ErrRateLimited
	}
	owner := *m.owner
	m.mu.Unlock()

	usernameMatch := constantStringEqual(normalizedUsername, normalizeUsername(owner.Username))
	passwordMatch := verifyPassword(password, owner.Password)
	if !usernameMatch || !passwordMatch {
		m.mu.Lock()
		userLimited := m.recordFailureLocked(userKey, now)
		remoteLimited := m.recordFailureLocked(remoteKey, now)
		m.mu.Unlock()
		if userLimited || remoteLimited {
			return Session{}, ErrRateLimited
		}
		return Session{}, ErrInvalidCredentials
	}

	m.mu.Lock()
	delete(m.attempts, userKey)
	delete(m.attempts, remoteKey)
	session, err := m.newSessionLocked(owner.Username)
	m.mu.Unlock()
	return session, err
}

// ValidateSession validates and refreshes the idle deadline in memory.
func (m *Manager) ValidateSession(token string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(raw) != sessionTokenBytes {
		return "", false
	}
	digest := sha256.Sum256(raw)
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(now)
	session, ok := m.sessions[digest]
	if !ok || now.Sub(session.LastSeen) > m.sessionIdleTimeout || !now.Before(session.ExpiresAt) {
		delete(m.sessions, digest)
		return "", false
	}
	session.LastSeen = now
	m.sessions[digest] = session
	return session.Username, true
}

// Logout invalidates one session immediately.
func (m *Manager) Logout(token string) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(raw) != sessionTokenBytes {
		return
	}
	digest := sha256.Sum256(raw)
	m.mu.Lock()
	delete(m.sessions, digest)
	m.mu.Unlock()
}

func (m *Manager) load() error {
	if m.path == "" {
		return nil
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read management credential file: %w", err)
	}
	var owner credentialFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&owner); err != nil {
		return fmt.Errorf("decode management credential file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("decode management credential file: trailing data")
	}
	if owner.Version != credentialVersion {
		return fmt.Errorf("management credential version must be %d", credentialVersion)
	}
	username, err := validateUsername(owner.Username)
	if err != nil {
		return fmt.Errorf("management credential username: %w", err)
	}
	owner.Username = username
	if err := validatePasswordRecord(owner.Password); err != nil {
		return fmt.Errorf("management credential password: %w", err)
	}
	m.owner = &owner
	return nil
}

func (m *Manager) newSessionLocked(username string) (Session, error) {
	token, err := randomToken()
	if err != nil {
		return Session{}, fmt.Errorf("generate management session: %w", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	digest := sha256.Sum256(raw)
	now := m.now()
	if len(m.sessions) >= maximumSessions {
		var oldestKey [sha256.Size]byte
		oldest := now
		for key, existing := range m.sessions {
			if !existing.CreatedAt.After(oldest) {
				oldestKey = key
				oldest = existing.CreatedAt
			}
		}
		delete(m.sessions, oldestKey)
	}
	expiresAt := now.Add(m.sessionAbsoluteTimeout)
	m.sessions[digest] = storedSession{Username: username, CreatedAt: now, LastSeen: now, ExpiresAt: expiresAt}
	return Session{Token: token, Username: username, ExpiresAt: expiresAt}, nil
}

func (m *Manager) cleanupLocked(now time.Time) {
	for key, session := range m.sessions {
		if !now.Before(session.ExpiresAt) || now.Sub(session.LastSeen) > m.sessionIdleTimeout {
			delete(m.sessions, key)
		}
	}
	for key, attempt := range m.attempts {
		if now.After(attempt.LockedUntil) && now.Sub(attempt.StartedAt) > m.failureWindow {
			delete(m.attempts, key)
		}
	}
}

func (m *Manager) bucketLockedLocked(key string, now time.Time) bool {
	bucket, ok := m.attempts[key]
	return ok && now.Before(bucket.LockedUntil)
}

func (m *Manager) recordFailureLocked(key string, now time.Time) bool {
	bucket := m.attempts[key]
	if bucket.StartedAt.IsZero() || now.Sub(bucket.StartedAt) > m.failureWindow {
		bucket = attemptBucket{StartedAt: now}
	}
	bucket.Failures++
	if bucket.Failures >= m.maxFailures {
		bucket.LockedUntil = now.Add(m.lockoutDuration)
	}
	m.attempts[key] = bucket
	return now.Before(bucket.LockedUntil)
}

func derivePassword(password string, iterations int) (passwordRecord, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return passwordRecord{}, fmt.Errorf("generate password salt: %w", err)
	}
	digest, err := pbkdf2.Key(sha256.New, password, salt, iterations, passwordDigestBytes)
	if err != nil {
		return passwordRecord{}, fmt.Errorf("derive password hash: %w", err)
	}
	return passwordRecord{
		Algorithm:  hashAlgorithm,
		Iterations: iterations,
		Salt:       base64.RawStdEncoding.EncodeToString(salt),
		Digest:     base64.RawStdEncoding.EncodeToString(digest),
	}, nil
}

func verifyPassword(password string, record passwordRecord) bool {
	if len(password) > maximumPasswordBytes || validatePasswordRecord(record) != nil {
		return false
	}
	salt, _ := base64.RawStdEncoding.DecodeString(record.Salt)
	want, _ := base64.RawStdEncoding.DecodeString(record.Digest)
	got, err := pbkdf2.Key(sha256.New, password, salt, record.Iterations, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func validatePasswordRecord(record passwordRecord) error {
	if record.Algorithm != hashAlgorithm {
		return fmt.Errorf("algorithm must be %s", hashAlgorithm)
	}
	if record.Iterations < 1 || record.Iterations > 10_000_000 {
		return errors.New("iteration count is invalid")
	}
	salt, err := base64.RawStdEncoding.DecodeString(record.Salt)
	if err != nil || len(salt) != passwordSaltBytes {
		return errors.New("salt is invalid")
	}
	digest, err := base64.RawStdEncoding.DecodeString(record.Digest)
	if err != nil || len(digest) != passwordDigestBytes {
		return errors.New("digest is invalid")
	}
	return nil
}

func validateUsername(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 64 {
		return "", errors.New("username must be 3 to 64 characters")
	}
	for index, character := range value {
		allowed := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' || character == '-' || character == '.'
		if !allowed || index == 0 && (character == '_' || character == '-' || character == '.') {
			return "", errors.New("username may use letters, numbers, periods, underscores, and hyphens, and must start with a letter or number")
		}
	}
	return value, nil
}

func validatePassword(value string) error {
	length := len(value)
	if length < minimumPasswordBytes {
		return fmt.Errorf("password must be at least %d characters", minimumPasswordBytes)
	}
	if length > maximumPasswordBytes {
		return fmt.Errorf("password must be no more than %d bytes", maximumPasswordBytes)
	}
	return nil
}

func writeCredentialExclusive(path string, owner credentialFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create management credential directory: %w", err)
	}
	data, err := json.MarshalIndent(owner, "", "  ")
	if err != nil {
		return fmt.Errorf("encode management credential: %w", err)
	}
	// #nosec G304 -- path is the operator-configured credential path, not request input.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create management credential file: %w", err)
	}
	removePartial := true
	defer func() {
		_ = file.Close()
		if removePartial {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write management credential file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync management credential file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close management credential file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect management credential file: %w", err)
	}
	removePartial = false
	return nil
}

func randomToken() (string, error) {
	value := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func constantStringEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func normalizeUsername(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func normalizeRemote(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(value)
}

func applyOptionDefaults(options *Options) {
	if options.PasswordIterations <= 0 {
		options.PasswordIterations = defaultPasswordIterations
	}
	if options.SessionIdleTimeout <= 0 {
		options.SessionIdleTimeout = defaultSessionIdle
	}
	if options.SessionAbsoluteLimit <= 0 {
		options.SessionAbsoluteLimit = defaultSessionAbsolute
	}
	if options.FailureWindow <= 0 {
		options.FailureWindow = defaultFailureWindow
	}
	if options.LockoutDuration <= 0 {
		options.LockoutDuration = defaultLockout
	}
	if options.MaxFailures <= 0 {
		options.MaxFailures = defaultMaxFailures
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
}

type usernameContextKey struct{}

// ContextWithUsername records the authenticated owner for downstream views.
func ContextWithUsername(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, usernameContextKey{}, username)
}

// UsernameFromContext returns the authenticated owner name.
func UsernameFromContext(ctx context.Context) string {
	username, _ := ctx.Value(usernameContextKey{}).(string)
	return username
}
