package exec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/patricksign/AgentClaw/internal/port"
)

// maxOutputBytes caps stdout/stderr buffer size to prevent OOM from verbose commands.
const maxOutputBytes = 10 * 1024 * 1024 // 10 MiB

// limitedWriter wraps a bytes.Buffer and stops writing after max bytes.
// Excess data is silently discarded to prevent OOM.
type limitedWriter struct {
	buf *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.max - w.buf.Len()
	if remaining <= 0 {
		return len(p), nil // discard, report success
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	return w.buf.Write(p)
}

// Compile-time check: OSRunner implements port.CommandRunner.
var _ port.CommandRunner = (*OSRunner)(nil)

// defaultTimeout caps any single command execution.
const defaultTimeout = 5 * time.Minute

// OSRunner executes commands via os/exec.
// Security: uses exec.CommandContext with separated args — no shell interpolation.
type OSRunner struct {
	timeout time.Duration
}

// NewOSRunner creates a runner with the given per-command timeout.
// If timeout <= 0, defaults to 5 minutes.
func NewOSRunner(timeout time.Duration) *OSRunner {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &OSRunner{timeout: timeout}
}

// Run executes a command in the given workDir and returns its output.
// Binary and args are passed separately to exec.CommandContext — never
// concatenated into a shell string, preventing command injection.
func (r *OSRunner) Run(ctx context.Context, workDir string, binary string, args ...string) (port.CommandResult, error) {
	if binary == "" {
		return port.CommandResult{ExitCode: -1}, fmt.Errorf("exec: empty binary name")
	}

	// Enforce timeout.
	execCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, binary, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, max: maxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderr, max: maxOutputBytes}

	err := cmd.Run()

	result := port.CommandResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: 0,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
			return result, fmt.Errorf("exec %s: %w", binary, err)
		}
	}

	return result, nil
}
