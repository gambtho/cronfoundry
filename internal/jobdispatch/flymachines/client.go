// Package flymachines implements cloud.JobDispatcher using the Fly Machines API.
package flymachines

import "context"

// CreateMachineRequest is the neutral shape for creating a Fly Machine.
type CreateMachineRequest struct {
	AppName     string
	Image       string
	AutoDestroy bool
	Args        []string
	Env         map[string]string
}

// FlyClient is the testability seam over the Fly Machines REST API.
type FlyClient interface {
	CreateMachine(ctx context.Context, req CreateMachineRequest) error
}
