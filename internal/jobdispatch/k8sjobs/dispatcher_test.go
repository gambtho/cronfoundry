package k8sjobs_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/cloud"
	"github.com/gambtho/cronfoundry/internal/jobdispatch/k8sjobs"
)

type fakeK8sClient struct {
	created []k8sjobs.JobSpec
	err     error
}

func (f *fakeK8sClient) CreateJob(ctx context.Context, spec k8sjobs.JobSpec) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, spec)
	return nil
}

func TestDispatch_CreatesJob(t *testing.T) {
	fake := &fakeK8sClient{}
	cfg := k8sjobs.Config{
		Namespace:      "cronfoundry",
		RunnerImage:    "ghcr.io/cronfoundry/runner:v1.0.0",
		ServiceAccount: "cf-runner",
	}
	d := k8sjobs.NewDispatcher(fake, cfg)

	req := cloud.DispatchRequest{
		Args: []string{"runner"},
		Env:  []string{"RUN_ID=abc123", "SCHEDULE_ID=s1"},
	}
	h, err := d.Dispatch(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Len(t, fake.created, 1)

	job := fake.created[0]
	require.NotEmpty(t, job.Name)
	require.Equal(t, "cronfoundry", job.Namespace)
	require.Equal(t, "ghcr.io/cronfoundry/runner:v1.0.0", job.Image)
	require.Equal(t, "cf-runner", job.ServiceAccount)
	require.Equal(t, []string{"runner"}, job.Args)
	require.Equal(t, []string{"RUN_ID=abc123", "SCHEDULE_ID=s1"}, job.Env)
}

func TestDispatch_PropagatesClientError(t *testing.T) {
	fake := &fakeK8sClient{err: context.DeadlineExceeded}
	cfg := k8sjobs.Config{Namespace: "cronfoundry", RunnerImage: "img:v1"}
	d := k8sjobs.NewDispatcher(fake, cfg)

	_, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestNewDispatcher_NilClientPanics(t *testing.T) {
	require.Panics(t, func() {
		k8sjobs.NewDispatcher(nil, k8sjobs.Config{})
	})
}

func TestHandle_PIDIsZero(t *testing.T) {
	fake := &fakeK8sClient{}
	d := k8sjobs.NewDispatcher(fake, k8sjobs.Config{Namespace: "ns", RunnerImage: "img:v1"})
	h, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.NoError(t, err)
	require.Equal(t, 0, h.PID())
}

func TestHandle_WaitReturnsErrNotImplemented(t *testing.T) {
	fake := &fakeK8sClient{}
	d := k8sjobs.NewDispatcher(fake, k8sjobs.Config{Namespace: "ns", RunnerImage: "img:v1"})
	h, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.NoError(t, err)
	require.ErrorIs(t, h.Wait(), k8sjobs.ErrWaitNotImplemented)
}

func TestHandle_KillReturnsErrNotImplemented(t *testing.T) {
	fake := &fakeK8sClient{}
	d := k8sjobs.NewDispatcher(fake, k8sjobs.Config{Namespace: "ns", RunnerImage: "img:v1"})
	h, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.NoError(t, err)
	require.ErrorIs(t, h.Kill(), k8sjobs.ErrKillNotImplemented)
}
