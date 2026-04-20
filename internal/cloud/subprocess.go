package cloud

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// SubprocessDispatcher runs DispatchSpec jobs via os/exec. This is the P2
// localhost implementation of JobDispatcher.
type SubprocessDispatcher struct{}

// NewSubprocessDispatcher returns a ready-to-use dispatcher.
func NewSubprocessDispatcher() *SubprocessDispatcher { return &SubprocessDispatcher{} }

// Compile-time interface check.
var _ JobDispatcher = (*SubprocessDispatcher)(nil)

// Dispatch starts the job and returns a Handle.
func (d *SubprocessDispatcher) Dispatch(ctx context.Context, spec DispatchSpec) (Handle, error) {
	cmd := exec.CommandContext(ctx, spec.BinaryPath, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	// Runner stdout/stderr are routed to our own stdout/stderr so they
	// integrate with the parent's slog pipeline (which has the run-scoped
	// redactor applied).
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cloud: subprocess: start %s: %w", spec.BinaryPath, err)
	}
	return &subprocessHandle{cmd: cmd}, nil
}

type subprocessHandle struct {
	cmd *exec.Cmd
}

// Compile-time interface check.
var _ Handle = (*subprocessHandle)(nil)

func (h *subprocessHandle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *subprocessHandle) Wait() error {
	return h.cmd.Wait()
}

func (h *subprocessHandle) Kill() error {
	if h.cmd.Process == nil {
		return nil
	}
	return h.cmd.Process.Kill()
}
