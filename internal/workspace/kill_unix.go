//go:build !windows

package workspace

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so the whole group
// (the shell plus any foreground grandchildren) can be killed together.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup terminates the command's process group (falling back to the
// process itself) after a context deadline. It is best-effort.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	neg := -cmd.Process.Pid // negative pid signals the whole group
	if err := syscall.Kill(neg, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
