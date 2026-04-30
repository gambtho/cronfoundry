// internal/bootstrap/azure/health.go
package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// WaitHealthy polls https://<fqdn>/healthz until 200 OK or timeout.
func WaitHealthy(ctx context.Context, fqdn string, timeout time.Duration) error {
	return waitHealthyAt(ctx, "https", fqdn, timeout, 5*time.Second)
}

func waitHealthyAt(ctx context.Context, scheme, host string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("%s://%s/healthz", scheme, host)
	const perRequestCap = 5 * time.Second
	client := &http.Client{}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("timeout waiting for /healthz")
		}
		perReq := min(remaining, perRequestCap)
		reqCtx, cancel := context.WithTimeout(ctx, perReq)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				cancel()
				return nil
			}
		}
		cancel()
		if time.Now().After(deadline) {
			return errors.New("timeout waiting for /healthz")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
