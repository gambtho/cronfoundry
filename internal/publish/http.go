package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

type httpPub struct {
	client *http.Client
}

// NewHTTPPublisher returns a Publisher that POSTs (or uses the configured method)
// to an arbitrary HTTP endpoint.
func NewHTTPPublisher() Publisher {
	return &httpPub{client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *httpPub) Type() string { return "http" }

func (p *httpPub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.HTTP
	if d == nil || d.URL == "" {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("http: url required")}
	}

	method := d.Method
	if method == "" {
		method = http.MethodPost
	}

	var bodyBytes []byte
	if d.BodyTemplate != "" {
		rendered, _ := template.Render(d.BodyTemplate, tctx)
		bodyBytes = []byte(rendered)
	} else {
		b, err := json.Marshal(map[string]string{"output": output})
		if err != nil {
			return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("http: marshal: %w", err)}
		}
		bodyBytes = b
	}

	req, err := http.NewRequestWithContext(ctx, method, d.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("http: build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range d.Headers {
		req.Header.Set(k, v)
	}
	if d.Secret != "" {
		tok, err := secrets.Get(d.Secret)
		if err != nil {
			return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("http: resolve secret: %w", err)}
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("http: %w", err)}
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return Result{Type: p.Type(), OK: true, Detail: fmt.Sprintf("http %d", resp.StatusCode)}
	}
	return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("http: status %d", resp.StatusCode), Detail: fmt.Sprintf("http %d", resp.StatusCode)}
}
