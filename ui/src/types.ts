// Domain model for the Vulos Apps & Bots platform wire contract.
//
// These types mirror the Go types in appsplatform/app.go (and the request/
// response shapes in appsplatform/handler.go) so the HTTP contract is typed on
// BOTH sides: the Go halves produce it, these consumers inherit it.

// ProductId is the closed set of product identifiers the platform knows about
// (appsplatform.ValidProducts). Strings are accepted where products may define
// their own, but these four are always understood.
export type ProductId = "talk" | "mail" | "meet" | "office";

// Scope is a permission string. The platform's built-in scope set is the generic
// apps:read / apps:write plus the Talk-compatible scopes; products MAY register
// their own, so the type stays open (`string`) while documenting the known set.
export type KnownScope =
  | "apps:read"
  | "apps:write"
  | "chat:write"
  | "history:read"
  | "channels:read"
  | "members:read"
  | "reactions:write";
export type Scope = KnownScope | (string & {});

// SlashCommand is a slash command an app registers. `name` is stored WITHOUT the
// leading slash (e.g. "deploy", not "/deploy"). Mirrors appsplatform.SlashCommand.
export interface SlashCommand {
  name: string;
  description: string;
}

// IncomingWebhook is the unauthenticated inbound webhook for an app. The ID in
// the URL is itself the secret. `url` is populated in summaries (relative path).
// Mirrors appsplatform.IncomingWebhook.
export interface IncomingWebhook {
  id: string;
  enabled: boolean;
  url?: string;
}

// AppSummary is the secret-free public view of an app — EXACTLY the shape
// returned by GET /api/apps, the consolidation contract Workspace aggregates.
// Mirrors appsplatform.Summary (json tags). Secrets (token hash / signing
// secret) are never present here.
export interface AppSummary {
  id: string;
  name: string;
  icon: string;
  description: string;
  scopes: Scope[];
  products: ProductId[] | string[];
  events: string[];
  slash_commands: SlashCommand[];
  webhook_url: string;
  incoming_webhook: IncomingWebhook;
  owner_id: string;
  default_target?: string;
  created_at: string; // RFC3339 timestamp
}

// Manifest is the App manifest as authored by an installer. It is the input to
// `create` and the conceptual superset of AppSummary; mirrors the request struct
// in appsplatform handler.create (plus the create defaults). Secrets are issued
// by the server, never supplied here.
export interface Manifest {
  name: string;
  icon?: string;
  description?: string;
  scopes?: Scope[];
  products?: (ProductId | string)[];
  events?: string[];
  slash_commands?: SlashCommand[];
  webhook_url?: string;
  default_target?: string;
  // Defaults to true server-side; set false to install without an inbound hook.
  incoming_enabled?: boolean;
}

// UpdateManifest is the patch body for PUT /api/apps/{id}. Every field is
// optional; mirrors appsplatform handler.update (pointer fields).
export type UpdateManifest = Partial<Manifest>;

// CreateAppResponse is the 201 body of POST /api/apps. The token + signing
// secret are shown ONCE here and never recoverable (rotate to re-issue).
export interface CreateAppResponse {
  app: AppSummary;
  token: string;
  signing_secret: string;
  incoming_webhook_url: string;
}

// RotateTokenResponse / RotateSecretResponse — the one-time reveal of a rotated
// secret (POST /api/apps/{id}/rotate/token | /secret).
export interface RotateTokenResponse {
  token: string;
}
export interface RotateSecretResponse {
  signing_secret: string;
}

// RemoveResponse is the body of DELETE /api/apps/{id}.
export interface RemoveResponse {
  ok: boolean;
}
