package port

import "context"

// CommandResult holds the outcome of a command execution.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// CommandRunner abstracts shell command execution.
// Implementations must use exec.CommandContext (never sh -c) and enforce timeouts.
type CommandRunner interface {
	// Run executes a command and returns its output.
	// The binary and args are passed separately — no shell interpolation.
	Run(ctx context.Context, workDir string, binary string, args ...string) (CommandResult, error)
}
