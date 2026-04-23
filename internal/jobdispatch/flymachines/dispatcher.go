package flymachines

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gambtho/cronfoundry/internal/cloud"
)

var ErrWaitNotImplemented = errors.New("flymachines: Wait not implemented: observe via flyctl")
var ErrKillNotImplemented = errors.New("flymachines: Kill not implemented: stop via flyctl")

// Config holds static parameters for the Fly Machines dispatcher.
type Config struct {
	App   string // Fly app name for the runner (pre-created)
	Image string // Registry image reference
}

// Dispatcher implements cloud.JobDispatcher using Fly Machines.
type Dispatcher struct {
	client FlyClient
	cfg    Config
}

// NewDispatcher returns a Dispatcher. Panics if client is nil.
func NewDispatcher(client FlyClient, cfg Config) *Dispatcher {
	if client == nil {
		panic("flymachines: NewDispatcher: client must not be nil")
	}
	return &Dispatcher{client: client, cfg: cfg}
}

// Compile-time interface check.
var _ cloud.JobDispatcher = (*Dispatcher)(nil)

// Dispatch creates an auto-destroy Fly Machine and returns immediately.
func (d *Dispatcher) Dispatch(ctx context.Context, req cloud.DispatchRequest) (cloud.Handle, error) {
	env := make(map[string]string, len(req.Env))
	for _, kv := range req.Env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("flymachines: malformed env entry %q (missing '=')", kv)
		}
		env[k] = v
	}
	mr := CreateMachineRequest{
		AppName:     d.cfg.App,
		Image:       d.cfg.Image,
		AutoDestroy: true,
		Args:        append([]string{}, req.Args...),
		Env:         env,
	}
	if err := d.client.CreateMachine(ctx, mr); err != nil {
		return nil, fmt.Errorf("flymachines: dispatch: %w", err)
	}
	return &flyHandle{}, nil
}

type flyHandle struct{}

var _ cloud.Handle = (*flyHandle)(nil)

func (h *flyHandle) PID() int    { return 0 }
func (h *flyHandle) Wait() error { return ErrWaitNotImplemented }
func (h *flyHandle) Kill() error { return ErrKillNotImplemented }
