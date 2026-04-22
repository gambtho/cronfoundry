package mcp

import (
	"io"
	"os/exec"
	"syscall"
	"time"
)

// shutdownGracePeriod bounds how long we wait for SIGTERM before SIGKILL.
// Exported for tests and for alignment with the spec's MCPShutdownGracePeriod.
const shutdownGracePeriod = 5 * time.Second

// serverProcess bundles an exec.Cmd with its stdio pipes for client use.
type serverProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func startServerProcess(command string, args, env []string) (*serverProcess, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = env
	// SIGTERM is sent to the process group to catch Node/Python children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &serverProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

// shutdown first closes stdin (many MCP servers exit cleanly on EOF),
// then SIGTERMs the process group, waits up to shutdownGracePeriod, and
// SIGKILLs if needed. Returns the exit code (or -1 if forced).
func (sp *serverProcess) shutdown() int {
	_ = sp.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- sp.cmd.Wait() }()

	select {
	case <-done:
		return sp.cmd.ProcessState.ExitCode()
	case <-time.After(200 * time.Millisecond):
		// Most well-behaved servers exit on stdin EOF; give them a hair of
		// additional time before escalating.
	}

	// SIGTERM the process group.
	if pid := sp.cmd.Process.Pid; pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
	select {
	case <-done:
		return sp.cmd.ProcessState.ExitCode()
	case <-time.After(shutdownGracePeriod):
	}

	// SIGKILL.
	if pid := sp.cmd.Process.Pid; pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	<-done
	return -1
}
