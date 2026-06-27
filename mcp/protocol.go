package mcp

import "encoding/json"

// MCP (Model Context Protocol) wire types for the method subset this foundation
// implements: initialize, ping, tools/list, tools/call, resources/list,
// resources/read. See https://modelcontextprotocol.io for the full spec.

// ProtocolVersion is the MCP protocol revision this server speaks. The Streamable
// HTTP transport was introduced in 2025-03-26; this is the current stable
// revision. On initialize the server echoes a version it supports.
const ProtocolVersion = "2025-06-18"

// ServerName / ServerVersion identify this implementation in the initialize
// handshake.
const (
	ServerName    = "vulos-mcp"
	ServerVersion = "0.1.0"
)

// initializeResult is returned from the initialize handshake.
type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
	Instructions    string       `json:"instructions,omitempty"`
}

type capabilities struct {
	Tools     *listChangedCap `json:"tools,omitempty"`
	Resources *listChangedCap `json:"resources,omitempty"`
}

type listChangedCap struct {
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// initializeParams is the client's half of the handshake. We read the client's
// requested protocol version for negotiation.
type initializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ClientInfo      json.RawMessage `json:"clientInfo"`
}

// Tool is an MCP tool descriptor (derived from a ProductAdapter Act action).
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// toolsListResult is the tools/list response.
type toolsListResult struct {
	Tools []Tool `json:"tools"`
}

// toolsCallParams is the tools/call request params.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Content is a single content block in a tool-call result. Only text content is
// produced by this foundation.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// callToolResult is the tools/call response.
type callToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Resource is an MCP resource descriptor (derived from a ProductAdapter Read
// kind).
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// resourcesListResult is the resources/list response.
type resourcesListResult struct {
	Resources []Resource `json:"resources"`
}

// resourcesReadParams is the resources/read request params.
type resourcesReadParams struct {
	URI string `json:"uri"`
}

// resourceContents is one content entry returned by resources/read.
type resourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
}

// resourcesReadResult is the resources/read response.
type resourcesReadResult struct {
	Contents []resourceContents `json:"contents"`
}
