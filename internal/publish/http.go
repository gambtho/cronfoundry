package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

type httpPub struct {
	http *http.Client
}

// NewHTTPPublisher returns a Publisher that POSTs (or uses the configured method)
// to an arbitrary HTTP endpoint.
func NewHTTPPublisher() Publisher {
	return &httpPub{http: &http.Client{Timeout: 30 * time.Second}}
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
	var warns []string
	if d.BodyTemplate != "" {
		rendered, w := template.Render(d.BodyTemplate, tctx)
		warns = w
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
	// Only default Content-Type to application/json for the default JSON envelope;
	// when a body_template is set the caller controls the content type via headers.
	if d.BodyTemplate == "" {
		req.Header.Set("Content-Type", "application/json")
	}
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

	resp, err := p.http.Do(req)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("http: %w", err)}
	}
	// Read a bounded preview for non-2xx detail, then drain the rest so the
	// connection can be returned to the pool for reuse.
	var snippet string
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	if n > 0 {
		snippet = strings.TrimSpace(string(buf[:n]))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		detail := fmt.Sprintf("http %d", resp.StatusCode)
		if len(warns) > 0 {
			detail += "; template warnings: " + strings.Join(warns, ", ")
		}
		return Result{Type: p.Type(), OK: true, Detail: detail}
	}
	detail := fmt.Sprintf("http %d", resp.StatusCode)
	if snippet != "" {
		detail += ": " + snippet
	}
	return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("http: status %d", resp.StatusCode), Detail: detail}
}
