//go:build !unix

package tsanalyzer

import "os/exec"

// configureProcessGroup is a no-op on non-unix platforms. WaitDelay in
// testrunner.go still provides an upper bound on how long cmd.Wait() can
// block after context cancellation.
func configureProcessGroup(cmd *exec.Cmd) {}
