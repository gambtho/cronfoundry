package flymachines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const flyMachinesBaseURL = "https://api.machines.dev/v1"

// RealFlyClient calls the Fly Machines REST API.
type RealFlyClient struct {
	apiToken string
	baseURL  string
	http     *http.Client
}

// NewRealFlyClient returns a FlyClient backed by the Fly Machines API.
func NewRealFlyClient(apiToken string) *RealFlyClient {
	return &RealFlyClient{
		apiToken: apiToken,
		baseURL:  flyMachinesBaseURL,
		http:     &http.Client{},
	}
}

// Compile-time interface check.
var _ FlyClient = (*RealFlyClient)(nil)

type flyCreateMachineBody struct {
	Config flyMachineConfig `json:"config"`
}

type flyMachineConfig struct {
	Image       string            `json:"image"`
	AutoDestroy bool              `json:"auto_destroy"`
	Env         map[string]string `json:"env,omitempty"`
	Init        flyMachineInit    `json:"init,omitempty"`
}

type flyMachineInit struct {
	Cmd []string `json:"cmd,omitempty"`
}

// CreateMachine calls POST /v1/apps/{app}/machines with auto_destroy: true.
func (c *RealFlyClient) CreateMachine(ctx context.Context, req CreateMachineRequest) error {
	body := flyCreateMachineBody{
		Config: flyMachineConfig{
			Image:       req.Image,
			AutoDestroy: true,
			Env:         req.Env,
			Init:        flyMachineInit{Cmd: req.Args},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("flymachines: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/apps/%s/machines", c.baseURL, req.AppName)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("flymachines: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiToken)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("flymachines: POST machines: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("flymachines: POST machines: unexpected status %d", resp.StatusCode)
	}
	return nil
}
