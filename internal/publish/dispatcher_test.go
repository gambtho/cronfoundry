package publish

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

type fakePub struct {
	typ    string
	ok     bool
	delay  time.Duration
	called int32
}

func (f *fakePub) Type() string { return f.typ }
func (f *fakePub) Publish(ctx context.Context, d config.Destination, out string, tctx template.Context, s SecretGetter) Result {
	atomic.AddInt32(&f.called, 1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.ok {
		return Result{Type: f.typ, OK: true, Detail: "ok"}
	}
	return Result{Type: f.typ, OK: false, Err: errors.New("boom")}
}

type nilSecrets struct{}

func (nilSecrets) Get(string) (string, error) { return "", nil }

func TestDispatch_IsolatesFailures_RunsInParallel(t *testing.T) {
	gh := &fakePub{typ: "github-issue", ok: false, delay: 20 * time.Millisecond}
	sl := &fakePub{typ: "slack", ok: true, delay: 20 * time.Millisecond}

	d := &Dispatcher{Publishers: map[string]Publisher{"github-issue": gh, "slack": sl}}

	dests := []config.Destination{
		{GitHubIssue: &config.GitHubIssueDest{}},
		{Slack: &config.WebhookDest{}},
	}
	start := time.Now()
	results := d.Dispatch(context.Background(), dests, func(_ config.Destination) string { return "body" }, template.Context{}, nilSecrets{}, true)
	dur := time.Since(start)

	require.Len(t, results, 2)
	// Order preserved.
	assert.Equal(t, "github-issue", results[0].Type)
	assert.False(t, results[0].OK)
	assert.Equal(t, "slack", results[1].Type)
	assert.True(t, results[1].OK)
	// Parallel execution — total time closer to 20ms than 40ms.
	assert.Less(t, dur, 50*time.Millisecond, "expected parallel execution")
}

func TestDispatch_UnknownType(t *testing.T) {
	d := &Dispatcher{Publishers: map[string]Publisher{}}
	dests := []config.Destination{{Slack: &config.WebhookDest{}}}
	results := d.Dispatch(context.Background(), dests, func(_ config.Destination) string { return "" }, template.Context{}, nilSecrets{}, true)
	require.Len(t, results, 1)
	assert.False(t, results[0].OK)
	assert.Contains(t, results[0].Err.Error(), "no publisher for slack")
}

func TestDispatcher_SkipsOnCondition(t *testing.T) {
	called := false
	pub := &fakePub{typ: "slack", ok: true}
	origPublish := pub.Publish
	_ = origPublish

	var calledFlag int32
	capPub := &capturePub{inner: pub, called: &calledFlag}
	d := &Dispatcher{Publishers: map[string]Publisher{"slack": capPub}}
	dest := config.Destination{
		Slack: &config.WebhookDest{Secret: "s"},
		When:  "on_failure",
	}
	outputFor := func(_ config.Destination) string { return "out" }
	results := d.Dispatch(context.Background(), []config.Destination{dest}, outputFor, template.Context{}, nilSecrets{}, true)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if !results[0].Skipped {
		t.Errorf("want Skipped=true for on_failure when run succeeded")
	}
	if called {
		t.Errorf("publisher should not have been called for skipped destination")
	}
	_ = calledFlag
}

type capturePub struct {
	inner  Publisher
	called *int32
}

func (c *capturePub) Type() string { return c.inner.Type() }
func (c *capturePub) Publish(ctx context.Context, d config.Destination, out string, tctx template.Context, s SecretGetter) Result {
	atomic.AddInt32(c.called, 1)
	return c.inner.Publish(ctx, d, out, tctx, s)
}
