package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v2"
)

// realARMJobsClient wraps armappcontainers.JobsClient to satisfy ARMJobsClient.
type realARMJobsClient struct {
	jobs *armappcontainers.JobsClient
}

// NewRealARMJobsClient constructs an ARMJobsClient backed by the Azure SDK.
func NewRealARMJobsClient(subscriptionID string, cred azcore.TokenCredential) (ARMJobsClient, error) {
	jobs, err := armappcontainers.NewJobsClient(subscriptionID, cred, &arm.ClientOptions{})
	if err != nil {
		return nil, fmt.Errorf("armappcontainers.NewJobsClient: %w", err)
	}
	return &realARMJobsClient{jobs: jobs}, nil
}

// BeginStartExecution starts a Container Apps Job execution and polls until completion,
// returning the execution name.
func (c *realARMJobsClient) BeginStartExecution(ctx context.Context, resourceGroup, jobName string, spec JobExecutionTemplate, _ interface{}) (string, error) {
	template := toARMTemplate(spec)
	poller, err := c.jobs.BeginStart(ctx, resourceGroup, jobName, &armappcontainers.JobsClientBeginStartOptions{
		Template: &template,
	})
	if err != nil {
		return "", fmt.Errorf("BeginStart: %w", err)
	}
	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("PollUntilDone: %w", err)
	}
	if resp.Name == nil {
		return "", fmt.Errorf("ARM response missing execution name")
	}
	return *resp.Name, nil
}

// toARMTemplate converts our neutral JobExecutionTemplate into the ARM SDK type.
func toARMTemplate(spec JobExecutionTemplate) armappcontainers.JobExecutionTemplate {
	envVars := make([]*armappcontainers.EnvironmentVar, 0, len(spec.Env))
	for _, kv := range spec.Env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		name := k
		value := v
		envVars = append(envVars, &armappcontainers.EnvironmentVar{
			Name:  &name,
			Value: &value,
		})
	}

	args := make([]*string, len(spec.ContainerArgs))
	for i, a := range spec.ContainerArgs {
		s := a
		args[i] = &s
	}

	container := &armappcontainers.JobExecutionContainer{
		Args: args,
		Env:  envVars,
	}

	return armappcontainers.JobExecutionTemplate{
		Containers: []*armappcontainers.JobExecutionContainer{container},
	}
}
