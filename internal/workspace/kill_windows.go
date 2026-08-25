//go:build windows

package workspace

import "os/exec"

// setProcessGroup is a no-op on Windows, which has no portable process-group
// primitive; the child is killed directly in killGroup.
func setProcessGroup(cmd *exec.Cmd) {}

// killGroup terminates the command's process after a context deadline.
// Best-effort.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
