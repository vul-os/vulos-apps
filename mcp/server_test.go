package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/vul-os/vulos-apps/appsplatform"
)

// describedAdapter is a Talk-like adapter that implements Descriptor, so the MCP
// server derives precise tools/resources from it.
type describedAdapter struct{ acted int }

func (a *describedAdapter) Product() string { return appsplatform.ProductTalk }

func (a *describedAdapter) RequiredScope(actionOrKind string) string {
	switch actionOrKind {
	case "message.post":
		return appsplatform.ScopeChatWrite
	case "history":
		return appsplatform.ScopeHistoryRead
	default:
		return ""
	}
}

func (a *describedAdapter) CanAccessTarget(_ *appsplatform.App, target string) (bool, bool) {
	switch target {
	case "missing":
		return false, false
	case "secret":
		return false, true
	default:
		return true, true
	}
}

func (a *describedAdapter) Act(_ context.Context, _ *appsplatform.App, req appsplatform.ActionRequest, emit appsplatform.EmitFunc) (any, error) {
	a.acted++
	if emit != nil {
		emit(appsplatform.EventMessageCreated, map[string]any{"target": req.Target}, nil)
	}
	return map[string]any{"posted": true, "target": req.Target, "payload": json.RawMessage(req.Payload)}, nil
}

func (a *describedAdapter) Read(_ context.Context, _ *appsplatform.App, req appsplatform.ReadRequest) (any, error) {
	return map[string]any{"kind": req.Kind, "target": req.Target, "params": req.Params}, nil
}

func (a *describedAdapter) MCPTools() []ToolSpec {
	return []ToolSpec{
		{
			Action:        "message.post",
			Description:   "Post a message to a channel.",
			AcceptsTarget: true,
			InputSchema:   json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		},
	}
}

func (a *describedAdapter) MCPResources() []ResourceSpec {
	return []ResourceSpec{
		{Kind: "history", Description: "Channel message history.", AcceptsTarget: true},
	}
}

// app builds an in-context app with the given scopes targeting Talk.
func testApp(scopes ...string) *appsplatform.App {
	return &appsplatform.App{ID: "a1", Name: "agent", Products: []string{appsplatform.ProductTalk}, Scopes: scopes}
}

func req(t *testing.T, method string, params any) *Request {
	t.Helper()
	r := &Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		r.Params = b
	}
	return r
}

func TestDeriveToolsFromDescriptor(t *testing.T) {
	s := NewServer(&describedAdapter{}, nil)
	tools := s.Tools()
	if len(tools) != 1 || tools[0].Name != "message.post" {
		t.Fatalf("expected one message.post tool, got %+v", tools)
	}
	// The input schema must have had the target property injected.
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["target"]; !ok {
		t.Fatalf("AcceptsTarget tool should inject a target property: %v", props)
	}
	if _, ok := props["text"]; !ok {
		t.Fatalf("supplied schema property lost: %v", props)
	}
	res := s.Resources()
	if len(res) != 1 || res[0].URI != "vulos://talk/history/{target}" {
		t.Fatalf("expected one history resource template, got %+v", res)
	}
}

func TestDeriveGenericFallback(t *testing.T) {
	// An adapter WITHOUT Descriptor gets the generic passthrough tool + no
	// resources.
	s := NewServer(&bareAdapter{}, nil)
	tools := s.Tools()
	if len(tools) != 1 || tools[0].Name != genericActTool {
		t.Fatalf("expected generic act tool, got %+v", tools)
	}
	if len(s.Resources()) != 0 {
		t.Fatalf("expected no resources for bare adapter, got %+v", s.Resources())
	}
}

func TestToolsCallSucceedsAndFansOut(t *testing.T) {
	ad := &describedAdapter{}
	var emitted string
	emit := func(eventType string, _ map[string]any, _ func(*appsplatform.App) bool) { emitted = eventType }
	s := NewServer(ad, emit)

	r := req(t, "tools/call", toolsCallParams{Name: "message.post", Arguments: json.RawMessage(`{"text":"hi","target":"general"}`)})
	resp := s.handle(context.Background(), testApp(appsplatform.ScopeAppsWrite, appsplatform.ScopeChatWrite), r)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result := resp.Result.(callToolResult)
	if result.IsError {
		t.Fatalf("tool reported error: %+v", result)
	}
	if ad.acted != 1 {
		t.Fatalf("adapter.Act not invoked once: %d", ad.acted)
	}
	if emitted != appsplatform.EventMessageCreated {
		t.Fatalf("expected event fan-out, got %q", emitted)
	}
}

func TestToolsCallScopeGating(t *testing.T) {
	s := NewServer(&describedAdapter{}, nil)
	r := req(t, "tools/call", toolsCallParams{Name: "message.post", Arguments: json.RawMessage(`{"text":"hi","target":"general"}`)})

	// No apps:write at all → rejected at the method gate.
	resp := s.handle(context.Background(), testApp(appsplatform.ScopeAppsRead), r)
	if resp.Error == nil {
		t.Fatal("expected scope error without apps:write")
	}
	// apps:write but missing the action-specific chat:write → rejected.
	resp = s.handle(context.Background(), testApp(appsplatform.ScopeAppsWrite), r)
	if resp.Error == nil {
		t.Fatal("expected scope error without chat:write")
	}
}

func TestToolsCallTargetAccess(t *testing.T) {
	s := NewServer(&describedAdapter{}, nil)
	app := testApp(appsplatform.ScopeAppsWrite, appsplatform.ScopeChatWrite)
	mk := func(target string) *Request {
		return req(t, "tools/call", toolsCallParams{Name: "message.post", Arguments: json.RawMessage(`{"text":"x","target":"` + target + `"}`)})
	}
	if resp := s.handle(context.Background(), app, mk("secret")); resp.Error == nil {
		t.Fatal("inaccessible target should error")
	}
	if resp := s.handle(context.Background(), app, mk("missing")); resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("unknown target should be invalid-params, got %+v", resp.Error)
	}
}

func TestResourcesReadScopeAndDispatch(t *testing.T) {
	s := NewServer(&describedAdapter{}, nil)
	r := req(t, "resources/read", resourcesReadParams{URI: "vulos://talk/history/general?limit=10"})

	// Without apps:read → method gate rejects.
	if resp := s.handle(context.Background(), testApp(), r); resp.Error == nil {
		t.Fatal("expected scope error without apps:read")
	}
	// With apps:read but missing history:read → action scope rejects.
	if resp := s.handle(context.Background(), testApp(appsplatform.ScopeAppsRead), r); resp.Error == nil {
		t.Fatal("expected scope error without history:read")
	}
	// Fully scoped → reads through to the adapter.
	resp := s.handle(context.Background(), testApp(appsplatform.ScopeAppsRead, appsplatform.ScopeHistoryRead), r)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	out := resp.Result.(resourcesReadResult)
	if len(out.Contents) != 1 {
		t.Fatalf("expected one content entry: %+v", out)
	}
	var read struct {
		Kind   string            `json:"kind"`
		Target string            `json:"target"`
		Params map[string]string `json:"params"`
	}
	if err := json.Unmarshal([]byte(out.Contents[0].Text), &read); err != nil {
		t.Fatal(err)
	}
	if read.Kind != "history" || read.Target != "general" || read.Params["limit"] != "10" {
		t.Fatalf("uri not parsed into read request: %+v", read)
	}
}

func TestGenericActPassthrough(t *testing.T) {
	ad := &bareAdapter{}
	s := NewServer(ad, nil)
	r := req(t, "tools/call", toolsCallParams{Name: genericActTool, Arguments: json.RawMessage(`{"action":"x.do","target":"t","payload":{"k":"v"}}`)})
	resp := s.handle(context.Background(), testApp(appsplatform.ScopeAppsWrite), r)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if ad.lastAction != "x.do" || ad.lastTarget != "t" {
		t.Fatalf("generic passthrough did not forward action/target: %+v", ad)
	}
}

func TestInitializeAndPing(t *testing.T) {
	s := NewServer(&describedAdapter{}, nil)
	resp := s.handle(context.Background(), testApp(), req(t, "initialize", initializeParams{ProtocolVersion: "2025-03-26"}))
	if resp.Error != nil {
		t.Fatalf("initialize errored: %+v", resp.Error)
	}
	init := resp.Result.(initializeResult)
	if init.ProtocolVersion != "2025-03-26" {
		t.Fatalf("should echo client protocol version, got %q", init.ProtocolVersion)
	}
	if init.ServerInfo.Name != ServerName {
		t.Fatalf("server info name %q", init.ServerInfo.Name)
	}
	if resp := s.handle(context.Background(), testApp(), req(t, "ping", nil)); resp.Error != nil {
		t.Fatalf("ping errored: %+v", resp.Error)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := NewServer(&describedAdapter{}, nil)
	resp := s.handle(context.Background(), testApp(), req(t, "tools/frobnicate", nil))
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("expected method-not-found, got %+v", resp.Error)
	}
}

// bareAdapter implements only ProductAdapter (no Descriptor).
type bareAdapter struct {
	lastAction string
	lastTarget string
}

func (b *bareAdapter) Product() string                                        { return appsplatform.ProductTalk }
func (b *bareAdapter) RequiredScope(string) string                            { return "" }
func (b *bareAdapter) CanAccessTarget(*appsplatform.App, string) (bool, bool) { return true, true }
func (b *bareAdapter) Act(_ context.Context, _ *appsplatform.App, r appsplatform.ActionRequest, _ appsplatform.EmitFunc) (any, error) {
	b.lastAction, b.lastTarget = r.Action, r.Target
	return map[string]any{"ok": true}, nil
}
func (b *bareAdapter) Read(_ context.Context, _ *appsplatform.App, _ appsplatform.ReadRequest) (any, error) {
	return map[string]any{}, nil
}
