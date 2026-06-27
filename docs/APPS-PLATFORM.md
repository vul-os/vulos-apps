# Vulos Apps & Bots Platform — Contract

This document describes the **shared Vulos Apps & Bots platform**: a
product-agnostic system each Vulos product (**Talk, Mail, Meet, Office**) hosts
as its "apps & bots place", and that **Vulos Workspace** aggregates into one
unified surface.

A **bot is an app**. The platform generalizes Talk's existing bot framework
(`vulos-talk/backend/bots` + `docs/BOT-API.md`) into a reusable contract: the
token + signing model, the REST surface, HMAC-signed event webhooks, slash
commands, incoming webhooks, and the standalone↔cloud registry seam are all kept
the **same shape** as Talk's so Talk can migrate cleanly — with the model widened
so the *same* app contract serves every product via a small per-product adapter.

All paths below are relative to a product origin (e.g. `https://talk.example`)
and a configurable **base path** (default `/api/apps`).

---

## Concepts

### App (the manifest)

An **App** has:

| Field              | Meaning                                                                 |
| ------------------ | ----------------------------------------------------------------------- |
| `id`               | Server-assigned id. The app posts/acts as account `app:<id>`.           |
| `name`             | Display name (used for `@name` mentions where a product supports them). |
| `icon`             | Emoji or icon name/URL for the UI.                                      |
| `description`      | Human description shown in the apps place.                              |
| `owner_id`         | Account that installed the app (management API is owner-scoped).        |
| `org_id`           | Tenant id. Empty in OSS / standalone.                                   |
| `scopes[]`         | Permissions (see Scopes).                                               |
| `products[]`       | Which products it targets: `talk` \| `mail` \| `meet` \| `office`.      |
| `events[]`         | Event types it subscribes to. **Empty = all** event types.             |
| `slash_commands[]` | `[{name, description}]` — names stored without the leading slash.       |
| `webhook_url`      | Optional outbound webhook for signed events.                           |
| `incoming_webhook` | `{id, enabled, url}` — the unauthenticated inbound webhook.            |
| `default_target`   | Generic fallback target (channel / folder / room / doc).               |
| `created_at`       | Creation time.                                                          |

A **product targeting** model is the key generalization over Talk: one app may
target several products, and each product's HTTP surface only lists/serves apps
that target *it*.

### Secrets

- **App token** — a Bearer secret (prefix `vat_`). **Only its sha256 hash is
  stored at rest.** The plaintext is shown **once** at create/rotate and can
  never be recovered — rotate to get a new one.
- **Signing secret** — signs outbound events (prefix `vas_`). Stored **as-is**
  (not hashed) because the platform must reproduce it to compute the HMAC on
  every outbound event. Treat the app record as sensitive at rest.

### Scopes

The platform ships generic, product-agnostic scopes plus the Talk-compatible set
(for clean migration). Unknown scope strings are rejected at create/update time.
Products may register **their own** scope set (`appsplatform.NewScopeSet(...)`).

| Scope             | Grants (convention)                          |
| ----------------- | -------------------------------------------- |
| `apps:read`       | Read product content the app can see.        |
| `apps:write`      | Act / post into the product surface.         |
| `chat:write`      | (Talk) post messages / reactions write path. |
| `history:read`    | (Talk) read channel history.                 |
| `channels:read`   | (Talk) list channels.                        |
| `members:read`    | (Talk) list members.                         |
| `reactions:write` | (Talk) add/remove reactions.                 |

**Which scope a runtime action requires is decided by the product adapter**
(`RequiredScope`), so each product enforces its own granularity while the
platform owns the check. An app with no scope can still call `auth.test`.

### Product targeting + access

- The runtime API rejects (`403`) a token whose app does not target the mounted
  product.
- Per-target visibility (private channel / restricted folder / room / doc) is
  enforced by the adapter's `CanAccessTarget` → `404` (not found) or `403`
  (forbidden).

---

## Management API (product session authed)

Base: `/api/apps`. Authenticated with the **product's own session** — the
platform calls back the product via an `AdminAuthFunc` adapter, so each product
reuses its existing session mechanism (JWT bearer / cookie). **Owner-scoped**: a
caller manages only apps they own; an admin manages all. Cross-owner access
returns `404` (no existence leak). Only apps targeting the mounted product are
visible.

`Summary` JSON never includes secrets:

```json
{
  "id": "…", "name": "Echo", "icon": "🔁", "description": "…",
  "scopes": ["chat:write"], "products": ["talk"], "events": [],
  "slash_commands": [{"name":"echo","description":"…"}],
  "webhook_url": "", "incoming_webhook": {"id":"…","enabled":true,"url":"/api/apps/hooks/…"},
  "owner_id": "alice", "default_target": "general", "created_at": "…"
}
```

| Method & path                      | Body                                                                                   | Response                                                                |
| ---------------------------------- | -------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `GET /api/apps`                    | —                                                                                      | `[Summary]` — **the consolidation contract Workspace reads**           |
| `POST /api/apps`                   | `{name, icon?, description?, scopes?, products?, events?, slash_commands?, webhook_url?, default_target?, incoming_enabled?}` | `{app, token, signing_secret, incoming_webhook_url}` (secrets shown once) |
| `GET /api/apps/{id}`               | —                                                                                      | `Summary`                                                              |
| `PUT /api/apps/{id}`               | any subset of the create fields                                                        | `Summary` (absent fields unchanged)                                   |
| `DELETE /api/apps/{id}`            | —                                                                                      | `{ok:true}`                                                           |
| `POST /api/apps/{id}/rotate/token` | —                                                                                      | `{token}` (shown once)                                                |
| `POST /api/apps/{id}/rotate/secret`| —                                                                                      | `{signing_secret}` (shown once)                                       |
| `GET /api/apps/commands`           | —                                                                                      | `[{name, description, app_id}]` — slash catalog for autocomplete       |

> `POST /api/apps` with no `products` defaults to targeting the hosting product,
> so "install into this place" just works. Rotate routes are nested one level
> deeper than Talk's `rotate-token` so the standard-library router never sees an
> ambiguous overlap with the `hooks/{id}` route.

---

## Runtime API (Bearer app-token authed)

Base: `/api/apps/v1`. Authenticate with the **app token**:

```
Authorization: Bearer vat_xxxxxxxx…
```

The token is looked up by its sha256 hash (**constant-time**); unknown/missing →
`401`. The app must target the mounted product → else `403`.

| Method & path                | Notes                                                                                         |
| ---------------------------- | --------------------------------------------------------------------------------------------- |
| `GET /api/apps/v1/auth.test` | `{app_id, name, scopes, products}`. No scope needed.                                           |
| `POST /api/apps/v1/act`      | Body `{action, target?, payload?}`. Generic action — the product **adapter** performs it (chat post / mail action / meet widget / office tool). Scope = `adapter.RequiredScope(action)`; target visibility enforced. |
| `GET /api/apps/v1/read`      | Query `?kind=…&target=…&…`. Generic read — the adapter returns product content (history / thread / roster / doc range). |
| `GET /api/apps/v1/events`    | SSE event stream (socket-mode style), authenticated by the token over TLS.                     |

The `act`/`read` envelopes are deliberately generic so one contract spans all
products; the **`payload` is product-specific** and opaque to the platform. The
Talk convention is `action:"message.post"`, `target:"<channel>"`,
`payload:{text}`.

---

## Incoming webhooks (simplest integration)

`POST /api/apps/hooks/{id}` — **no auth header**; the `{id}` in the URL is itself
the secret. Disabled or unknown webhook → `404`. The raw body is handed to the
adapter as an `incoming_webhook` action with `target` defaulting to the app's
`default_target`:

```bash
curl -X POST https://talk.example/api/apps/hooks/$WEBHOOK_ID \
  -H 'Content-Type: application/json' -d '{"text":"deploy finished ✅"}'
```

---

## Outbound events (signed webhooks)

When an app has `webhook_url` set, the platform POSTs JSON events to it
(fire-and-forget, ~5s timeout, never blocking the originating request).

### Headers (Slack-compatible v0 — identical to Talk)

```
Content-Type: application/json
X-Vulos-Request-Timestamp: <unix seconds>
X-Vulos-Signature: v0=<hex hmac-sha256>
```

`basestring = "<timestamp>" + "." + <raw body>`; sign with HMAC-SHA256 under the
signing secret. **Verify** by recomputing over the timestamp + raw body and
comparing in constant time; reject stale timestamps (the example app uses a
5-minute window) to blunt replay.

### Event envelope

```json
{ "type": "app_mention", "app_id": "…", "product": "talk",
  "event": { /* per-type payload */ }, "event_time": 1700000000 }
```

Common event types (mirroring Talk; products may define their own):
`message.created`, `app_mention`, `member_joined`, `slash_command`. An app
receives only the types in its `events[]` (empty = all).

---

## Slash commands

An app registers command names (without the slash) in `slash_commands`, scoped to
the products it targets. On a product's send path, `Dispatcher.MaybeHandleSlash`
intercepts a body whose first token matches a registered command for that
product, emits a `slash_command` event, and tells the caller not to store it.

---

## The registry seam (standalone vs. cloud control plane)

The `Registry` is an **interface**. The **standalone default**
(`StandaloneRegistry`) lives in the core package, backed by pure-Go SQLite with
an in-memory fallback (tokens stored as sha256 hashes; signing secrets as-is). A
**Vulos Cloud developer console / control plane** implements the **same
interface** in a **separate package the core never imports**; only a product's
composition root wires it, and only when explicitly selected. Removing the cloud
package never breaks the core build. The data plane (HTTP surface, dispatcher,
signing, webhooks, SSE) depends only on the interface. See `appsplatform/seam.go`.

---

## How a product hosts an apps & bots place

A product supplies a **`ProductAdapter`** (how to post/act/read in *its* surface)
and mounts the handler set:

```go
reg, _ := appsplatform.NewStandaloneRegistry(cfg.AppsDB) // or a cloud Registry
disp := appsplatform.NewDispatcher(reg, appsplatform.ProductTalk)

h, _ := appsplatform.NewHandler(appsplatform.MountConfig{
    Adapter:    talkAdapter{spaces: store, disp: disp}, // implements ProductAdapter
    Registry:   reg,
    Dispatcher: disp,
    Admin:      func(r *http.Request) (string, bool, bool) { /* reuse product session */ },
    BasePath:   "/api/apps",
})
mux.Handle("/api/apps", h)   // mount at the base
mux.Handle("/api/apps/", h)  // and its subtree
```

On its own send path the product calls `disp.MaybeHandleSlash(...)` to intercept
slash commands and `disp.Emit(...)` (or the `EmitFunc` handed to `Act`) to fan
events out to subscribed apps.

### The adapter interface

```go
type ProductAdapter interface {
    Product() string
    RequiredScope(actionOrKind string) string
    CanAccessTarget(app *App, target string) (allowed, exists bool)
    Act(ctx, app, ActionRequest, EmitFunc) (any, error)
    Read(ctx, app, ReadRequest) (any, error)
}
```

The platform owns auth, token hashing, product-targeting and scope enforcement;
the adapter owns product-native semantics.

---

## How Workspace consolidates

Vulos Workspace does **not** need a new backend contract: it simply calls each
product's **`GET /api/apps`** (the same management list endpoint, with that
product's session token) and merges the `Summary[]` results, grouped by product.
Because every product exposes the identical `Summary` shape, the aggregate
surface is a pure read-fan-out. The shared `<AppsAndBots mode="aggregate"
sources={[…]} />` component does exactly this in the UI.
