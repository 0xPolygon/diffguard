//go:build !windows

package tsanalyzer

import (
	"os/exec"
	"syscall"
	"time"
)

// configureProcessKill makes cmd kill its entire process group when the
// command context is canceled. exec.CommandContext by default sends SIGKILL
// to the direct child only; a grandchild like `sleep` spawned by a shell that
// did not exec-optimize inherits the shell's stdio pipes and keeps cmd.Wait()
// blocked for its full runtime even though the shell is dead. Placing the
// child in its own process group and sending SIGKILL to the negative PID
// reaps the whole tree.
//
// WaitDelay is a belt-and-suspenders: if any pipe descriptor remains open
// after cancellation, cmd.Wait() force-closes it after the delay and returns.
func configureProcessKill(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
