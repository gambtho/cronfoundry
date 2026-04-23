package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// client is a single-connection MCP JSON-RPC client. It reads one line
// per response from stdout, writes one line per request to stdin. Requests
// are correlated by ID.
type client struct {
	w       io.Writer
	writeMu sync.Mutex // serializes writes to w
	r       *bufio.Reader
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan jsonrpcResponse
	closed  bool

	done chan struct{}
}

func newClient(stdout io.Reader, stdin io.Writer) *client {
	c := &client{
		w:       stdin,
		r:       bufio.NewReader(stdout),
		pending: map[int64]chan jsonrpcResponse{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *client) readLoop() {
	defer close(c.done)
	for {
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			c.mu.Lock()
			c.closed = true
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		var idInt int64
		if err := json.Unmarshal(resp.ID, &idInt); err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[idInt]
		delete(c.pending, idInt)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *client) send(ctx context.Context, method string, params any) (jsonrpcResponse, error) {
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return jsonrpcResponse{}, fmt.Errorf("marshal params: %w", err)
		}
		paramsRaw = b
	}
	reqBytes, _ := json.Marshal(jsonrpcRequest{
		JSONRPC: "2.0", ID: idRaw, Method: method, Params: paramsRaw,
	})
	reqBytes = append(reqBytes, '\n')

	ch := make(chan jsonrpcResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return jsonrpcResponse{}, fmt.Errorf("mcp client closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	c.writeMu.Lock()
	_, err := c.w.Write(reqBytes)
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return jsonrpcResponse{}, fmt.Errorf("write: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return jsonrpcResponse{}, fmt.Errorf("mcp client closed while awaiting response")
		}
		return resp, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return jsonrpcResponse{}, ctx.Err()
	case <-c.done:
		return jsonrpcResponse{}, fmt.Errorf("mcp client stream ended")
	}
}

func (c *client) initialize(ctx context.Context) error {
	resp, err := c.send(ctx, "initialize", initializeParams{
		ProtocolVersion: protocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      map[string]string{"name": "cronfoundry", "version": "0.1"},
	})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize: %s (%d)", resp.Error.Message, resp.Error.Code)
	}
	return nil
}

func (c *client) listTools(ctx context.Context) ([]Tool, error) {
	resp, err := c.send(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list: %s", resp.Error.Message)
	}
	var out listToolsResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, fmt.Errorf("tools/list result: %w", err)
	}
	tools := make([]Tool, 0, len(out.Tools))
	for _, t := range out.Tools {
		tools = append(tools, Tool(t))
	}
	return tools, nil
}

// callTool returns (rawResult, isError, err). isError is true when the MCP
// server returned an error at the tool level (the LLM should see it); err
// is set only for transport / protocol failures.
func (c *client) callTool(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, bool, error) {
	resp, err := c.send(ctx, "tools/call", callToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, false, err
	}
	if resp.Error != nil {
		// JSON-RPC-level error → tool-level error (spec semantics).
		raw, _ := json.Marshal(map[string]any{
			"error": resp.Error.Message,
			"code":  resp.Error.Code,
		})
		return raw, true, nil
	}
	return resp.Result, false, nil
}
