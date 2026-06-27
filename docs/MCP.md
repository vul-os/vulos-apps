# Vulos MCP Layer — Contract

This document describes the **Vulos MCP (Model Context Protocol) layer**: a thin
protocol adapter that lets any LLM/agent operate **any Vulos product** over MCP,
reusing the product's **existing** `appsplatform.ProductAdapter` and scoped-token
auth.

It lives in the `mcp` package of `github.com/vul-os/vulos-apps` and is mounted by
a product exactly like `appsplatform.NewHandler` — MCP is a *different shape* over
the seam each product already implements, not new product code.

---

## The key insight: reuse the ProductAdapter

Every Vulos product already implements one `appsplatform.ProductAdapter`:

| Adapter method        | Meaning                                            |
| --------------------- | -------------------------------------------------- |
| `Act(...)`            | perform a product action (send, post, label, …)    |
| `Read(...)`           | read product content (history, thread, roster, …)  |
| `RequiredScope(x)`    | which scope an action/kind needs                   |
| `CanAccessTarget(...)`| may this app touch this target (channel/doc/…)     |

…guarded by a **scoped Bearer token** (`vat_…`) looked up in a `Registry` in
constant time. MCP maps directly onto that seam:

| MCP concept                      | Vulos source                              | Scope to use  |
| -------------------------------- | ----------------------------------------- | ------------- |
| **tool** (`tools/call`)          | an adapter **`Act` action** (mutating)    | `apps:write`  |
| **resource** (`resources/read`)  | an adapter **`Read` kind**                | `apps:read`   |
| **auth**                         | `@vulos/apps` token (`appsplatform.TokenAuth`) | —        |

Because tools/resources are **generated from the adapter**, every product gets a
correct MCP server for free.

---

## Transport: Streamable HTTP

The server speaks **JSON-RPC 2.0** over the MCP **Streamable HTTP** transport at
a single mount path (default `/mcp`):

| Method   | Path     | Behavior                                                                          |
| -------- | -------- | -------------------------------------------------------------------------------- |
| `POST`   | `{base}` | a JSON-RPC request → a JSON-RPC response. `application/json` by default, or a single `text/event-stream` SSE event when the client's `Accept` includes it. A notification (no `id`) → **202 Accepted**, no body. |
| `GET`    | `{base}` | opens an SSE stream for server→client messages (kept alive with comment pings). The foundation pushes none — this is the server-initiated seam. Requires `Accept: text/event-stream`. |
| `DELETE` | `{base}` | ends a session. This server is **stateless** → `200 OK`.                          |

Implemented JSON-RPC methods: `initialize`, `ping`, `tools/list`, `tools/call`,
`resources/list`, `resources/read`. The advertised protocol version is
`2025-06-18`; on `initialize` the server echoes the client's requested version if
present. JSON-RPC **batching is rejected** (removed in MCP 2025-06-18).

The transport subset is implemented directly (no external SDK) to keep the module
stdlib-only, matching the rest of `vulos-apps`.

---

## Auth model

Every request is **Bearer-token authenticated** against the `Registry`
(`Authorization: Bearer vat_…`), reusing `appsplatform.TokenAuth` — the same
sha256-hash, constant-time path the REST runtime uses. Layered checks:

1. **Token valid** — else `401`. **App targets this product** — else `403`.
2. **Method-level scope**: `tools/list`, `resources/list`, `resources/read` need
   `apps:read`; `tools/call` needs `apps:write`. (`initialize`/`ping` need only a
   valid token.) A missing scope is a JSON-RPC error (`-32600`).
3. **Action/kind scope**: the adapter's `RequiredScope(action|kind)` is enforced
   too (e.g. `chat:write`, `history:read`).
4. **Target access**: if a tool/resource carries a `target`, the adapter's
   `CanAccessTarget` gates it (unknown → invalid-params, forbidden → error).

This mirrors `appsplatform`'s `checkScopeAndTarget` exactly.

---

## Mounting it in a product

A product mounts the MCP handler alongside its `appsplatform` mount, passing the
**same** adapter and registry:

```go
import "github.com/vul-os/vulos-apps/mcp"

// reg, adapter, dispatcher are what the product already built for appsplatform.
h, err := mcp.NewHandler(mcp.MCPConfig{
    Adapter:  adapter,               // the SAME ProductAdapter
    Registry: reg,                   // the SAME token registry
    Emit:     dispatcher.EmitFunc(), // optional: tool calls fan out like REST actions
    // BasePath: "/mcp",             // default
})
if err != nil { log.Fatal(err) }

mux.Handle("/mcp", h)
mux.Handle("/mcp/", h)
```

That is the entire per-product integration (the next step in the roadmap).

---

## Per-product tool/resource derivation

The base `ProductAdapter` exposes `Act`/`Read` but cannot *enumerate* them, so a
product publishes its surface via the **optional** `mcp.Descriptor` interface on
its adapter:

```go
func (a *MyAdapter) MCPTools() []mcp.ToolSpec {
    return []mcp.ToolSpec{{
        Action:        "message.post",          // → ProductAdapter.Act action + tool name
        Description:   "Post a message.",
        AcceptsTarget: true,                     // lifts arguments.target → ActionRequest.Target
        InputSchema:   json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
    }}
}

func (a *MyAdapter) MCPResources() []mcp.ResourceSpec {
    return []mcp.ResourceSpec{{
        Kind:          "history",                // → ProductAdapter.Read kind
        Description:   "Channel message history.",
        AcceptsTarget: true,
    }}
}
```

- A **tool's** `arguments` object becomes the action `Payload`. With
  `AcceptsTarget`, a `target` property is auto-injected into the schema and lifted
  into `ActionRequest.Target` (then access-checked).
- A **resource** is addressed by URI: `vulos://<product>/<kind>[/<target>][?k=v]`.
  Extra query params flow through to `ReadRequest.Params`. The returned value is
  emitted as a JSON text content block.

**Sane defaults if `Descriptor` is not implemented:** the server still works — it
exposes a single generic `act` passthrough tool (set `action`/`target`/`payload`
in its arguments) and an empty `resources/list`. `resources/read` still works for
any `vulos://<product>/<kind>` URI a caller already knows. An individual
`ToolSpec` with a `nil` `InputSchema` also defaults to a permissive object schema.

---

## Open-core / self-host escape hatch

The MCP server **ships in the OSS product** and runs **standalone**:

1. Self-host the product.
2. Install an app and mint a token (`apps:read`/`apps:write` scopes) via the
   product's apps UI / API.
3. Point any MCP agent at `https://<product>/mcp` with
   `Authorization: Bearer vat_…`.

No cloud dependency. An **optional cloud aggregating MCP gateway** (one agent
endpoint fanning out across products) is left as a documented **env-gated seam**,
`MCPConfig.Gateway`:

```go
type Gateway interface {
    RegisterServer(product, basePath string, srv *Server) error
}
```

When non-nil, `NewHandler` registers the server with the gateway. The OSS core
**never imports** a `Gateway` implementation — only a Vulos Cloud composition root
wires one, and only when explicitly selected. This mirrors the `Registry`
standalone↔cloud seam: removing the cloud package never breaks the OSS build.

---

## Example / tests

- `mcp/example_test.go` — a runnable `Example` mounting a fake "notes" product and
  driving `initialize → tools/list → tools/call` over HTTP with a token.
- `mcp/server_test.go` — tool/resource derivation (Descriptor + generic
  fallback), scope gating, target access, URI parsing, method dispatch.
- `mcp/handler_test.go` — HTTP transport: auth (401/403), cross-product rejection,
  the JSON-RPC round-trip, SSE response framing, notification 202, and the gateway
  seam.
