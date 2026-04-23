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
// and fails with the given Kind.
type FatalError struct {
	Kind string // "mcp_server_crashed" | "mcp_tool_timeout"
	Err  error
}

func (e *FatalError) Error() string { return e.Kind + ": " + e.Err.Error() }

// Manager owns one-or-more MCP servers for the lifetime of a single run.
type Manager struct {
	ctx context.Context

	mu      sync.Mutex
	servers map[string]*serverEntry
}

type serverEntry struct {
	name   string
	proc   *serverProcess
	client *client
	tools  []Tool
}

func NewManager(ctx context.Context) *Manager {
	return &Manager{
		ctx:     ctx,
		servers: map[string]*serverEntry{},
	}
}

// Start launches one server. Blocks until initialize + tools/list succeed,
// the process exits, or ctx deadline hits.
func (m *Manager) Start(name, command string, args, env []string) error {
	proc, err := startServerProcess(command, args, env)
	if err != nil {
		return fmt.Errorf("mcp: start %q: %w", name, err)
	}

	// Drain stderr into memory (last ~1KB) for crash diagnostics; keep the
	// goroutine running for the server's lifetime.
	// (Implementer: simple "read-and-discard-but-keep-last-N-bytes" ring buffer.
	// Small enough that we keep it inline here.)
	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := proc.stderr.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	c := newClient(proc.stdout, proc.stdin)
	initCtx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
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
	m.servers[name] = &serverEntry{name: name, proc: proc, client: c, tools: tools}
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
	var (
		fatalMu sync.Mutex
		fatal   *FatalError
	)

	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call ToolUse) {
			defer wg.Done()
			server, tool, ok := splitToolName(call.Name)
			if !ok {
				fatalMu.Lock()
				if fatal == nil {
					fatal = &FatalError{Kind: "mcp_tool_invalid_name", Err: fmt.Errorf("tool name %q missing __ namespace", call.Name)}
				}
				fatalMu.Unlock()
				return
			}
			m.mu.Lock()
			entry := m.servers[server]
			m.mu.Unlock()
			if entry == nil {
				fatalMu.Lock()
				if fatal == nil {
					fatal = &FatalError{Kind: "mcp_tool_invalid_name", Err: fmt.Errorf("no such server %q", server)}
				}
				fatalMu.Unlock()
				return
			}

			start := time.Now()
			callCtx, cancel := context.WithTimeout(ctx, perToolTimeout)
			defer cancel()
			raw, isErr, err := entry.client.callTool(callCtx, tool, call.Input)
			dur := time.Since(start).Milliseconds()
			if err != nil {
				kind := "mcp_server_crashed"
				if errors.Is(err, context.DeadlineExceeded) {
					kind = "mcp_tool_timeout"
				}
				fatalMu.Lock()
				if fatal == nil {
					fatal = &FatalError{Kind: kind, Err: err}
				}
				fatalMu.Unlock()
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
		_ = s.proc.shutdown()
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
