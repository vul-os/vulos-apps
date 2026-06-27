// mockFetcher simulates the Vulos Apps & Bots HTTP surface in-memory so the demo
// (and the library build) runs with no backend. It implements the same routes
// the real Go handler serves: GET/POST {base}, GET/PUT/DELETE {base}/{id},
// POST {base}/{id}/rotate/token|secret. One store per product keyed by baseUrl.
export function makeMockFetcher() {
  const db = {}; // baseUrl -> [apps]
  let seq = 1;

  function store(baseUrl) {
    if (!db[baseUrl]) db[baseUrl] = [];
    return db[baseUrl];
  }
  function rand(p) {
    return p + Math.random().toString(16).slice(2, 18);
  }
  function summary(a, base) {
    const { token_hash, signing_secret, ...rest } = a;
    return { ...rest, incoming_webhook: { ...a.incoming_webhook, url: `${base}/hooks/${a.incoming_webhook.id}` } };
  }

  // Seed a few apps per product so the demo is not empty.
  function seed(baseUrl, base, items) {
    const s = store(baseUrl);
    for (const it of items) {
      s.push({
        id: rand(""),
        created_at: new Date().toISOString(),
        token_hash: "x",
        signing_secret: "vas_seed",
        incoming_webhook: { id: rand(""), enabled: true },
        webhook_url: it.webhook_url || "",
        events: [],
        slash_commands: it.slash || [],
        ...it,
      });
    }
  }
  seed("/talk", "/api/apps", [
    { name: "Echo Bot", icon: "\u{1F501}", description: "Echoes mentions back into the channel.", products: ["talk"], scopes: ["chat:write"], slash: [{ name: "echo", description: "echo text" }] },
    { name: "Deployer", icon: "\u{1F680}", description: "Ships builds via /deploy.", products: ["talk"], scopes: ["chat:write", "history:read"], slash: [{ name: "deploy", description: "ship it" }] },
  ]);
  seed("/mail", "/api/apps", [
    { name: "Auto-Filer", icon: "\u{1F4C1}", description: "Files newsletters into folders.", products: ["mail"], scopes: ["apps:write"] },
  ]);
  seed("/meet", "/api/apps", [
    { name: "Notetaker", icon: "\u{1F4DD}", description: "Posts a meeting summary widget.", products: ["meet"], scopes: ["apps:write", "apps:read"] },
  ]);
  seed("/office", "/api/apps", [
    { name: "Lint Tool", icon: "✅", description: "Checks documents for style.", products: ["office"], scopes: ["apps:read"] },
  ]);

  return async function mockFetch(url, opts = {}) {
    const u = new URL(url, "http://demo.local");
    const path = u.pathname;
    const method = (opts.method || "GET").toUpperCase();
    const body = opts.body ? JSON.parse(opts.body) : null;

    // figure out baseUrl (everything before /api/apps)
    const idx = path.indexOf("/api/apps");
    const baseUrl = idx > 0 ? path.slice(0, idx) : "";
    const base = `${baseUrl}/api/apps`;
    const rest = path.slice(idx + "/api/apps".length); // "", "/{id}", "/{id}/rotate/token", ...
    const s = store(baseUrl);

    const json = (status, data) =>
      new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json" } });

    // list / create
    if (rest === "" || rest === "/") {
      if (method === "GET") return json(200, s.map((a) => summary(a, base)));
      if (method === "POST") {
        const app = {
          id: rand(""),
          name: body.name,
          icon: body.icon || "",
          description: body.description || "",
          owner_id: "demo",
          scopes: body.scopes || [],
          products: body.products && body.products.length ? body.products : [baseUrl.replace("/", "") || "talk"],
          events: body.events || [],
          slash_commands: body.slash_commands || [],
          webhook_url: body.webhook_url || "",
          default_target: body.default_target || "",
          incoming_webhook: { id: rand(""), enabled: true },
          created_at: new Date().toISOString(),
          token_hash: "x",
          signing_secret: rand("vas_"),
        };
        s.push(app);
        return json(201, {
          app: summary(app, base),
          token: rand("vat_"),
          signing_secret: app.signing_secret,
          incoming_webhook_url: `${base}/hooks/${app.incoming_webhook.id}`,
        });
      }
    }

    const m = rest.match(/^\/([^/]+)(\/rotate\/(token|secret))?$/);
    if (m) {
      const id = m[1];
      const app = s.find((a) => a.id === id);
      if (!app) return json(404, { error: "app not found" });
      if (m[3] === "token") return json(200, { token: rand("vat_") });
      if (m[3] === "secret") return json(200, { signing_secret: rand("vas_") });
      if (method === "GET") return json(200, summary(app, base));
      if (method === "DELETE") {
        db[baseUrl] = s.filter((a) => a.id !== id);
        return json(200, { ok: true });
      }
      if (method === "PUT") {
        Object.assign(app, body);
        return json(200, summary(app, base));
      }
    }
    return json(404, { error: "not found" });
  };
}
