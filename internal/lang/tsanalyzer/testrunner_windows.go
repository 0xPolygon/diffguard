//go:build windows

package tsanalyzer

import (
	"os/exec"
	"time"
)

// configureProcessKill only sets WaitDelay on Windows. The Unix concern
// (shell-plus-child pipe inheritance blocking cmd.Wait) does not apply to
// the cmd.exe / pwsh shapes we exercise, and Windows process groups need a
// distinct call shape that this codebase does not yet require.
func configureProcessKill(cmd *exec.Cmd) {
	cmd.WaitDelay = 500 * time.Millisecond
}
