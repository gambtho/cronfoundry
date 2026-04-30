// Package azure implements the `cronfoundry bootstrap azure` interactive
// installer.
package azure

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Runner abstracts shell-outs so tests can stub them.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	RunStreaming(ctx context.Context, name string, args ...string) error
	RunWithEnv(ctx context.Context, env []string, name string, args ...string) error
}

// ExecRunner is the production Runner, backed by os/exec.
type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

func (r *ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func (r *ExecRunner) RunStreaming(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return cmd.Run()
}

func (r *ExecRunner) RunWithEnv(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return cmd.Run()
}

// MockRunner is a test double recording calls and returning canned responses.
type MockRunner struct {
	Responses []MockResponse
	Calls     []MockCall
	EnvCalls  []MockEnvCall
	Stdout    io.Writer
}

type MockResponse struct {
	Stdout []byte
	Err    error
}

type MockCall struct {
	Name string
	Args []string
}

type MockEnvCall struct {
	Name string
	Args []string
	Env  []string
}

func (m *MockRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	m.Calls = append(m.Calls, MockCall{Name: name, Args: args})
	if len(m.Responses) == 0 {
		return nil, fmt.Errorf("MockRunner: unexpected call to %q %v", name, args)
	}
	r := m.Responses[0]
	m.Responses = m.Responses[1:]
	return r.Stdout, r.Err
}

func (m *MockRunner) RunStreaming(ctx context.Context, name string, args ...string) error {
	out, err := m.Run(ctx, name, args...)
	if m.Stdout != nil && out != nil {
		_, _ = m.Stdout.Write(out)
	}
	return err
}

func (m *MockRunner) RunWithEnv(_ context.Context, env []string, name string, args ...string) error {
	m.EnvCalls = append(m.EnvCalls, MockEnvCall{Name: name, Args: args, Env: env})
	if len(m.Responses) == 0 {
		return fmt.Errorf("MockRunner: unexpected call to %q %v", name, args)
	}
	r := m.Responses[0]
	m.Responses = m.Responses[1:]
	return r.Err
}
