package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"

	"github.com/vul-os/vulos-apps/appsplatform"
	"github.com/vul-os/vulos-apps/mcp"
)

// noteAdapter is a tiny example product: a notes app exposing one action
// ("note.add") as an MCP tool and one read kind ("notes") as an MCP resource. A
// real product reuses its EXISTING appsplatform.ProductAdapter unchanged and just
// adds the MCPTools/MCPResources methods (the optional Descriptor).
type noteAdapter struct{}

func (noteAdapter) Product() string { return appsplatform.ProductOffice }
func (noteAdapter) RequiredScope(string) string {
	return ""
}
func (noteAdapter) CanAccessTarget(*appsplatform.App, string) (bool, bool) { return true, true }
func (noteAdapter) Act(_ context.Context, _ *appsplatform.App, r appsplatform.ActionRequest, _ appsplatform.EmitFunc) (any, error) {
	var p struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(r.Payload, &p)
	return map[string]any{"added": p.Text}, nil
}
func (noteAdapter) Read(context.Context, *appsplatform.App, appsplatform.ReadRequest) (any, error) {
	return map[string]any{"notes": []string{"buy milk"}}, nil
}

// Descriptor methods publish the adapter's surface to MCP.
func (noteAdapter) MCPTools() []mcp.ToolSpec {
	return []mcp.ToolSpec{{
		Action:      "note.add",
		Description: "Add a note.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}}
}
func (noteAdapter) MCPResources() []mcp.ResourceSpec {
	return []mcp.ResourceSpec{{Kind: "notes", Description: "All notes."}}
}

// Example mounts an MCP server for a fake "notes" product and drives the
// initialize → tools/list → tools/call handshake over HTTP with an app token.
func Example() {
	reg := appsplatform.NewMemoryRegistry()
	created, _ := reg.Create(appsplatform.CreateParams{
		Name:     "agent",
		OwnerID:  "owner",
		Products: []string{appsplatform.ProductOffice},
		Scopes:   []string{appsplatform.ScopeAppsRead, appsplatform.ScopeAppsWrite},
	})

	h, _ := mcp.NewHandler(mcp.MCPConfig{Adapter: noteAdapter{}, Registry: reg})
	srv := httptest.NewServer(h) // mounted at "/mcp"
	defer srv.Close()

	call := func(method string, params any) mcp.Response {
		body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+created.Token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var resp mcp.Response
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return resp
	}

	init := call("initialize", map[string]any{"protocolVersion": mcp.ProtocolVersion})
	fmt.Println("initialize error:", init.Error == nil)

	tools := call("tools/list", nil)
	b, _ := json.Marshal(tools.Result)
	fmt.Println("tools/list contains note.add:", strings.Contains(string(b), "note.add"))

	res := call("tools/call", map[string]any{"name": "note.add", "arguments": map[string]any{"text": "hello"}})
	// The tool result wraps the adapter's return value as a JSON text content block.
	var call0 struct {
		Content []struct{ Text string } `json:"content"`
	}
	rb, _ := json.Marshal(res.Result)
	_ = json.Unmarshal(rb, &call0)
	fmt.Println("tool result has added:", len(call0.Content) == 1 && strings.Contains(call0.Content[0].Text, `"added":"hello"`))

	// Output:
	// initialize error: true
	// tools/list contains note.add: true
	// tool result has added: true
}
