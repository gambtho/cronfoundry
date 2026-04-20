// Package azure provides an Azure Container Apps Jobs implementation of cloud.JobDispatcher.
package azure

import "context"

// JobExecution captures the parameters of one dispatched job — used by fakeARMClient in tests.
type JobExecution struct {
	ResourceGroup string
	JobName       string
	Template      JobExecutionTemplate
}

// JobExecutionTemplate is the minimal shape passed to the ARM API.
type JobExecutionTemplate struct {
	ContainerArgs []string
	Env           []string
}

// ARMJobsClient is the testability seam over armappcontainers job execution.
type ARMJobsClient interface {
	BeginStartExecution(ctx context.Context, resourceGroup, jobName string, spec JobExecutionTemplate, opts interface{}) (string, error)
}
