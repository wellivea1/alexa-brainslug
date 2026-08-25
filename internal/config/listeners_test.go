package config

import (
	"strings"
	"testing"
)

func TestLoadAdminServerAddr(t *testing.T) {
	t.Run("loopback default", func(t *testing.T) {
		t.Setenv("ADMIN_SERVER_ADDR", "")
		addr, err := LoadAdminServerAddr(":8080")
		if err != nil {
			t.Fatalf("LoadAdminServerAddr() error = %v", err)
		}
		if addr != defaultAdminServerAddr {
			t.Errorf("address = %q, want %q", addr, defaultAdminServerAddr)
		}
	})

	t.Run("explicit address", func(t *testing.T) {
		t.Setenv("ADMIN_SERVER_ADDR", ":9091")
		addr, err := LoadAdminServerAddr(":9090")
		if err != nil {
			t.Fatalf("LoadAdminServerAddr() error = %v", err)
		}
		if addr != ":9091" {
			t.Errorf("address = %q", addr)
		}
	})

	t.Run("same listener rejected", func(t *testing.T) {
		t.Setenv("ADMIN_SERVER_ADDR", " :9090 ")
		_, err := LoadAdminServerAddr(":9090")
		if err == nil || !strings.Contains(err.Error(), "must differ") {
			t.Fatalf("LoadAdminServerAddr() error = %v", err)
		}
	})
}
