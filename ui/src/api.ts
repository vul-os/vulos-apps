// API client for the Vulos Apps & Bots platform HTTP surface.
//
// Tokens-only: every request carries an Authorization: Bearer <token> header
// (the product's session token), never relies on ambient cookies. A custom
// `fetcher` may be injected (tests / SSR); it defaults to window.fetch.

import type {
  AppSummary,
  CreateAppResponse,
  Manifest,
  ProductId,
  RemoveResponse,
  RotateSecretResponse,
  RotateTokenResponse,
  SlashCommand,
  UpdateManifest,
} from "./types";

// Fetcher is the fetch-compatible function the client calls. Defaults to the
// global `fetch`; injectable for tests / SSR.
export type Fetcher = (
  input: string,
  init?: RequestInit,
) => Promise<Response>;

// MakeClientOptions configures a client for ONE product surface.
export interface MakeClientOptions {
  baseUrl?: string;
  basePath?: string;
  token?: string;
  fetcher?: Fetcher;
}

// AppsClient is the typed handle over the platform's management REST surface.
export interface AppsClient {
  /** The consolidation contract Workspace reads: list installed apps. */
  list: () => Promise<AppSummary[]>;
  create: (manifest: Manifest) => Promise<CreateAppResponse>;
  get: (id: string) => Promise<AppSummary>;
  update: (id: string, patch: UpdateManifest) => Promise<AppSummary>;
  remove: (id: string) => Promise<RemoveResponse>;
  rotateToken: (id: string) => Promise<RotateTokenResponse>;
  rotateSecret: (id: string) => Promise<RotateSecretResponse>;
  commands: () => Promise<SlashCommand[]>;
}

// ApiError is thrown for non-2xx responses; `status` carries the HTTP code.
export interface ApiError extends Error {
  status?: number;
}

export function makeClient({
  baseUrl = "",
  basePath = "/api/apps",
  token,
  fetcher,
}: MakeClientOptions = {}): AppsClient {
  const f: Fetcher = fetcher || ((input, init) => fetch(input, init));
  const root = `${baseUrl}${basePath}`;

  async function call<T>(
    method: string,
    path = "",
    body?: unknown,
  ): Promise<T> {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await f(`${root}${path}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    let data: unknown = null;
    try {
      data = text ? JSON.parse(text) : null;
    } catch {
      data = text;
    }
    if (!res.ok) {
      const msg =
        (data && typeof data === "object" && "error" in data
          ? String((data as { error?: unknown }).error)
          : "") ||
        res.statusText ||
        `HTTP ${res.status}`;
      const err = new Error(msg) as ApiError;
      err.status = res.status;
      throw err;
    }
    return data as T;
  }

  return {
    list: () => call<AppSummary[]>("GET", ""),
    create: (manifest) => call<CreateAppResponse>("POST", "", manifest),
    get: (id) => call<AppSummary>("GET", `/${id}`),
    update: (id, patch) => call<AppSummary>("PUT", `/${id}`, patch),
    remove: (id) => call<RemoveResponse>("DELETE", `/${id}`),
    rotateToken: (id) =>
      call<RotateTokenResponse>("POST", `/${id}/rotate/token`),
    rotateSecret: (id) =>
      call<RotateSecretResponse>("POST", `/${id}/rotate/secret`),
    commands: () => call<SlashCommand[]>("GET", "/commands"),
  };
}

// ProductMeta is the display metadata used by the aggregate (Workspace) grouping.
export interface ProductMeta {
  id: ProductId;
  label: string;
  icon: string;
  glyph: string;
}

// The four products the platform knows about, with display metadata used by the
// aggregate (Workspace) grouping.
export const PRODUCTS: ProductMeta[] = [
  { id: "talk", label: "Talk", icon: "speech_balloon", glyph: "\u{1F4AC}" },
  { id: "mail", label: "Mail", icon: "envelope", glyph: "✉️" },
  { id: "meet", label: "Meet", icon: "camera", glyph: "\u{1F4F9}" },
  { id: "office", label: "Office", icon: "memo", glyph: "\u{1F4DD}" },
];

export const ALL_SCOPES: string[] = [
  "apps:read",
  "apps:write",
  "chat:write",
  "history:read",
  "channels:read",
  "members:read",
  "reactions:write",
];
