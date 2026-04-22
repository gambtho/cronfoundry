// Package mcp implements a minimal MCP 2024-11-05 stdio client: just
// enough of the protocol to initialize a server, enumerate its tools,
// call tools, and cancel in-flight calls. Resources, prompts, sampling,
// and roots are intentionally out of scope (see deferred in the spec).
package mcp

import "encoding/json"

// jsonrpcRequest is a single JSON-RPC 2.0 request over stdio.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is a single JSON-RPC 2.0 response. Exactly one of
// Result or Error is set.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// initializeParams is MCP's initialize request payload. We announce minimal
// capabilities — we don't serve roots, sampling, resources, or prompts.
type initializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]any         `json:"capabilities"`
	ClientInfo      map[string]string      `json:"clientInfo"`
}

// listToolsResult is the 'result' payload of tools/list.
type listToolsResult struct {
	Tools []toolWire `json:"tools"`
}

type toolWire struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// callToolParams is the 'params' payload of tools/call.
type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Tool is the public representation of an MCP-advertised tool, exposed
// to Manager consumers.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// protocolVersion is the MCP spec version we speak.
const protocolVersion = "2024-11-05"
