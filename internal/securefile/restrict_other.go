//go:build !windows

package securefile

import "os"

func restrictToOwner(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info.IsDir() {
		// The owner must retain traversal access to create and read private files.
		mode = 0o700
	}
	return os.Chmod(path, mode)
}
