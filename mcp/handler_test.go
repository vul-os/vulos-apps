package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vul-os/vulos-apps/appsplatform"
)

// newTestHandler mounts an MCP handler over an in-memory registry + described
// adapter and returns it plus a freshly minted token with the given scopes.
func newTestHandler(t *testing.T, scopes []string, products []string) (*Handler, string) {
	t.Helper()
	reg := appsplatform.NewMemoryRegistry()
	c, err := reg.Create(appsplatform.CreateParams{Name: "agent", OwnerID: "o", Products: products, Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(MCPConfig{Adapter: &describedAdapter{}, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	return h, c.Token
}

// rpc POSTs a JSON-RPC call and returns the decoded response.
func rpc(t *testing.T, h http.Handler, token, method string, params any, accept string) (*httptest.ResponseRecorder, Response) {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(string(b)))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var resp Response
	if w.Code == http.StatusOK && strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
	}
	return w, resp
}

func TestHTTPUnauthenticated(t *testing.T) {
	h, _ := newTestHandler(t, []string{appsplatform.ScopeAppsRead}, []string{appsplatform.ProductTalk})
	w, _ := rpc(t, h, "", "tools/list", nil, "application/json")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing token should 401, got %d: %s", w.Code, w.Body)
	}
	if w, _ := rpc(t, h, "vat_bogus", "tools/list", nil, "application/json"); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token should 401, got %d", w.Code)
	}
}

func TestHTTPCrossProductRejected(t *testing.T) {
	// Token targets Mail only; mount is Talk → 403.
	h, tok := newTestHandler(t, []string{appsplatform.ScopeAppsRead}, []string{appsplatform.ProductMail})
	w, _ := rpc(t, h, tok, "tools/list", nil, "application/json")
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-product token should 403, got %d: %s", w.Code, w.Body)
	}
}

// TestRoundTrip exercises initialize → tools/list → tools/call over HTTP.
func TestRoundTrip(t *testing.T) {
	h, tok := newTestHandler(t,
		[]string{appsplatform.ScopeAppsRead, appsplatform.ScopeAppsWrite, appsplatform.ScopeChatWrite},
		[]string{appsplatform.ProductTalk})

	// initialize
	w, resp := rpc(t, h, tok, "initialize", initializeParams{ProtocolVersion: ProtocolVersion}, "application/json")
	if w.Code != http.StatusOK || resp.Error != nil {
		t.Fatalf("initialize failed: %d %s", w.Code, w.Body)
	}

	// tools/list
	_, resp = rpc(t, h, tok, "tools/list", nil, "application/json")
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	var list toolsListResult
	remarshal(t, resp.Result, &list)
	if len(list.Tools) != 1 || list.Tools[0].Name != "message.post" {
		t.Fatalf("tools/list returned %+v", list.Tools)
	}

	// tools/call
	_, resp = rpc(t, h, tok, "tools/call",
		toolsCallParams{Name: "message.post", Arguments: json.RawMessage(`{"text":"hello","target":"general"}`)},
		"application/json")
	if resp.Error != nil {
		t.Fatalf("tools/call error: %+v", resp.Error)
	}
	var call callToolResult
	remarshal(t, resp.Result, &call)
	if call.IsError || len(call.Content) != 1 || !strings.Contains(call.Content[0].Text, "\"posted\":true") {
		t.Fatalf("unexpected tool result: %+v", call)
	}
}

func TestHTTPNotificationAccepted(t *testing.T) {
	h, tok := newTestHandler(t, []string{appsplatform.ScopeAppsRead}, []string{appsplatform.ProductTalk})
	// A message with no id is a notification → 202, no body.
	b := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("notification should 202, got %d: %s", w.Code, w.Body)
	}
}

func TestHTTPSSEResponse(t *testing.T) {
	h, tok := newTestHandler(t, []string{appsplatform.ScopeAppsRead}, []string{appsplatform.ProductTalk})
	// When the client accepts text/event-stream, the response is an SSE event.
	w, _ := rpc(t, h, tok, "tools/list", nil, "application/json, text/event-stream")
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected SSE content-type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "event: message") || !strings.Contains(w.Body.String(), "data: ") {
		t.Fatalf("expected SSE framing, got %q", w.Body.String())
	}
}

func TestGatewaySeam(t *testing.T) {
	reg := appsplatform.NewMemoryRegistry()
	g := &fakeGateway{}
	_, err := NewHandler(MCPConfig{Adapter: &describedAdapter{}, Registry: reg, Gateway: g})
	if err != nil {
		t.Fatal(err)
	}
	if g.product != appsplatform.ProductTalk || g.srv == nil {
		t.Fatalf("gateway seam not invoked: %+v", g)
	}
}

func TestNewHandlerValidation(t *testing.T) {
	if _, err := NewHandler(MCPConfig{Registry: appsplatform.NewMemoryRegistry()}); err == nil {
		t.Fatal("expected error without adapter")
	}
	if _, err := NewHandler(MCPConfig{Adapter: &describedAdapter{}}); err == nil {
		t.Fatal("expected error without registry")
	}
}

type fakeGateway struct {
	product string
	srv     *Server
}

func (f *fakeGateway) RegisterServer(product, _ string, srv *Server) error {
	f.product, f.srv = product, srv
	return nil
}

func remarshal(t *testing.T, in any, out any) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}
