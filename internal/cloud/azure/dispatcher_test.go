package azure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/cloud"
	cloudazure "github.com/gambtho/cronfoundry/internal/cloud/azure"
)

type fakeARMClient struct {
	dispatched []cloudazure.JobExecution
	err        error
}

func (f *fakeARMClient) BeginStartExecution(ctx context.Context, resourceGroup, jobName string, spec cloudazure.JobExecutionTemplate, opts interface{}) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.dispatched = append(f.dispatched, cloudazure.JobExecution{
		ResourceGroup: resourceGroup,
		JobName:       jobName,
		Template:      spec,
	})
	return "exec-001", nil
}

func TestContainerAppsJobDispatcher_DispatchCreatesExecution(t *testing.T) {
	fake := &fakeARMClient{}
	d := cloudazure.NewContainerAppsJobDispatcher(fake, "rg-test", "cronfoundry-runner")

	spec := cloud.DispatchSpec{
		BinaryPath: "/usr/local/bin/cronfoundry",
		Args:       []string{"runner"},
		Env:        []string{"RUN_ID=abc123"},
	}
	h, err := d.Dispatch(context.Background(), spec)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Equal(t, 0, h.PID())
	require.Len(t, fake.dispatched, 1)
	require.Equal(t, "rg-test", fake.dispatched[0].ResourceGroup)
	require.Equal(t, "cronfoundry-runner", fake.dispatched[0].JobName)
	// Verify args and env forwarded
	require.Equal(t, []string{"runner"}, fake.dispatched[0].Template.ContainerArgs)
	require.Equal(t, []string{"RUN_ID=abc123"}, fake.dispatched[0].Template.Env)
}

func TestContainerAppsJobDispatcher_HandleWait_IsNoOp(t *testing.T) {
	fake := &fakeARMClient{}
	d := cloudazure.NewContainerAppsJobDispatcher(fake, "rg-test", "cronfoundry-runner")
	spec := cloud.DispatchSpec{BinaryPath: "/usr/local/bin/cronfoundry", Args: []string{"runner"}}
	h, err := d.Dispatch(context.Background(), spec)
	require.NoError(t, err)
	require.NoError(t, h.Wait())
}

func TestContainerAppsJobDispatcher_HandleKill_IsNoOp(t *testing.T) {
	fake := &fakeARMClient{}
	d := cloudazure.NewContainerAppsJobDispatcher(fake, "rg-test", "cronfoundry-runner")
	spec := cloud.DispatchSpec{BinaryPath: "/usr/local/bin/cronfoundry", Args: []string{"runner"}}
	h, err := d.Dispatch(context.Background(), spec)
	require.NoError(t, err)
	require.NoError(t, h.Kill())
}

func TestContainerAppsJobDispatcher_DispatchReturnsError(t *testing.T) {
	fake := &fakeARMClient{err: errors.New("azure: quota exceeded")}
	d := cloudazure.NewContainerAppsJobDispatcher(fake, "rg-test", "cronfoundry-runner")
	spec := cloud.DispatchSpec{BinaryPath: "/usr/local/bin/cronfoundry", Args: []string{"runner"}}
	_, err := d.Dispatch(context.Background(), spec)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cronfoundry-runner")
}
