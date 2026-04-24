//go:build unix

package tsanalyzer

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup places cmd in its own process group and installs
// a Cancel hook that signals the whole group on context cancellation.
// Without this, a timeout only SIGKILLs the direct child (e.g. /bin/sh,
// npx); forked descendants (vitest/jest workers, orphaned `sleep`) keep
// the inherited stdout/stderr pipes open and block cmd.Wait() until they
// exit on their own.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
