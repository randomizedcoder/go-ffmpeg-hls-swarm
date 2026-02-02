// Package process provides abstractions for running external processes.
package process

import (
	"context"
	"os/exec"
)

// Runner creates executable commands for clients.
// This interface allows the supervisor to be process-agnostic.
type Runner interface {
	// BuildCommand returns a ready-to-start command for the given client.
	// The command should NOT be started yet.
	BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error)

	// Name returns a human-readable name for this process type.
	Name() string
}

// Result captures the outcome of a process execution.
type Result struct {
	ClientID  int
	ExitCode  int
	StartTime int64 // Unix timestamp
	EndTime   int64 // Unix timestamp
	Error     error
}
