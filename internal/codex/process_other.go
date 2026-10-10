//go:build !windows

package codex

import (
	"os/exec"
	"syscall"
)

func hideProcess(command *exec.Cmd) { command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func containProcess(command *exec.Cmd) (func() error, error) {
	return func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }, nil
}
