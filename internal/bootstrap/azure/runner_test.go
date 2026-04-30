// internal/bootstrap/azure/runner_test.go
package azure

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMockRunner_RecordsCallsAndReturnsCanned(t *testing.T) {
	mr := &MockRunner{
		Responses: []MockResponse{
			{Stdout: []byte("ok\n")},
			{Err: errors.New("nope")},
		},
	}
	out, err := mr.Run(context.Background(), "az", "account", "show")
	require.NoError(t, err)
	require.Equal(t, "ok\n", string(out))

	_, err = mr.Run(context.Background(), "az", "deployment", "sub", "create")
	require.EqualError(t, err, "nope")

	require.Len(t, mr.Calls, 2)
	require.Equal(t, "az", mr.Calls[0].Name)
	require.Equal(t, []string{"account", "show"}, mr.Calls[0].Args)
}

func TestMockRunner_RunStreaming_WritesStdoutToWriter(t *testing.T) {
	var buf bytes.Buffer
	mr := &MockRunner{
		Responses: []MockResponse{{Stdout: []byte("streamed\n")}},
		Stdout:    &buf,
	}
	err := mr.RunStreaming(context.Background(), "az", "x")
	require.NoError(t, err)
	require.True(t, strings.Contains(buf.String(), "streamed"))
}

func TestMockRunner_RunWithEnv_RecordsEnv(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	err := mr.RunWithEnv(context.Background(), []string{"K=V"}, "echo", "hi")
	require.NoError(t, err)
	require.Len(t, mr.EnvCalls, 1)
	require.Equal(t, []string{"K=V"}, mr.EnvCalls[0].Env)
	require.Equal(t, "echo", mr.EnvCalls[0].Name)
}
