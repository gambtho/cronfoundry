package mcp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubServerEnv constructs a clean env for spawning the stub with the given
// behavior overrides.
func stubServerEnv(overrides map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

func TestManager_StartAndTools(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)

	mgr := NewManager(context.Background())
	defer mgr.Shutdown()

	require.NoError(t, mgr.Start("echo-server", bin, nil, stubServerEnv(nil)))
	tools := mgr.Tools("echo-server")
	require.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name)
}

func TestManager_DispatchAll_Success(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	mgr := NewManager(context.Background())
	defer mgr.Shutdown()
	require.NoError(t, mgr.Start("s", bin, nil, stubServerEnv(nil)))

	calls := []ToolUse{
		{ID: "1", Name: "s__echo", Input: json.RawMessage(`{"x":1}`)},
		{ID: "2", Name: "s__echo", Input: json.RawMessage(`{"y":2}`)},
	}
	results, fatal := mgr.DispatchAll(context.Background(), calls, 2*time.Second)
	require.Nil(t, fatal)
	require.Len(t, results, 2)
	for _, r := range results {
		assert.False(t, r.IsError)
	}
}

func TestManager_DispatchAll_ToolLevelError(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	mgr := NewManager(context.Background())
	defer mgr.Shutdown()
	require.NoError(t, mgr.Start("s", bin, nil, stubServerEnv(map[string]string{
		"MCP_STUB_RETURN_ERROR": "1",
	})))

	calls := []ToolUse{{ID: "1", Name: "s__echo", Input: json.RawMessage(`{}`)}}
	results, fatal := mgr.DispatchAll(context.Background(), calls, 2*time.Second)
	require.Nil(t, fatal, "tool-level error is NOT a fatal error")
	require.Len(t, results, 1)
	assert.True(t, results[0].IsError)
}

func TestManager_DispatchAll_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	mgr := NewManager(context.Background())
	defer mgr.Shutdown()
	require.NoError(t, mgr.Start("s", bin, nil, stubServerEnv(map[string]string{
		"MCP_STUB_SLEEP_MS": "5000",
	})))

	calls := []ToolUse{{ID: "1", Name: "s__echo", Input: json.RawMessage(`{}`)}}
	results, fatal := mgr.DispatchAll(context.Background(), calls, 100*time.Millisecond)
	_ = results // not examined on fatal
	require.NotNil(t, fatal)
	assert.Equal(t, "mcp_tool_timeout", fatal.Kind)
}

func TestManager_Start_InitializeFails(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	mgr := NewManager(context.Background())
	defer mgr.Shutdown()
	err := mgr.Start("s", bin, nil, stubServerEnv(map[string]string{
		"MCP_STUB_EXIT_ON_INIT": "1",
	}))
	require.Error(t, err)
}
