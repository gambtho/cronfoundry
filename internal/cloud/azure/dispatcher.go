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
func NewContainerAppsJobDispatcher(client ARMJobsClient, resourceGroup, jobName string) *ContainerAppsJobDispatcher {
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
	executionName, err := d.client.BeginStartExecution(ctx, d.resourceGroup, d.jobName, tmpl, nil)
	if err != nil {
		return nil, fmt.Errorf("cloud/azure: dispatch job %s: %w", d.jobName, err)
	}
	return &containerAppsHandle{executionName: executionName}, nil
}

type containerAppsHandle struct {
	executionName string
}

var _ cloud.Handle = (*containerAppsHandle)(nil)

// PID returns 0 — Azure Container Apps Jobs have no OS process identifier.
func (h *containerAppsHandle) PID() int { return 0 }

// Wait is a no-op for MVP — job outcomes are observed via Log Analytics.
func (h *containerAppsHandle) Wait() error { return nil }

// Kill is not supported for Container Apps Jobs in MVP.
func (h *containerAppsHandle) Kill() error { return nil }
