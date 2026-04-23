package cloud

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipOnWindows skips the test if running on Windows; these tests rely on
// POSIX binaries (true, false, sleep).
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only test")
	}
}

func TestSubprocessDispatcher_RunsAndWaits(t *testing.T) {
	skipOnWindows(t)
	bin, err := exec.LookPath("true")
	require.NoError(t, err)

	d := NewSubprocessDispatcher()
	h, err := d.Dispatch(context.Background(), DispatchRequest{BinaryPath: bin})
	require.NoError(t, err)
	require.Positive(t, h.PID())
	require.NoError(t, h.Wait())
}

func TestSubprocessDispatcher_NonZeroExitSurfacesError(t *testing.T) {
	skipOnWindows(t)
	bin, err := exec.LookPath("false")
	require.NoError(t, err)

	d := NewSubprocessDispatcher()
	h, err := d.Dispatch(context.Background(), DispatchRequest{BinaryPath: bin})
	require.NoError(t, err)
	err = h.Wait()
	require.Error(t, err)
}

func TestSubprocessDispatcher_KillTerminates(t *testing.T) {
	skipOnWindows(t)
	bin, err := exec.LookPath("sleep")
	require.NoError(t, err)

	d := NewSubprocessDispatcher()
	h, err := d.Dispatch(context.Background(), DispatchRequest{
		BinaryPath: bin,
		Args:       []string{"10"},
	})
	require.NoError(t, err)

	// Let sleep start up.
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, h.Kill())
	err = h.Wait()
	require.Error(t, err, "expected non-nil exit after Kill")
}

func TestSubprocessDispatcher_PropagatesEnv(t *testing.T) {
	skipOnWindows(t)
	// Use `sh -c 'test "$CRONFOUNDRY_T5_TEST" = "hello"'` to verify env.
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)

	d := NewSubprocessDispatcher()
	h, err := d.Dispatch(context.Background(), DispatchRequest{
		BinaryPath: sh,
		Args:       []string{"-c", `test "$CRONFOUNDRY_T5_TEST" = "hello"`},
		Env:        []string{"CRONFOUNDRY_T5_TEST=hello"},
	})
	require.NoError(t, err)
	require.NoError(t, h.Wait())

	// Negative: spec without the env should fail the test command.
	h2, err := d.Dispatch(context.Background(), DispatchRequest{
		BinaryPath: sh,
		Args:       []string{"-c", `test "$CRONFOUNDRY_T5_TEST" = "hello"`},
	})
	require.NoError(t, err)
	assert.Error(t, h2.Wait())
}

func TestSubprocessDispatcher_BadBinary(t *testing.T) {
	d := NewSubprocessDispatcher()
	_, err := d.Dispatch(context.Background(), DispatchRequest{
		BinaryPath: "/nonexistent/path/definitely/not/here",
	})
	require.Error(t, err)
}
