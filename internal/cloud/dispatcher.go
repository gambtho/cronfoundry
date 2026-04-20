// Package cloud defines pluggable interfaces for cloud-specific concerns —
// job dispatch, secret storage, and identity. P2 ships localhost
// implementations only; P4 will add Azure variants behind the same interfaces.
package cloud

import (
	"context"
)

// DispatchSpec describes a single job to run.
type DispatchSpec struct {
	// BinaryPath is the absolute path to the runner binary to execute.
	BinaryPath string
	// Args are passed positionally after the binary path.
	Args []string
	// Env contains additional environment variables, formatted as "KEY=VALUE".
	// Entries are appended to the process's current environment.
	Env []string
}

// Handle is returned by Dispatch so the caller can observe / supervise the
// running job. In the P2 subprocess implementation this wraps *os.Process;
// in P4 it wraps an Azure Container Apps Job execution.
type Handle interface {
	// PID returns the OS process identifier for the running job, or 0 if the
	// underlying executor doesn't expose one.
	PID() int
	// Wait blocks until the job terminates. Returns nil for exit code 0,
	// non-nil for non-zero exit or signal-termination.
	Wait() error
	// Kill terminates the job. Best-effort; returns any error from the OS.
	Kill() error
}

// JobDispatcher dispatches one-shot jobs and returns a Handle for supervision.
type JobDispatcher interface {
	Dispatch(ctx context.Context, spec DispatchSpec) (Handle, error)
}
