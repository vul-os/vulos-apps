# vulos-apps — shared Apps & Bots platform

*Part of **VulOS**.*

A reusable, product-agnostic **Apps & Bots platform** that every Vulos product
(**Talk, Mail, Meet, Office**) hosts as its "apps & bots place", and that **Vulos
Workspace** aggregates into one unified surface. A **bot is an app**.

It generalizes Talk's working bot framework into a single shared contract — the
token + signing model, REST surface, HMAC-signed event webhooks, slash commands,
incoming webhooks, and the standalone↔cloud registry seam — widened so the *same*
app manifest serves any product through a small per-product adapter.

> This is the **foundation only**. It does not modify Talk/Mail/Meet/Office/
> Workspace; adoption is a later step. The package is self-contained and
> importable: a Go module for the backend, and a `file:`-installable React lib.

## What's here

```
vulos-apps/
├── go.mod                       module "vulos-apps"
├── appsplatform/                the Go platform package
│   ├── app.go                   App manifest, products, scopes, summaries
│   ├── registry.go / store_standalone.go   Registry seam + SQLite/in-memory default
│   ├── tokens.go / signing.go   hashed tokens (constant-time) + HMAC v0 signing
│   ├── dispatcher.go            outbound events, SSE, slash dispatch
│   ├── adapter.go               ProductAdapter — THE product seam
│   ├── middleware.go            Bearer token auth + session-auth adapter
│   ├── handler.go               the mountable HTTP handler set
│   ├── seam.go                  documented cloud control-plane hook
│   └── *_test.go                tests
├── examples/echo-app/           dependency-free example app (any product)
├── ui/                          @vulos/apps-ui — the shared React component
│   ├── src/AppsAndBots.jsx      <AppsAndBots/> (per-product + aggregate modes)
│   └── dist-lib/                library build output
└── docs/APPS-PLATFORM.md        the full contract
```

## How a product hosts a place

A product implements a `ProductAdapter` (how to post/act/read into *its* surface
— chat message / mail action / meet widget / office tool) and mounts the handler
set with a small composition-root wiring. See **[docs/APPS-PLATFORM.md](docs/APPS-PLATFORM.md)**.

```go
h, _ := appsplatform.NewHandler(appsplatform.MountConfig{
    Adapter: myAdapter, Registry: reg, Dispatcher: disp,
    Admin: reuseProductSession, BasePath: "/api/apps",
})
mux.Handle("/api/apps", h); mux.Handle("/api/apps/", h)
```

The product exposes the common surface: `GET /api/apps` (list — the
consolidation contract), `POST/GET/PUT/DELETE /api/apps`, `…/rotate/token` &
`…/rotate/secret`, the runtime `/api/apps/v1/{auth.test,act,read,events}`, signed
outbound events, slash dispatch, and `/api/apps/hooks/{id}` incoming webhooks.

## How Workspace consolidates

Workspace needs no new backend contract: it calls each product's **`GET
/api/apps`** with that product's session token and merges the identical
`Summary[]` results, grouped by product. The shared component does this directly:

```jsx
import AppsAndBots from "@vulos/apps-ui";
import "@vulos/apps-ui/styles.css";

// Per-product (one product's place):
<AppsAndBots mode="product" product="talk" token={sessionToken} />

// Aggregate (Workspace — across all products):
<AppsAndBots mode="aggregate" sources={[
  { product: "talk",   baseUrl: "https://talk.example",   token: t1 },
  { product: "mail",   baseUrl: "https://mail.example",   token: t2 },
  { product: "meet",   baseUrl: "https://meet.example",   token: t3 },
  { product: "office", baseUrl: "https://office.example", token: t4 },
]} />
```

The component is tokens-only (Authorization: Bearer; no cookies), responsive,
a11y-minded, and styled with Vulos OSS-native tokens.

## Open-core seam discipline

The `Registry` is an interface with a standalone SQLite/in-memory default in the
core. A Vulos Cloud control plane implements the **same interface** in a separate
package the **core never imports** — only a product's `main.go` wires it, and
only when selected. Removing the cloud package never breaks the core build.

## Build & test

```bash
# Go backend
go build ./... && go test ./...

# React component
cd ui && npm install && npm run build      # demo app  -> dist/
        npm run build:lib                  # library   -> dist-lib/

# Run the example app against a product
VULOS_BASE_URL=http://localhost:8080 VULOS_APP_TOKEN=vat_… \
  VULOS_APP_SIGNING_SECRET=vas_… go run ./examples/echo-app
```
