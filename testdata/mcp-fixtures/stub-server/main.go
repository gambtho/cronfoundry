// Stub MCP server for tests.
//
// Behavior is controlled by env vars:
//
//   MCP_STUB_EXIT_ON_INIT=1      exit 1 during initialize
//   MCP_STUB_CRASH_ON_CALL=1     exit 1 mid-tools/call
//   MCP_STUB_SLEEP_MS=5000       sleep N ms inside tools/call before replying
//   MCP_STUB_RETURN_ERROR=1      respond to tools/call with a JSON-RPC error result
//   MCP_STUB_CALL_COUNT_FILE=path  write call count to file after each tools/call
//   MCP_STUB_TOOL_NAME=echo      tool name in tools/list (default "echo")
//
// The stub speaks MCP 2024-11-05 over stdio. Each incoming JSON-RPC
// request produces exactly one response (no notifications).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

type req struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type resp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	if os.Getenv("MCP_STUB_EXIT_ON_INIT") == "1" {
		// Exit without sending anything, once the first line arrives.
		sc := bufio.NewScanner(os.Stdin)
		sc.Scan()
		os.Exit(1)
	}

	toolName := os.Getenv("MCP_STUB_TOOL_NAME")
	if toolName == "" {
		toolName = "echo"
	}
	callCountFile := os.Getenv("MCP_STUB_CALL_COUNT_FILE")
	var callCount int

	enc := json.NewEncoder(os.Stdout)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<16), 1<<20)

	for sc.Scan() {
		var r req
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		switch r.Method {
		case "initialize":
			raw, _ := json.Marshal(map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]string{"name": "stub", "version": "0.1"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			})
			writeResp(enc, r.ID, raw, nil)
		case "tools/list":
			raw, _ := json.Marshal(map[string]any{
				"tools": []map[string]any{{
					"name":        toolName,
					"description": "stub tool",
					"inputSchema": map[string]any{"type": "object"},
				}},
			})
			writeResp(enc, r.ID, raw, nil)
		case "tools/call":
			callCount++
			if callCountFile != "" {
				_ = os.WriteFile(callCountFile, []byte(strconv.Itoa(callCount)), 0644)
			}
			if os.Getenv("MCP_STUB_CRASH_ON_CALL") == "1" {
				os.Exit(1)
			}
			if ms, err := strconv.Atoi(os.Getenv("MCP_STUB_SLEEP_MS")); err == nil && ms > 0 {
				time.Sleep(time.Duration(ms) * time.Millisecond)
			}
			if os.Getenv("MCP_STUB_RETURN_ERROR") == "1" {
				writeResp(enc, r.ID, nil, &rpcErr{Code: -32000, Message: "stub error"})
				continue
			}
			raw, _ := json.Marshal(map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "ok"},
				},
			})
			writeResp(enc, r.ID, raw, nil)
		default:
			writeResp(enc, r.ID, nil, &rpcErr{Code: -32601, Message: fmt.Sprintf("method not found: %s", r.Method)})
		}
	}
}

func writeResp(enc *json.Encoder, id json.RawMessage, result json.RawMessage, e *rpcErr) {
	_ = enc.Encode(resp{JSONRPC: "2.0", ID: id, Result: result, Error: e})
}
