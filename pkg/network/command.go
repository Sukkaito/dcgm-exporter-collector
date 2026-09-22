package network

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CommandRunner executes system commands with timeout and context cancellation.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecCommandRunner implements CommandRunner using os/exec.
type ExecCommandRunner struct {
	Timeout time.Duration
}

// NewExecCommandRunner creates a new ExecCommandRunner.
func NewExecCommandRunner(timeout time.Duration) *ExecCommandRunner {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &ExecCommandRunner{Timeout: timeout}
}

// Run executes a command and returns trimmed stdout or an error containing stderr.
func (r *ExecCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return trimmed, fmt.Errorf("command '%s %s' failed: %w (output: %s)", name, strings.Join(args, " "), err, trimmed)
	}
	return trimmed, nil
}
