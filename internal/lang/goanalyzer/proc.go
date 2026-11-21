package goanalyzer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// outputTail is how much of a child's output is kept for reports.
const outputTail = 4 << 10

// pipeGrace is how long a finished child's output pipes may stay open.
const pipeGrace = 2000 * time.Millisecond

// procResult is the outcome of one child process.
type procResult struct {
	err      error
	output   string
	duration time.Duration
	timedOut bool
}

// proc describes a child process to run.
type proc struct {
	dir     string
	env     []string // appended to the parent's environment
	timeout time.Duration
	name    string
	args    []string
	// keep is how many trailing bytes of output to return; zero means
	// outputTail.
	keep int
}

// run executes the process, killing it when timeout elapses or ctx is done.
// timedOut is set only for the timeout: a cancelled ctx is the caller's to
// detect, since it means the result must be discarded.
func (p proc) run(ctx context.Context) procResult {
	runCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, p.name, p.args...)
	cmd.Dir = p.dir
	// A child must not inherit this process's own mutant selection, which
	// is set when diffguard's tests run under mutation testing themselves.
	cmd.Env = append(os.Environ(), mutantEnv+"=", traceEnv+"=")
	cmd.Env = append(cmd.Env, p.env...)
	if p.dir != "" {
		// The go tool takes its working directory from PWD. Left at the
		// parent's value it resolves symlinks itself, and overlay paths
		// written with the unresolved directory stop matching.
		cmd.Env = append(cmd.Env, "PWD="+p.dir)
	}
	// A test that leaves a child holding the pipes must not hang the run.
	cmd.WaitDelay = pipeGrace
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	start := time.Now()
	err := cmd.Run()
	return procResult{
		err:      err,
		output:   tail(out.Bytes(), p.keep),
		duration: time.Since(start),
		timedOut: ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded),
	}
}

func tail(b []byte, n int) string {
	if n <= 0 {
		n = outputTail
	}
	return string(b[max(0, len(b)-n):])
}
