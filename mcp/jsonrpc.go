package mcp

import "encoding/json"

// JSON-RPC 2.0 envelope types and error codes used by the MCP transport.
//
// MCP speaks JSON-RPC 2.0 over the Streamable HTTP transport. We implement the
// small subset the foundation needs directly (no external dependency, matching
// the rest of this module's stdlib-only posture) rather than pulling in a full
// MCP SDK: the protocol surface here is intentionally tiny and generated from the
// product's existing appsplatform.ProductAdapter.

const jsonRPCVersion = "2.0"

// Standard JSON-RPC 2.0 error codes (plus MCP's use of them).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Request is an incoming JSON-RPC 2.0 message. A message with no ID is a
// notification (no response is sent).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message is a notification (no id ⇒ the
// server must not reply).
func (r *Request) isNotification() bool { return len(r.ID) == 0 }

// Response is an outgoing JSON-RPC 2.0 message. Exactly one of Result / Error is
// set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

// newError builds an error response for a request id.
func newError(id json.RawMessage, code int, msg string) *Response {
	return &Response{JSONRPC: jsonRPCVersion, ID: id, Error: &RPCError{Code: code, Message: msg}}
}

// newResult builds a success response for a request id.
func newResult(id json.RawMessage, result any) *Response {
	return &Response{JSONRPC: jsonRPCVersion, ID: id, Result: result}
}
