package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubBin is lazily built once per test binary run.
var (
	stubBinOnce sync.Once
	stubBinPath string
	stubBinErr  error
)

func stub(t *testing.T) string {
	t.Helper()
	stubBinOnce.Do(func() {
		if testing.Short() {
			stubBinErr = nil
			return
		}
		dir, err := os.MkdirTemp("", "mcp-stub-")
		if err != nil {
			stubBinErr = err
			return
		}
		out := filepath.Join(dir, "mcp-stub")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		repoRoot := findRepoRoot(t)
		cmd := exec.Command("go", "build", "-o", out, "./testdata/mcp-fixtures/stub-server")
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if err := cmd.Run(); err != nil {
			stubBinErr = err
			return
		}
		stubBinPath = out
	})
	if stubBinErr != nil {
		t.Fatalf("build stub: %v", stubBinErr)
	}
	if stubBinPath == "" {
		t.Skip("stub not built")
	}
	return stubBinPath
}

// findRepoRoot walks up from the current working directory until it finds go.mod.
func findRepoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above cwd")
		}
		dir = parent
	}
}

func TestClient_InitializeAndListTools(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	cmd := exec.Command(bin)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := newClient(stdout, stdin)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, c.initialize(ctx))
	tools, err := c.listTools(ctx)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name)
}

func TestClient_CallTool_Success(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	cmd := exec.Command(bin)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := newClient(stdout, stdin)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, c.initialize(ctx))

	result, isErr, err := c.callTool(ctx, "echo", json.RawMessage(`{"a":1}`))
	require.NoError(t, err)
	assert.False(t, isErr)
	assert.Contains(t, string(result), "ok")
}

func TestClient_CallTool_ServerError(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "MCP_STUB_RETURN_ERROR=1")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := newClient(stdout, stdin)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, c.initialize(ctx))

	_, isErr, err := c.callTool(ctx, "echo", json.RawMessage(`{}`))
	require.NoError(t, err) // tool-level errors are not Go errors
	assert.True(t, isErr)
}

func TestClient_InitializeFail_ServerExits(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "MCP_STUB_EXIT_ON_INIT=1")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := newClient(stdout, stdin)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.Error(t, c.initialize(ctx))
}

func TestClient_CallTool_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	cmd := exec.Command(bin)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := newClient(stdout, stdin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.initialize(ctx))

	const N = 20
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := c.callTool(ctx, "echo", json.RawMessage(`{}`))
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		assert.NoError(t, err, "call %d", i)
	}
}
