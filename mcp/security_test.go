package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vul-os/vulos-apps/appsplatform"
)

// rawPost sends an arbitrary (possibly malformed) body to the MCP endpoint with
// the given bearer token and returns the recorder plus the decoded JSON-RPC
// response (when the response is application/json).
func rawPost(t *testing.T, h http.Handler, token, body string) (*httptest.ResponseRecorder, Response) {
	t.Helper()
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var resp Response
	if strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
	}
	return w, resp
}

// TestMalformedJSONRPCNeverPanics feeds the transport a battery of malformed
// envelopes: each must produce a graceful JSON-RPC error (or auth status), never
// a 5xx / panic.
func TestMalformedJSONRPCNeverPanics(t *testing.T) {
	h, tok := newTestHandler(t, []string{appsplatform.ScopeAppsRead}, []string{appsplatform.ProductTalk})

	cases := []struct {
		name, body string
		wantCode   int // JSON-RPC error code expected in the body (0 = don't care)
	}{
		{"empty", ``, codeParseError},
		{"garbage", `not json at all`, codeParseError},
		{"truncated", `{"jsonrpc":"2.0","id":1,`, codeParseError},
		{"batch array", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, codeInvalidRequest},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, codeInvalidRequest},
		{"null", `null`, codeInvalidRequest}, // decodes to a zero Request → bad version
		{"number", `42`, codeParseError},     // cannot decode a number into the envelope
		{"string", `"hello"`, codeParseError},
		{"deep nest params", `{"jsonrpc":"2.0","id":1,"method":"ping","params":` + strings.Repeat("[", 500) + strings.Repeat("]", 500) + `}`, 0},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"do.evil"}`, codeMethodNotFound},
		{"empty method", `{"jsonrpc":"2.0","id":1,"method":""}`, codeMethodNotFound},
	}
	for _, tc := range cases {
		w, resp := rawPost(t, h, tok, tc.body)
		if w.Code >= 500 {
			t.Errorf("%s: produced a 5xx (%d): %s", tc.name, w.Code, w.Body)
			continue
		}
		if tc.wantCode != 0 {
			if resp.Error == nil {
				t.Errorf("%s: expected JSON-RPC error code %d, got result", tc.name, tc.wantCode)
			} else if resp.Error.Code != tc.wantCode {
				t.Errorf("%s: error code = %d, want %d", tc.name, resp.Error.Code, tc.wantCode)
			}
		}
	}
}

// TestBodyCapEnforced sends a body well over the 1 MiB cap; the truncated read
// must surface as a parse error, not an OOM or a 5xx.
func TestBodyCapEnforced(t *testing.T) {
	h, tok := newTestHandler(t, []string{appsplatform.ScopeAppsRead}, []string{appsplatform.ProductTalk})
	huge := `{"jsonrpc":"2.0","id":1,"method":"ping","params":"` + strings.Repeat("A", 2<<20) + `"}`
	w, resp := rawPost(t, h, tok, huge)
	if w.Code >= 500 {
		t.Fatalf("oversized body produced %d", w.Code)
	}
	if resp.Error == nil || resp.Error.Code != codeParseError {
		t.Fatalf("oversized body should be a parse error, got %+v", resp.Error)
	}
}

// TestAuthGatingAllMethods asserts every transport verb requires a valid token
// before doing anything.
func TestAuthGatingAllVerbs(t *testing.T) {
	h, _ := newTestHandler(t, []string{appsplatform.ScopeAppsRead}, []string{appsplatform.ProductTalk})
	for _, verb := range []string{"POST", "GET", "DELETE"} {
		r := httptest.NewRequest(verb, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		if verb == "GET" {
			r.Header.Set("Accept", "text/event-stream")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s without token: got %d, want 401", verb, w.Code)
		}
	}
}

// TestCrossProductGatingAllVerbs confirms a token that does not target the
// mounted product is 403 on every verb (no privilege to even open a stream).
func TestCrossProductGatingAllVerbs(t *testing.T) {
	h, tok := newTestHandler(t, []string{appsplatform.ScopeAppsRead}, []string{appsplatform.ProductMail}) // mail token, talk mount
	for _, verb := range []string{"POST", "GET", "DELETE"} {
		r := httptest.NewRequest(verb, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		r.Header.Set("Authorization", "Bearer "+tok)
		if verb == "GET" {
			r.Header.Set("Accept", "text/event-stream")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s cross-product: got %d, want 403", verb, w.Code)
		}
	}
}

// TestGetRequiresSSEAccept asserts the streaming GET is refused unless the
// client explicitly accepts text/event-stream.
func TestGetRequiresSSEAccept(t *testing.T) {
	h, tok := newTestHandler(t, []string{appsplatform.ScopeAppsRead}, []string{appsplatform.ProductTalk})
	r := httptest.NewRequest("GET", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET without SSE accept: got %d, want 405", w.Code)
	}
}

// TestPrivilegeBoundaryScopeOverHTTP proves the scope boundary is enforced on
// the wire, not just in unit calls: a read-only token cannot list/call tools,
// and a write-less token cannot mutate.
func TestPrivilegeBoundaryScopeOverHTTP(t *testing.T) {
	// Token with NO scopes at all.
	h, tok := newTestHandler(t, nil, []string{appsplatform.ProductTalk})

	_, resp := rpc(t, h, tok, "tools/list", nil, "application/json")
	if resp.Error == nil {
		t.Fatal("tools/list without apps:read should error")
	}
	_, resp = rpc(t, h, tok, "resources/list", nil, "application/json")
	if resp.Error == nil {
		t.Fatal("resources/list without apps:read should error")
	}
	_, resp = rpc(t, h, tok, "tools/call",
		toolsCallParams{Name: "message.post", Arguments: json.RawMessage(`{"text":"x","target":"general"}`)},
		"application/json")
	if resp.Error == nil {
		t.Fatal("tools/call without apps:write should error")
	}
}

// TestNoCrossActionEscalation confirms that holding apps:write (the method gate
// for tools/call) does NOT let an app invoke an action whose specific scope it
// lacks — i.e. one tool's grant cannot be used to reach another's privilege.
func TestNoCrossActionEscalation(t *testing.T) {
	h, tok := newTestHandler(t, []string{appsplatform.ScopeAppsWrite}, []string{appsplatform.ProductTalk})
	_, resp := rpc(t, h, tok, "tools/call",
		toolsCallParams{Name: "message.post", Arguments: json.RawMessage(`{"text":"x","target":"general"}`)},
		"application/json")
	if resp.Error == nil {
		t.Fatal("apps:write alone must not satisfy the action-specific chat:write scope")
	}
}

// TestUnknownToolRejected confirms calling a tool that does not exist is an
// invalid-params error, not a crash or a silent passthrough.
func TestUnknownToolRejected(t *testing.T) {
	h, tok := newTestHandler(t,
		[]string{appsplatform.ScopeAppsWrite, appsplatform.ScopeChatWrite},
		[]string{appsplatform.ProductTalk})
	_, resp := rpc(t, h, tok, "tools/call", toolsCallParams{Name: "rm.rf", Arguments: json.RawMessage(`{}`)}, "application/json")
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("unknown tool should be invalid-params, got %+v", resp.Error)
	}
}

// TestResourcesReadMalformedURIRejected fuzzes the resource URI surface: bad
// scheme, missing kind, wrong product and junk all error cleanly.
func TestResourcesReadMalformedURIRejected(t *testing.T) {
	h, tok := newTestHandler(t,
		[]string{appsplatform.ScopeAppsRead, appsplatform.ScopeHistoryRead},
		[]string{appsplatform.ProductTalk})
	for _, uri := range []string{
		"",
		"not-a-uri",
		"http://evil.test/history",   // wrong scheme
		"vulos://talk",               // missing kind
		"vulos://talk/",              // empty kind
		"vulos://mail/history/x",     // cross-product
		"file:///etc/passwd",         // local file scheme
		"vulos://talk/history/../..", // traversal-looking target (still just a target string)
	} {
		_, resp := rpc(t, h, tok, "resources/read", resourcesReadParams{URI: uri}, "application/json")
		if resp.Error == nil && uri != "vulos://talk/history/../.." {
			t.Errorf("resources/read(%q) should error", uri)
		}
	}
}

// TestToolCallBadArgumentsRejected (generic passthrough) confirms invalid
// arguments JSON and a missing action are rejected as invalid params.
func TestToolCallBadArgumentsRejected(t *testing.T) {
	reg := appsplatform.NewMemoryRegistry()
	c, _ := reg.Create(appsplatform.CreateParams{Name: "a", OwnerID: "o", Products: []string{appsplatform.ProductTalk}, Scopes: []string{appsplatform.ScopeAppsWrite}})
	h, err := NewHandler(MCPConfig{Adapter: &bareAdapter{}, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	// arguments is a JSON string, not the expected object → invalid params.
	_, resp := rpc(t, h, c.Token, "tools/call", toolsCallParams{Name: genericActTool, Arguments: json.RawMessage(`"oops"`)}, "application/json")
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("bad arguments should be invalid-params, got %+v", resp.Error)
	}
	// Missing action in the generic passthrough → invalid params.
	_, resp = rpc(t, h, c.Token, "tools/call", toolsCallParams{Name: genericActTool, Arguments: json.RawMessage(`{"payload":{}}`)}, "application/json")
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("missing action should be invalid-params, got %+v", resp.Error)
	}
}

// TestMCPToolCallFanoutIsSSRFGuardedAndSigned wires the MCP server's Emit to a
// real appsplatform.Dispatcher and asserts a tool call's follow-up event is
// delivered to the app's webhook through the SAME SSRF-guarded, HMAC-signing
// client the REST surface uses. This is the MCP layer's only outbound path.
func TestMCPToolCallFanoutIsSSRFGuardedAndSigned(t *testing.T) {
	t.Setenv(appsplatform.AllowPrivateWebhooksEnv, "1") // allow the loopback receiver

	type received struct {
		ts, sig string
		body    []byte
	}
	got := make(chan received, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- received{
			ts:   r.Header.Get(appsplatform.SigHeaderTimestamp),
			sig:  r.Header.Get(appsplatform.SigHeaderSignature),
			body: b,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := appsplatform.NewMemoryRegistry()
	c, err := reg.Create(appsplatform.CreateParams{
		Name: "agent", OwnerID: "o", Products: []string{appsplatform.ProductTalk},
		Scopes:     []string{appsplatform.ScopeAppsWrite, appsplatform.ScopeChatWrite},
		WebhookURL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	disp := appsplatform.NewDispatcher(reg, appsplatform.ProductTalk)
	h, err := NewHandler(MCPConfig{Adapter: &describedAdapter{}, Registry: reg, Emit: disp.EmitFunc()})
	if err != nil {
		t.Fatal(err)
	}

	_, resp := rpc(t, h, c.Token, "tools/call",
		toolsCallParams{Name: "message.post", Arguments: json.RawMessage(`{"text":"hi","target":"general"}`)},
		"application/json")
	if resp.Error != nil {
		t.Fatalf("tool call errored: %+v", resp.Error)
	}
	select {
	case rec := <-got:
		if !strings.HasPrefix(rec.sig, "v0=") {
			t.Fatalf("delivered event missing v0= signature: %q", rec.sig)
		}
		if !appsplatform.Verify(rec.ts, rec.body, c.SigningSecret, rec.sig) {
			t.Fatal("MCP-fanned event signature does not verify under the app secret")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("MCP tool call did not fan out a signed webhook event")
	}
}
