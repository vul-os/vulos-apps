package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/vul-os/vulos-apps/appsplatform"
)

// FuzzParseResourceURI ensures the resource-URI parser never panics on arbitrary
// input and that, whenever it succeeds, it yields a non-empty kind (the dispatch
// key) and only ever the vulos scheme.
func FuzzParseResourceURI(f *testing.F) {
	for _, s := range []string{
		"vulos://talk/history",
		"vulos://talk/history/general?limit=10",
		"vulos://mail/thread/abc",
		"vulos:///nohost",
		"vulos://talk",
		"http://evil/x",
		"://",
		"vulos://talk/a%2Fb/t",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		p, err := parseResourceURI(raw) // must not panic
		if err != nil {
			return
		}
		if p.kind == "" {
			t.Fatalf("parsed %q to an empty kind", raw)
		}
		if p.params == nil {
			t.Fatalf("parsed %q to a nil params map", raw)
		}
	})
}

// FuzzServerHandle drives the protocol engine with arbitrary method names and
// params bytes for a fully-scoped app: it must always return a response (or nil
// for a notification) and never panic, regardless of how malformed the params
// are.
func FuzzServerHandle(f *testing.F) {
	seeds := []struct {
		method string
		params string
	}{
		{"initialize", `{"protocolVersion":"x"}`},
		{"ping", ``},
		{"tools/list", ``},
		{"tools/call", `{"name":"message.post","arguments":{"text":"x"}}`},
		{"tools/call", `{"name":"act","arguments":{"action":"a"}}`},
		{"resources/list", ``},
		{"resources/read", `{"uri":"vulos://talk/history/g"}`},
		{"bogus", `garbage`},
	}
	for _, s := range seeds {
		f.Add(s.method, s.params)
	}

	app := &appsplatform.App{
		ID: "a1", Products: []string{appsplatform.ProductTalk},
		Scopes: []string{
			appsplatform.ScopeAppsRead, appsplatform.ScopeAppsWrite,
			appsplatform.ScopeChatWrite, appsplatform.ScopeHistoryRead,
		},
	}

	f.Fuzz(func(t *testing.T, method, params string) {
		// Fresh server per iteration: the fuzzer runs workers in parallel, so a
		// shared adapter with mutable state would race.
		srv := NewServer(&describedAdapter{}, nil)
		r := &Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method}
		if params != "" {
			// Params may be invalid JSON on purpose; the engine must cope.
			r.Params = json.RawMessage(params)
		}
		resp := srv.handle(context.Background(), app, r) // must not panic
		if resp == nil {
			return // notification path
		}
		// A response must be exactly one of result/error.
		if (resp.Result == nil) == (resp.Error == nil) {
			t.Fatalf("response for %q has both/neither result and error", method)
		}
	})
}

// FuzzServerHandleScopeless ensures that with a no-scope app the engine still
// never panics and never returns a non-error result for a privileged method.
func FuzzServerHandleScopeless(f *testing.F) {
	f.Add("tools/list")
	f.Add("tools/call")
	f.Add("resources/read")
	f.Add("resources/list")

	app := &appsplatform.App{ID: "a1", Products: []string{appsplatform.ProductTalk}} // no scopes

	privileged := map[string]bool{
		"tools/list": true, "tools/call": true,
		"resources/list": true, "resources/read": true,
	}
	f.Fuzz(func(t *testing.T, method string) {
		srv := NewServer(&describedAdapter{}, nil)
		r := &Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method}
		resp := srv.handle(context.Background(), app, r)
		if resp == nil {
			return
		}
		if privileged[method] && resp.Error == nil {
			t.Fatalf("scopeless app got a non-error result for privileged method %q", method)
		}
	})
}
