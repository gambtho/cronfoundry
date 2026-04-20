package azure

import (
	"context"
	"fmt"

	"github.com/gambtho/cronfoundry/internal/cloud"
)

// ContainerAppsJobDispatcher implements cloud.JobDispatcher using Azure Container Apps Jobs.
type ContainerAppsJobDispatcher struct {
	client        ARMJobsClient
	resourceGroup string
	jobName       string
}

// NewContainerAppsJobDispatcher returns a dispatcher targeting the named Container Apps Job.
// It panics if client is nil.
func NewContainerAppsJobDispatcher(client ARMJobsClient, resourceGroup, jobName string) *ContainerAppsJobDispatcher {
	if client == nil {
		panic("cloud/azure: NewContainerAppsJobDispatcher: client must not be nil")
	}
	return &ContainerAppsJobDispatcher{
		client:        client,
		resourceGroup: resourceGroup,
		jobName:       jobName,
	}
}

// Compile-time interface check.
var _ cloud.JobDispatcher = (*ContainerAppsJobDispatcher)(nil)

// Dispatch fires a Container Apps Job execution. BinaryPath is ignored — the job image
// is configured in Azure; only Args and Env are forwarded.
func (d *ContainerAppsJobDispatcher) Dispatch(ctx context.Context, spec cloud.DispatchSpec) (cloud.Handle, error) {
	tmpl := JobExecutionTemplate{
		ContainerArgs: append([]string{}, spec.Args...),
		Env:           append([]string{}, spec.Env...),
	}
	executionName, err := d.client.BeginStartExecution(ctx, d.resourceGroup, d.jobName, tmpl)
	if err != nil {
		return nil, fmt.Errorf("cloud/azure: dispatch job %s: %w", d.jobName, err)
	}
	return &containerAppsHandle{executionName: executionName}, nil
}

type containerAppsHandle struct {
	// executionName is the ARM execution identifier returned by BeginStartExecution.
	// Retained for a future Wait implementation that polls job status.
	executionName string
}

var _ cloud.Handle = (*containerAppsHandle)(nil)

// PID returns 0 — Azure Container Apps Jobs have no OS process identifier.
func (h *containerAppsHandle) PID() int { return 0 }

// Wait is not implemented for Azure Container Apps Jobs.
// Observe job outcomes via Log Analytics or the ARM jobs API.
func (h *containerAppsHandle) Wait() error {
	return fmt.Errorf("cloud/azure: Wait not implemented: observe execution %q via Log Analytics", h.executionName)
}

// Kill is not implemented for Azure Container Apps Jobs.
// Cancel via the Azure Portal or `az containerapp job stop`.
func (h *containerAppsHandle) Kill() error {
	return fmt.Errorf("cloud/azure: Kill not implemented for execution %q", h.executionName)
}
