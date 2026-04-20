package publish

import (
	"context"
	"fmt"
	"sync"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

// Dispatcher fans destinations out to their respective Publishers in parallel,
// preserving input order in the returned Results slice.
type Dispatcher struct {
	Publishers map[string]Publisher
}

// Dispatch publishes `output` to each destination concurrently. Each Publisher
// is responsible for its own retries, secret resolution, and error wrapping.
// The returned slice has the same length and order as `dests`.
func (d *Dispatcher) Dispatch(ctx context.Context, dests []config.Destination, output string, tctx template.Context, secrets SecretGetter) []Result {
	results := make([]Result, len(dests))
	var wg sync.WaitGroup
	for i, dest := range dests {
		i, dest := i, dest
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = d.publishOne(ctx, dest, output, tctx, secrets)
		}()
	}
	wg.Wait()
	return results
}

func (d *Dispatcher) publishOne(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	typ, err := destType(dest)
	if err != nil {
		return Result{OK: false, Err: err}
	}
	p, ok := d.Publishers[typ]
	if !ok {
		return Result{Type: typ, OK: false, Err: fmt.Errorf("no publisher for %s", typ)}
	}
	return p.Publish(ctx, dest, output, tctx, secrets)
}

func destType(d config.Destination) (string, error) {
	switch {
	case d.GitHubIssue != nil:
		return "github-issue", nil
	case d.Slack != nil:
		return "slack", nil
	case d.Discord != nil:
		return "discord", nil
	case d.Teams != nil:
		return "teams", nil
	}
	return "", fmt.Errorf("destination entry has no recognised type")
}
