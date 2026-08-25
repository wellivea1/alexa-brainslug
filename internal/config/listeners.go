package config

import (
	"errors"
	"os"
	"strings"
)

const defaultAdminServerAddr = "127.0.0.1:8081"

// LoadAdminServerAddr reads and validates the loopback administrative listener
// separately from the public ASK listener.
//
// Docker overrides the default with :8081 inside the container, while Compose
// binds that port to host loopback. A local process keeps the safer loopback
// default without requiring another environment variable.
func LoadAdminServerAddr(publicAddr string) (string, error) {
	addr := strings.TrimSpace(os.Getenv("ADMIN_SERVER_ADDR"))
	if addr == "" {
		addr = defaultAdminServerAddr
	}
	if addr == strings.TrimSpace(publicAddr) {
		return "", errors.New("ADMIN_SERVER_ADDR must differ from SERVER_ADDR")
	}
	return addr, nil
}
