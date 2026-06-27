// API client for the Vulos Apps & Bots platform HTTP surface.
//
// Tokens-only: every request carries an Authorization: Bearer <token> header
// (the product's session token), never relies on ambient cookies. A custom
// `fetcher` may be injected (tests / SSR); it defaults to window.fetch.

export function makeClient({ baseUrl = "", basePath = "/api/apps", token, fetcher } = {}) {
  const f = fetcher || ((...a) => fetch(...a));
  const root = `${baseUrl}${basePath}`;

  async function call(method, path = "", body) {
    const headers = { Accept: "application/json" };
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await f(`${root}${path}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    let data = null;
    try {
      data = text ? JSON.parse(text) : null;
    } catch {
      data = text;
    }
    if (!res.ok) {
      const msg = (data && data.error) || res.statusText || `HTTP ${res.status}`;
      const err = new Error(msg);
      err.status = res.status;
      throw err;
    }
    return data;
  }

  return {
    // The consolidation contract Workspace reads: list installed apps.
    list: () => call("GET", ""),
    create: (manifest) => call("POST", "", manifest),
    get: (id) => call("GET", `/${id}`),
    update: (id, patch) => call("PUT", `/${id}`, patch),
    remove: (id) => call("DELETE", `/${id}`),
    rotateToken: (id) => call("POST", `/${id}/rotate/token`),
    rotateSecret: (id) => call("POST", `/${id}/rotate/secret`),
    commands: () => call("GET", "/commands"),
  };
}

// The four products the platform knows about, with display metadata used by the
// aggregate (Workspace) grouping.
export const PRODUCTS = [
  { id: "talk", label: "Talk", icon: "speech_balloon", glyph: "\u{1F4AC}" },
  { id: "mail", label: "Mail", icon: "envelope", glyph: "✉️" },
  { id: "meet", label: "Meet", icon: "camera", glyph: "\u{1F4F9}" },
  { id: "office", label: "Office", icon: "memo", glyph: "\u{1F4DD}" },
];

export const ALL_SCOPES = [
  "apps:read",
  "apps:write",
  "chat:write",
  "history:read",
  "channels:read",
  "members:read",
  "reactions:write",
];
