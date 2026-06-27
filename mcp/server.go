package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/vul-os/vulos-apps/appsplatform"
)

// Server is the product-agnostic MCP protocol engine. It derives its tools from
// the adapter's Act actions and its resources from the adapter's Read kinds, then
// answers JSON-RPC method calls against an authenticated app. It is transport-
// agnostic: NewHandler wraps it with the Streamable HTTP transport + token auth.
//
// A Server holds no per-connection state; one instance serves all callers.
type Server struct {
	adapter    appsplatform.ProductAdapter
	emit       appsplatform.EmitFunc
	product    string
	tools      []toolEntry
	toolByName map[string]toolEntry
	resources  []resourceEntry
}

// NewServer builds the protocol engine for an adapter. emit may be nil (no event
// fan-out on tool calls); a product that already runs an appsplatform.Dispatcher
// should pass dispatcher.EmitFunc() so MCP tool calls fan out like REST actions.
func NewServer(adapter appsplatform.ProductAdapter, emit appsplatform.EmitFunc) *Server {
	tools, byName := deriveTools(adapter)
	return &Server{
		adapter:    adapter,
		emit:       emit,
		product:    adapter.Product(),
		tools:      tools,
		toolByName: byName,
		resources:  deriveResources(adapter),
	}
}

// Tools returns the derived MCP tool descriptors (for tests / introspection).
func (s *Server) Tools() []Tool {
	out := make([]Tool, len(s.tools))
	for i, e := range s.tools {
		out[i] = e.tool
	}
	return out
}

// Resources returns the derived MCP resource descriptors.
func (s *Server) Resources() []Resource {
	out := make([]Resource, len(s.resources))
	for i, e := range s.resources {
		out[i] = e.resource
	}
	return out
}

// handle dispatches one JSON-RPC request for an authenticated app. It returns
// nil for notifications (no response is sent). Scope gating mirrors the
// appsplatform model: apps:read for the read methods, apps:write for tools/call,
// plus any action/kind-specific scope the adapter declares.
func (s *Server) handle(ctx context.Context, app *appsplatform.App, req *Request) *Response {
	if req.isNotification() {
		// Notifications (e.g. notifications/initialized) are acknowledged at the
		// transport layer; nothing to return.
		return nil
	}
	switch req.Method {
	case "initialize":
		return s.initialize(req)
	case "ping":
		return newResult(req.ID, map[string]any{})
	case "tools/list":
		if resp := s.requireScope(req, app, appsplatform.ScopeAppsRead); resp != nil {
			return resp
		}
		return newResult(req.ID, toolsListResult{Tools: s.Tools()})
	case "tools/call":
		return s.toolsCall(ctx, app, req)
	case "resources/list":
		if resp := s.requireScope(req, app, appsplatform.ScopeAppsRead); resp != nil {
			return resp
		}
		return newResult(req.ID, resourcesListResult{Resources: s.Resources()})
	case "resources/read":
		return s.resourcesRead(ctx, app, req)
	default:
		return newError(req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) initialize(req *Request) *Response {
	var p initializeParams
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &p)
	}
	version := ProtocolVersion
	// Honor the client's requested version if we recognize it; otherwise advertise
	// our own and let the client decide whether to proceed.
	if p.ProtocolVersion != "" {
		version = p.ProtocolVersion
	}
	return newResult(req.ID, initializeResult{
		ProtocolVersion: version,
		Capabilities: capabilities{
			Tools:     &listChangedCap{ListChanged: false},
			Resources: &listChangedCap{ListChanged: false},
		},
		ServerInfo:   serverInfo{Name: ServerName, Version: ServerVersion},
		Instructions: "Tools are this product's actions; resources are its readable content. Authenticate with a Vulos app token (Bearer vat_...).",
	})
}

func (s *Server) toolsCall(ctx context.Context, app *appsplatform.App, req *Request) *Response {
	// tools/call requires the write scope (tools mutate the product surface).
	if resp := s.requireScope(req, app, appsplatform.ScopeAppsWrite); resp != nil {
		return resp
	}
	var p toolsCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return newError(req.ID, codeInvalidParams, "invalid params: "+err.Error())
		}
	}
	entry, ok := s.toolByName[p.Name]
	if !ok {
		return newError(req.ID, codeInvalidParams, "unknown tool: "+p.Name)
	}

	action := entry.spec.Action
	var target string
	var payload json.RawMessage

	if isGenericActSpec(entry.spec) {
		// Generic passthrough: the arguments carry action/target/payload directly.
		var args struct {
			Action  string          `json:"action"`
			Target  string          `json:"target"`
			Payload json.RawMessage `json:"payload"`
		}
		if len(p.Arguments) > 0 {
			if err := json.Unmarshal(p.Arguments, &args); err != nil {
				return newError(req.ID, codeInvalidParams, "invalid arguments: "+err.Error())
			}
		}
		if strings.TrimSpace(args.Action) == "" {
			return newError(req.ID, codeInvalidParams, "arguments.action required")
		}
		action, target, payload = args.Action, args.Target, args.Payload
	} else {
		// Typed tool: the arguments object IS the action payload. If the tool
		// accepts a target, lift it out (without mutating the rest of the payload).
		payload = p.Arguments
		if entry.spec.AcceptsTarget && len(p.Arguments) > 0 {
			var probe struct {
				Target string `json:"target"`
			}
			_ = json.Unmarshal(p.Arguments, &probe)
			target = probe.Target
		}
	}

	// Action-specific scope + target access checks (mirrors appsplatform).
	if resp := s.checkActionScopeAndTarget(req, app, action, target); resp != nil {
		return resp
	}

	result, err := s.adapter.Act(ctx, app, appsplatform.ActionRequest{
		Action:  action,
		Target:  target,
		Payload: payload,
	}, s.emit)
	if err != nil {
		// Tool errors are reported as a tool result with isError per the MCP spec,
		// not as a protocol error, so the model can see and react to them.
		return newResult(req.ID, callToolResult{
			Content: []Content{{Type: "text", Text: err.Error()}},
			IsError: true,
		})
	}
	return newResult(req.ID, callToolResult{Content: []Content{{Type: "text", Text: jsonText(result)}}})
}

func (s *Server) resourcesRead(ctx context.Context, app *appsplatform.App, req *Request) *Response {
	if resp := s.requireScope(req, app, appsplatform.ScopeAppsRead); resp != nil {
		return resp
	}
	var p resourcesReadParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return newError(req.ID, codeInvalidParams, "invalid params: "+err.Error())
		}
	}
	parsed, err := parseResourceURI(p.URI)
	if err != nil {
		return newError(req.ID, codeInvalidParams, err.Error())
	}
	if parsed.product != "" && parsed.product != s.product {
		return newError(req.ID, codeInvalidParams, "resource uri targets a different product: "+parsed.product)
	}

	// Kind-specific scope + target access checks.
	if resp := s.checkActionScopeAndTarget(req, app, parsed.kind, parsed.target); resp != nil {
		return resp
	}

	result, err := s.adapter.Read(ctx, app, appsplatform.ReadRequest{
		Kind:   parsed.kind,
		Target: parsed.target,
		Params: parsed.params,
	})
	if err != nil {
		return newError(req.ID, codeInternalError, err.Error())
	}
	mime := "application/json"
	for _, e := range s.resources {
		if e.spec.Kind == parsed.kind && e.resource.MIMEType != "" {
			mime = e.resource.MIMEType
			break
		}
	}
	return newResult(req.ID, resourcesReadResult{Contents: []resourceContents{{
		URI:      p.URI,
		MIMEType: mime,
		Text:     jsonText(result),
	}}})
}

// requireScope returns an error response if the app lacks scope, else nil.
func (s *Server) requireScope(req *Request, app *appsplatform.App, scope string) *Response {
	if app == nil || !app.HasScope(scope) {
		return newError(req.ID, codeInvalidRequest, "missing required scope: "+scope)
	}
	return nil
}

// checkActionScopeAndTarget enforces the adapter's per-action/kind scope and the
// app's access to the target, mirroring appsplatform's checkScopeAndTarget.
func (s *Server) checkActionScopeAndTarget(req *Request, app *appsplatform.App, actionOrKind, target string) *Response {
	if scope := s.adapter.RequiredScope(actionOrKind); scope != "" && !app.HasScope(scope) {
		return newError(req.ID, codeInvalidRequest, "missing required scope: "+scope)
	}
	if strings.TrimSpace(target) != "" {
		allowed, exists := s.adapter.CanAccessTarget(app, target)
		if !exists {
			return newError(req.ID, codeInvalidParams, "target not found: "+target)
		}
		if !allowed {
			return newError(req.ID, codeInvalidRequest, "app cannot access target: "+target)
		}
	}
	return nil
}

// jsonText marshals a value to compact JSON text for a content block, degrading
// to a Go string representation if marshalling fails.
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
