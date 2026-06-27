// Package mcp is the Vulos Model Context Protocol (MCP) layer: a thin protocol
// adapter that lets any LLM/agent operate a Vulos product over MCP, reusing the
// product's EXISTING appsplatform.ProductAdapter and scoped-token auth.
//
// The key insight is that each product already implements an
// appsplatform.ProductAdapter — Act (actions) + Read (kinds) — guarded by a
// scoped Bearer token (vat_) Registry. MCP is just a different shape over the
// same seam:
//
//	MCP tools     = the adapter's Act actions   (apps:write to call)
//	MCP resources = the adapter's Read kinds     (apps:read to read/list)
//	MCP auth      = the @vulos/apps scoped token (Bearer vat_, constant-time)
//
// A product mounts this exactly like appsplatform.NewHandler:
//
//	h, _ := mcp.NewHandler(mcp.MCPConfig{
//	    Adapter:  myAdapter,          // the SAME adapter the product already has
//	    Registry: reg,                // the SAME app registry (token auth)
//	    Emit:     dispatcher.EmitFunc(),
//	})
//	mux.Handle("/mcp", h)
//	mux.Handle("/mcp/", h)
//
// Tools and resources are GENERATED from the adapter, so every product gets a
// correct MCP server for free. An adapter that implements the optional Descriptor
// interface describes its tools/resources precisely; otherwise a sane generic
// passthrough is exposed.
//
// Open-core escape hatch: this server ships in the OSS product and runs
// standalone — self-host the product, mint an app token, and point any MCP agent
// at /mcp with that token. An optional cloud aggregating gateway is an env-gated
// seam (MCPConfig.Gateway) that the OSS core never imports.
package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vul-os/vulos-apps/appsplatform"
)

// MCPConfig configures the mountable MCP handler a product embeds.
type MCPConfig struct {
	// Adapter is the product seam (required) — the SAME appsplatform.ProductAdapter
	// the product already implements. Its Act actions become MCP tools and its Read
	// kinds become MCP resources. Implement the optional Descriptor interface on it
	// to publish precise tool/resource schemas.
	Adapter appsplatform.ProductAdapter

	// Registry authenticates Bearer app tokens (required) — the SAME registry the
	// product's appsplatform mount uses. Tokens are looked up by sha256 hash in
	// constant time (see appsplatform.TokenAuth).
	Registry appsplatform.Registry

	// Emit optionally fans a follow-up platform event out when a tool call acts
	// (pass dispatcher.EmitFunc() so MCP tool calls behave like REST actions). nil
	// disables fan-out.
	Emit appsplatform.EmitFunc

	// BasePath is the mount prefix (default "/mcp").
	BasePath string

	// Gateway is the OPTIONAL cloud-aggregation seam. When non-nil (only ever wired
	// by a Vulos Cloud composition root, never in the OSS build), the server
	// registers itself with the aggregating gateway so one agent endpoint can fan
	// out across products. nil — the default and the only OSS behavior — is
	// standalone: an agent connects straight to this /mcp with a token. The OSS
	// core never imports a Gateway implementation (mirrors the Registry seam).
	Gateway Gateway
}

// Gateway is the optional cloud MCP-aggregation seam. A Vulos Cloud control plane
// implements it in a separate package this core never imports; the product's
// composition root wires it only when explicitly selected (env-gated). The
// foundation only leaves the hook — it builds no gateway.
type Gateway interface {
	// RegisterServer announces a product's MCP server to the aggregating gateway.
	RegisterServer(product, basePath string, srv *Server) error
}

// Handler is the mounted MCP handler plus its resolved base path and engine.
type Handler struct {
	http.Handler
	BasePath string
	Server   *Server
}

// NewHandler wires the Streamable HTTP MCP transport over the config. The
// returned handler answers JSON-RPC 2.0 at BasePath:
//
//	POST   {base}   a JSON-RPC request → a JSON-RPC response (application/json,
//	                or text/event-stream when the client's Accept asks for SSE).
//	                A notification (no id) → 202 Accepted.
//	GET    {base}   opens an SSE stream for server-initiated messages (kept alive;
//	                the foundation pushes none — this is the server→client seam).
//	DELETE {base}   ends a session (this server is stateless → 200 OK).
//
// Every request is Bearer-token authenticated against the Registry and the app
// must target this product. Method-level scope (apps:read / apps:write) and the
// adapter's per-action scope + target access are enforced inside the engine.
func NewHandler(cfg MCPConfig) (*Handler, error) {
	if cfg.Adapter == nil {
		return nil, errors.New("mcp: MCPConfig.Adapter is required")
	}
	if cfg.Registry == nil {
		return nil, errors.New("mcp: MCPConfig.Registry is required")
	}
	base := cfg.BasePath
	if base == "" {
		base = "/mcp"
	}
	base = "/" + strings.Trim(base, "/")

	srv := NewServer(cfg.Adapter, cfg.Emit)
	if cfg.Gateway != nil {
		if err := cfg.Gateway.RegisterServer(cfg.Adapter.Product(), base, srv); err != nil {
			return nil, err
		}
	}

	h := &transport{cfg: cfg, base: base, product: cfg.Adapter.Product(), server: srv}
	mux := http.NewServeMux()
	tok := func(fn http.HandlerFunc) http.Handler { return appsplatform.TokenAuth(cfg.Registry, fn) }
	for _, pat := range []string{base, base + "/"} {
		mux.Handle("POST "+pat, tok(h.post))
		mux.Handle("GET "+pat, tok(h.get))
		mux.Handle("DELETE "+pat, tok(h.delete))
	}
	return &Handler{Handler: mux, BasePath: base, Server: srv}, nil
}

type transport struct {
	cfg     MCPConfig
	base    string
	product string
	server  *Server
}

const maxBody = 1 << 20 // 1 MiB request cap, matching appsplatform.

// runtimeApp pulls the token-authed app and enforces product targeting.
func (t *transport) runtimeApp(w http.ResponseWriter, r *http.Request) (*appsplatform.App, bool) {
	a, ok := appsplatform.AppFromContext(r.Context())
	if !ok || a == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "app not authenticated"})
		return nil, false
	}
	if !a.TargetsProduct(t.product) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "app does not target this product"})
		return nil, false
	}
	return a, true
}

// post handles a JSON-RPC request/notification over HTTP POST.
func (t *transport) post(w http.ResponseWriter, r *http.Request) {
	app, ok := t.runtimeApp(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "read body"})
		return
	}
	if i := firstNonSpace(body); i >= 0 && body[i] == '[' {
		// JSON-RPC batching was removed in MCP 2025-06-18; reject clearly.
		writeRPC(w, r, newError(nil, codeInvalidRequest, "batch requests are not supported"))
		return
	}
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, r, newError(nil, codeParseError, "parse error: "+err.Error()))
		return
	}
	if req.JSONRPC != jsonRPCVersion {
		writeRPC(w, r, newError(req.ID, codeInvalidRequest, "jsonrpc must be \"2.0\""))
		return
	}
	resp := t.server.handle(r.Context(), app, &req)
	if resp == nil {
		// Notification: acknowledge with 202 and no body, per the transport spec.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeRPC(w, r, resp)
}

// get opens an SSE stream for server→client messages. The foundation emits none,
// so the stream is a kept-alive seam; it closes when the client disconnects.
func (t *transport) get(w http.ResponseWriter, r *http.Request) {
	if _, ok := t.runtimeApp(w, r); !ok {
		return
	}
	if !accepts(r, "text/event-stream") {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET requires Accept: text/event-stream"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming unsupported"})
		return
	}
	sseHeaders(w)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			// SSE comment keepalive (ignored by clients) so proxies hold the stream.
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// delete ends a session. This server is stateless, so there is nothing to tear
// down; acknowledge so clients that send DELETE on shutdown are satisfied.
func (t *transport) delete(w http.ResponseWriter, r *http.Request) {
	if _, ok := t.runtimeApp(w, r); !ok {
		return
	}
	w.WriteHeader(http.StatusOK)
}

// writeRPC writes a JSON-RPC response either as a single SSE event (when the
// client's Accept asks for text/event-stream) or as application/json.
func writeRPC(w http.ResponseWriter, r *http.Request, resp *Response) {
	body, err := json.Marshal(resp)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "encode response"})
		return
	}
	if accepts(r, "text/event-stream") {
		if flusher, ok := w.(http.Flusher); ok {
			sseHeaders(w)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "event: message\ndata: ")
			_, _ = w.Write(body)
			_, _ = io.WriteString(w, "\n\n")
			flusher.Flush()
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

// accepts reports whether the request's Accept header includes mediaType (or *).
func accepts(r *http.Request, mediaType string) bool {
	a := r.Header.Get("Accept")
	return strings.Contains(a, mediaType) || strings.Contains(a, "*/*")
}

func firstNonSpace(b []byte) int {
	for i, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return i
		}
	}
	return -1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
