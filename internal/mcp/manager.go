package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	FatalKindServerCrashed  = "mcp_server_crashed"
	FatalKindToolTimeout    = "mcp_tool_timeout"
	FatalKindInvalidName    = "mcp_tool_invalid_name"
	FatalKindCallCancelled  = "mcp_call_cancelled"
)

// ToolUse is the subset of an LLM-emitted tool call that Manager needs to
// dispatch. Mirrors internal/llm.ToolUse, kept local to avoid a dependency
// cycle. The runner converts between the two.
type ToolUse struct {
	ID    string
	Name  string          // namespaced "<server>__<tool>"
	Input json.RawMessage
}

// CallResult is the outcome of a single tool dispatch.
type CallResult struct {
	ID         string
	ResultJSON json.RawMessage
	IsError    bool
	DurationMS int64
}

// FatalError is a failure that the run cannot recover from (server crash,
// per-call timeout, etc.). The runner treats this as a fatal run failure
// and fails with the given Kind (one of the FatalKind* constants).
type FatalError struct {
	Kind string
	Err  error
}

func (e *FatalError) Error() string {
	if e.Err == nil {
		return e.Kind
	}
	return e.Kind + ": " + e.Err.Error()
}

// Manager owns one-or-more MCP servers for the lifetime of a single run.
type Manager struct {
	mu      sync.Mutex
	servers map[string]*serverEntry
}

type serverEntry struct {
	proc       *serverProcess
	client     *client
	tools      []Tool
	stderrTail *ringBuf
}

func NewManager(ctx context.Context) *Manager {
	return &Manager{
		servers: map[string]*serverEntry{},
	}
}

// Start launches one server. Blocks until initialize + tools/list succeed,
// the process exits, or ctx deadline hits.
func (m *Manager) Start(name, command string, args, env []string) error {
	m.mu.Lock()
	if _, exists := m.servers[name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("mcp: server %q already started", name)
	}
	m.mu.Unlock()

	proc, err := startServerProcess(command, args, env)
	if err != nil {
		return fmt.Errorf("mcp: start %q: %w", name, err)
	}

	// Drain stderr into a ring buffer so the subprocess doesn't block on
	// a full pipe. The last ~1KB is available via StderrTail for diagnostics.
	tail := &ringBuf{buf: make([]byte, 1024)}
	go func() {
		tmp := make([]byte, 256)
		for {
			n, err := proc.stderr.Read(tmp)
			if n > 0 {
				tail.write(tmp[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	c := newClient(proc.stdout, proc.stdin)
	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.initialize(initCtx); err != nil {
		proc.shutdown()
		return fmt.Errorf("mcp: initialize %q: %w", name, err)
	}
	tools, err := c.listTools(initCtx)
	if err != nil {
		proc.shutdown()
		return fmt.Errorf("mcp: tools/list %q: %w", name, err)
	}

	m.mu.Lock()
	m.servers[name] = &serverEntry{proc: proc, client: c, tools: tools, stderrTail: tail}
	m.mu.Unlock()
	return nil
}

// Tools returns the read-only tool list for a named server.
func (m *Manager) Tools(name string) []Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.servers[name]
	if s == nil {
		return nil
	}
	out := make([]Tool, len(s.tools))
	copy(out, s.tools)
	return out
}

// DispatchAll runs all calls in parallel, each bounded by perToolTimeout.
// Returns per-call results and an optional FatalError describing the first
// fatal condition observed.
func (m *Manager) DispatchAll(ctx context.Context, calls []ToolUse, perToolTimeout time.Duration) ([]CallResult, *FatalError) {
	results := make([]CallResult, len(calls))
	dispatchCtx, dispatchCancel := context.WithCancel(ctx)
	defer dispatchCancel()
	var (
		fatalMu sync.Mutex
		fatal   *FatalError
	)

	setFatal := func(kind string, err error) {
		fatalMu.Lock()
		if fatal == nil {
			fatal = &FatalError{Kind: kind, Err: err}
			dispatchCancel()
		}
		fatalMu.Unlock()
	}

	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call ToolUse) {
			defer wg.Done()
			server, tool, ok := splitToolName(call.Name)
			if !ok {
				results[i] = CallResult{ID: call.ID, IsError: true}
				setFatal(FatalKindInvalidName, fmt.Errorf("tool name %q missing __ namespace", call.Name))
				return
			}
			m.mu.Lock()
			entry := m.servers[server]
			m.mu.Unlock()
			if entry == nil {
				results[i] = CallResult{ID: call.ID, IsError: true}
				setFatal(FatalKindInvalidName, fmt.Errorf("no such server %q", server))
				return
			}

			start := time.Now()
			callCtx, cancel := context.WithTimeout(dispatchCtx, perToolTimeout)
			defer cancel()
			raw, isErr, err := entry.client.callTool(callCtx, tool, call.Input)
			dur := time.Since(start).Milliseconds()
			if err != nil {
				results[i] = CallResult{ID: call.ID, IsError: true}
				kind := FatalKindServerCrashed
				if errors.Is(err, context.DeadlineExceeded) {
					kind = FatalKindToolTimeout
				} else if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					kind = FatalKindCallCancelled
				}
				setFatal(kind, err)
				return
			}
			results[i] = CallResult{ID: call.ID, ResultJSON: raw, IsError: isErr, DurationMS: dur}
		}(i, call)
	}
	wg.Wait()

	return results, fatal
}

// Shutdown terminates every server. Idempotent.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	servers := m.servers
	m.servers = map[string]*serverEntry{}
	m.mu.Unlock()
	for _, s := range servers {
		s.proc.shutdown()
	}
}

// splitToolName splits "<server>__<tool>" into (server, tool, true) or
// returns (_, _, false) on malformed input.
func splitToolName(name string) (string, string, bool) {
	i := strings.Index(name, "__")
	if i <= 0 || i == len(name)-2 {
		return "", "", false
	}
	return name[:i], name[i+2:], true
}

// StderrTail returns the last ~1KB of a server's stderr output.
func (m *Manager) StderrTail(name string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.servers[name]
	if s == nil || s.stderrTail == nil {
		return nil
	}
	return s.stderrTail.snapshot()
}

type ringBuf struct {
	mu  sync.Mutex
	buf []byte
	pos int
	len int
}

func (r *ringBuf) write(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range data {
		r.buf[r.pos] = b
		r.pos = (r.pos + 1) % len(r.buf)
		if r.len < len(r.buf) {
			r.len++
		}
	}
}

func (r *ringBuf) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.len == 0 {
		return nil
	}
	out := make([]byte, r.len)
	start := (r.pos - r.len + len(r.buf)) % len(r.buf)
	if start+r.len <= len(r.buf) {
		copy(out, r.buf[start:start+r.len])
	} else {
		first := len(r.buf) - start
		copy(out, r.buf[start:])
		copy(out[first:], r.buf[:r.len-first])
	}
	return out
}
