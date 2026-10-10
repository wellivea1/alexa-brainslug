package securefile

import (
	"os"
)

// PrepareDirectory creates a dedicated process data directory with owner-only
// access. Callers supply operator configuration, never request paths.
func PrepareDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return restrictToOwner(path)
}
