// Package supervisor manages the lifecycle of individual FFmpeg client processes.
package supervisor

// State represents the current state of a supervised client.
type State int

const (
	// StateCreated is the initial state before the client has started.
	StateCreated State = iota

	// StateStarting indicates the client process is being spawned.
	StateStarting

	// StateRunning indicates the client process is actively running.
	StateRunning

	// StateBackoff indicates the client is waiting before restart.
	StateBackoff

	// StateStopped indicates the client has been permanently stopped.
	StateStopped
)

// String returns a human-readable name for the state.
func (s State) String() string {
	switch s {
	case StateCreated:
		return "created"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateBackoff:
		return "backoff"
	case StateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// IsActive returns true if the state represents an active client
// (either running or in the process of starting/restarting).
func (s State) IsActive() bool {
	return s == StateStarting || s == StateRunning || s == StateBackoff
}

// IsTerminal returns true if the state is a terminal state (stopped).
func (s State) IsTerminal() bool {
	return s == StateStopped
}
