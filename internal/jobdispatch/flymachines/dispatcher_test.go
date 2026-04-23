package flymachines_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/cloud"
	"github.com/gambtho/cronfoundry/internal/jobdispatch/flymachines"
)

type fakeFlyClient struct {
	created []flymachines.CreateMachineRequest
	err     error
}

func (f *fakeFlyClient) CreateMachine(ctx context.Context, req flymachines.CreateMachineRequest) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, req)
	return nil
}

func TestDispatch_CreatesMachine(t *testing.T) {
	fake := &fakeFlyClient{}
	cfg := flymachines.Config{
		App:   "cronfoundry-runner",
		Image: "registry.fly.io/cronfoundry-runner:v1.0.0",
	}
	d := flymachines.NewDispatcher(fake, cfg)

	req := cloud.DispatchRequest{
		Args: []string{"runner"},
		Env:  []string{"RUN_ID=abc123", "SCHEDULE_ID=s1"},
	}
	h, err := d.Dispatch(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Len(t, fake.created, 1)

	m := fake.created[0]
	require.Equal(t, "cronfoundry-runner", m.AppName)
	require.Equal(t, "registry.fly.io/cronfoundry-runner:v1.0.0", m.Image)
	require.True(t, m.AutoDestroy)
	require.Equal(t, []string{"runner"}, m.Args)
	require.Equal(t, map[string]string{"RUN_ID": "abc123", "SCHEDULE_ID": "s1"}, m.Env)
}

func TestDispatch_PropagatesClientError(t *testing.T) {
	fake := &fakeFlyClient{err: context.DeadlineExceeded}
	cfg := flymachines.Config{App: "app", Image: "img:v1"}
	d := flymachines.NewDispatcher(fake, cfg)

	_, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestNewDispatcher_NilClientPanics(t *testing.T) {
	require.Panics(t, func() {
		flymachines.NewDispatcher(nil, flymachines.Config{})
	})
}

func TestDispatch_MalformedEnvReturnsError(t *testing.T) {
	fake := &fakeFlyClient{}
	d := flymachines.NewDispatcher(fake, flymachines.Config{App: "app", Image: "img:v1"})
	_, err := d.Dispatch(context.Background(), cloud.DispatchRequest{
		Env: []string{"MALFORMED_NO_EQUALS"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "malformed env entry")
}

func TestHandle_PIDIsZero(t *testing.T) {
	fake := &fakeFlyClient{}
	d := flymachines.NewDispatcher(fake, flymachines.Config{App: "app", Image: "img:v1"})
	h, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.NoError(t, err)
	require.Equal(t, 0, h.PID())
}

func TestHandle_WaitReturnsErrNotImplemented(t *testing.T) {
	fake := &fakeFlyClient{}
	d := flymachines.NewDispatcher(fake, flymachines.Config{App: "app", Image: "img:v1"})
	h, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.NoError(t, err)
	require.ErrorIs(t, h.Wait(), flymachines.ErrWaitNotImplemented)
}

func TestHandle_KillReturnsErrNotImplemented(t *testing.T) {
	fake := &fakeFlyClient{}
	d := flymachines.NewDispatcher(fake, flymachines.Config{App: "app", Image: "img:v1"})
	h, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.NoError(t, err)
	require.ErrorIs(t, h.Kill(), flymachines.ErrKillNotImplemented)
}
