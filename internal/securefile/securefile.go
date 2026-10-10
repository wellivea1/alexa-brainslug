// Package securefile reads and writes the small private files that hold
// provider credentials, account tokens, the owner password hash, and
// dashboard settings.
//
// Writes are atomic. New content goes to a temporary file in the same
// directory, is flushed to disk, and is renamed over the target, so a crash or
// a full disk leaves either the complete old file or the complete new one. The
// strict loaders that read these files refuse a truncated one, and a refusal
// at startup takes the whole service down, Alexa ingress included.
//
// Files are restricted to their owner. On Unix that is mode 0600. On Windows,
// where Go's file modes only toggle the read-only attribute, it is a protected
// access-control list, so privacy does not depend on the inherited permissions
// of whatever folder the files were unpacked into.
package securefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// directoryMode matches the private data directory used for SQLite.
const directoryMode = 0o750

// Write atomically replaces path with data and restricts it to the owner.
func Write(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return fmt.Errorf("create directory for %s: %w", filepath.Base(path), err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", filepath.Base(path), err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()

	// Restricted before any content is written, so the data is never readable
	// more widely than the finished file.
	restrict(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush %s: %w", filepath.Base(path), err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	committed = true
	return nil
}

// CreateExclusive writes data to path only when nothing exists there yet, so
// creating the file is also an atomic claim on it. The returned error wraps
// os.ErrExist when the path is already taken. A failed write removes the
// partial file so the claim can be made again.
func CreateExclusive(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
		return fmt.Errorf("create directory for %s: %w", filepath.Base(path), err)
	}
	// #nosec G304 -- path is operator configuration, not request input.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()

	restrict(path)
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flush %s: %w", filepath.Base(path), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	completed = true
	return nil
}

// DecodeJSON decodes exactly one JSON value into v. Unknown fields and
// trailing data are errors, so a hand-edited or damaged file fails loudly
// instead of being half applied.
func DecodeJSON(data []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

// restrict limits path to its owner. A failure is logged rather than
// returned: some filesystems, such as FAT volumes, cannot store permissions,
// and refusing to save there would break credential management without
// making the file any more private.
func restrict(path string) {
	if err := restrictToOwner(path); err != nil {
		slog.Warn("could not restrict a private file to its owner", "file", filepath.Base(path), "error", err)
	}
}
