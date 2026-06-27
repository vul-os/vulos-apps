import { useCallback, useEffect, useMemo, useState } from "react";
import { makeClient, PRODUCTS } from "./api.js";
import AppCard from "./components/AppCard.jsx";
import InstallForm from "./components/InstallForm.jsx";
import "./styles.css";

// <AppsAndBots/> — the shared Vulos Apps & Bots surface.
//
// Two modes:
//   mode="product"   (default) — ONE product's apps & bots place. Reads/writes
//                    GET/POST {baseUrl}{basePath}. Shows install + manage.
//   mode="aggregate" — Vulos Workspace: apps & bots across ALL products. Reads
//                    each product's GET /api/apps and groups by product. Each
//                    source may carry its own token (write) or be read-only.
//
// Tokens-only: management calls send Authorization: Bearer <token>; no cookies.
//
// Props (product mode):
//   product, token, baseUrl="", basePath="/api/apps", fetcher?
// Props (aggregate mode):
//   sources: [{ product, label?, baseUrl, basePath?, token? }], fetcher?
// Common: theme="dark"|"light", title?, subtitle?
export default function AppsAndBots(props) {
  const {
    mode = "product",
    theme = "dark",
    title,
    subtitle,
    fetcher,
  } = props;

  // Normalize the configured sources into [{ key, product, label, client }].
  const sources = useMemo(() => {
    if (mode === "aggregate") {
      const list = props.sources || [];
      return list.map((s) => ({
        key: `${s.product}:${s.baseUrl || ""}`,
        product: s.product,
        label: s.label || labelFor(s.product),
        client: makeClient({
          baseUrl: s.baseUrl || "",
          basePath: s.basePath || "/api/apps",
          token: s.token,
          fetcher,
        }),
      }));
    }
    const product = props.product || "talk";
    return [
      {
        key: product,
        product,
        label: title || labelFor(product),
        client: makeClient({
          baseUrl: props.baseUrl || "",
          basePath: props.basePath || "/api/apps",
          token: props.token,
          fetcher,
        }),
      },
    ];
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, props.product, props.token, props.baseUrl, props.basePath, props.sources, fetcher]);

  const [state, setState] = useState({ loading: true, error: "", groups: [] });
  const [installing, setInstalling] = useState(null); // a source object or null

  const load = useCallback(async () => {
    setState((s) => ({ ...s, loading: true, error: "" }));
    const results = await Promise.all(
      sources.map(async (src) => {
        try {
          const apps = await src.client.list();
          return { src, apps: Array.isArray(apps) ? apps : [], error: "" };
        } catch (e) {
          return { src, apps: [], error: e.message || String(e) };
        }
      })
    );
    const anyOk = results.some((r) => !r.error);
    const firstErr = results.find((r) => r.error);
    setState({
      loading: false,
      error: anyOk ? "" : firstErr ? firstErr.error : "",
      groups: results,
    });
  }, [sources]);

  useEffect(() => {
    load();
  }, [load]);

  const total = state.groups.reduce((n, g) => n + g.apps.length, 0);

  return (
    <section className="vulos-apps" data-theme={theme} aria-label="Apps and bots">
      <header className="va-head">
        <div>
          <h2 className="va-title">{title || (mode === "aggregate" ? "Apps & Bots" : `${labelFor(props.product)} — Apps & Bots`)}</h2>
          <p className="va-sub">
            {subtitle ||
              (mode === "aggregate"
                ? "Every app & bot across your Vulos products, in one place."
                : "Install, configure and manage apps & bots for this product.")}
          </p>
        </div>
        {mode === "product" && sources[0] ? (
          <button type="button" className="va-btn va-btn--primary" onClick={() => setInstalling(sources[0])}>
            + Install app
          </button>
        ) : null}
      </header>

      {state.loading ? (
        <p className="va-state" role="status">Loading apps…</p>
      ) : state.error && total === 0 ? (
        <p className="va-state va-state--error" role="alert">Could not load apps: {state.error}</p>
      ) : total === 0 ? (
        <p className="va-state">No apps or bots installed yet.</p>
      ) : null}

      {!state.loading &&
        state.groups.map((g) =>
          // In product mode render a single ungrouped grid; in aggregate mode
          // render one group per product source.
          mode === "aggregate" ? (
            <div className="va-group" key={g.src.key}>
              <div className="va-group__head">
                <span aria-hidden="true">{glyphFor(g.src.product)}</span>
                <h3 className="va-group__title">{g.src.label}</h3>
                <span className="va-group__count">{g.apps.length}</span>
                {g.src.client && g.src.client.create && hasToken(props, g.src) ? (
                  <button
                    type="button"
                    className="va-btn"
                    style={{ marginLeft: "auto" }}
                    onClick={() => setInstalling(g.src)}
                  >
                    + Install
                  </button>
                ) : null}
              </div>
              {g.error ? (
                <p className="va-state va-state--error" role="alert">{g.error}</p>
              ) : g.apps.length === 0 ? (
                <p className="va-state">No apps for {g.src.label}.</p>
              ) : (
                <ul className="va-grid" style={{ listStyle: "none", margin: 0, padding: 0 }}>
                  {g.apps.map((app) => (
                    <AppCard key={app.id} app={app} source={g.src} onChanged={load} />
                  ))}
                </ul>
              )}
            </div>
          ) : (
            <ul className="va-grid" key={g.src.key} style={{ listStyle: "none", margin: 0, padding: 0 }}>
              {g.apps.map((app) => (
                <AppCard key={app.id} app={app} source={g.src} onChanged={load} />
              ))}
            </ul>
          )
        )}

      {installing ? (
        <InstallForm
          source={installing}
          defaultProduct={installing.product}
          onClose={() => setInstalling(null)}
          onInstalled={load}
        />
      ) : null}
    </section>
  );
}

function labelFor(product) {
  const p = PRODUCTS.find((x) => x.id === product);
  return p ? p.label : product || "Apps";
}
function glyphFor(product) {
  const p = PRODUCTS.find((x) => x.id === product);
  return p ? p.glyph : "\u{1F916}";
}
// In aggregate mode a source can install only when the host supplied a token for
// it (read-only sources omit the token).
function hasToken(props, src) {
  const list = props.sources || [];
  const match = list.find((s) => `${s.product}:${s.baseUrl || ""}` === src.key);
  return !!(match && match.token);
}
