import { useCallback, useEffect, useMemo, useState } from "react";
import { makeClient, PRODUCTS } from "./api";
import type { AppsClient, Fetcher } from "./api";
import type { AppSummary, ProductId } from "./types";
import AppCard from "./components/AppCard";
import InstallForm from "./components/InstallForm";
import "./styles.css";

export type Theme = "dark" | "light";

// AggregateSource is one product surface the Workspace aggregate reads. A source
// without a `token` is read-only (no management actions surfaced for it).
export interface AggregateSource {
  product: ProductId | string;
  label?: string;
  baseUrl?: string;
  basePath?: string;
  token?: string;
}

// NormalizedSource is the internal, resolved form of a source: a stable key, a
// label, and a ready-to-use client. Passed to AppCard / InstallForm.
export interface NormalizedSource {
  key: string;
  product: ProductId | string;
  label: string;
  client: AppsClient;
}

interface CommonProps {
  theme?: Theme;
  title?: string;
  subtitle?: string;
  fetcher?: Fetcher;
}

// Product mode: ONE product's apps & bots place.
export interface ProductModeProps extends CommonProps {
  mode?: "product";
  product?: ProductId | string;
  token?: string;
  baseUrl?: string;
  basePath?: string;
}

// Aggregate mode: Vulos Workspace — apps & bots across ALL products.
export interface AggregateModeProps extends CommonProps {
  mode: "aggregate";
  sources: AggregateSource[];
}

export type AppsAndBotsProps = ProductModeProps | AggregateModeProps;

interface Group {
  src: NormalizedSource;
  apps: AppSummary[];
  error: string;
}

interface LoadState {
  loading: boolean;
  error: string;
  groups: Group[];
}

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
export default function AppsAndBots(props: AppsAndBotsProps) {
  const { mode = "product", theme = "dark", title, subtitle, fetcher } = props;

  // Normalize the configured sources into [{ key, product, label, client }].
  const sources = useMemo<NormalizedSource[]>(() => {
    if (props.mode === "aggregate") {
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
  }, [mode, (props as ProductModeProps).product, (props as ProductModeProps).token, (props as ProductModeProps).baseUrl, (props as ProductModeProps).basePath, (props as AggregateModeProps).sources, fetcher, title]);

  const [state, setState] = useState<LoadState>({
    loading: true,
    error: "",
    groups: [],
  });
  const [installing, setInstalling] = useState<NormalizedSource | null>(null);

  const load = useCallback(async () => {
    setState((s) => ({ ...s, loading: true, error: "" }));
    const results: Group[] = await Promise.all(
      sources.map(async (src) => {
        try {
          const apps = await src.client.list();
          return { src, apps: Array.isArray(apps) ? apps : [], error: "" };
        } catch (e) {
          return { src, apps: [], error: errMsg(e) };
        }
      }),
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
  const productLabel =
    props.mode === "aggregate" ? "" : labelFor(props.product);

  return (
    <section className="vulos-apps" data-theme={theme} aria-label="Apps and bots">
      <header className="va-head">
        <div>
          <h2 className="va-title">
            {title ||
              (mode === "aggregate" ? "Apps & Bots" : `${productLabel} — Apps & Bots`)}
          </h2>
          <p className="va-sub">
            {subtitle ||
              (mode === "aggregate"
                ? "Every app & bot across your Vulos products, in one place."
                : "Install, configure and manage apps & bots for this product.")}
          </p>
        </div>
        {mode === "product" && sources[0] ? (
          <button
            type="button"
            className="va-btn va-btn--primary"
            onClick={() => setInstalling(sources[0])}
          >
            + Install app
          </button>
        ) : null}
      </header>

      {state.loading ? (
        <p className="va-state" role="status">
          Loading apps…
        </p>
      ) : state.error && total === 0 ? (
        <p className="va-state va-state--error" role="alert">
          Could not load apps: {state.error}
        </p>
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
                {hasToken(props, g.src) ? (
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
                <p className="va-state va-state--error" role="alert">
                  {g.error}
                </p>
              ) : g.apps.length === 0 ? (
                <p className="va-state">No apps for {g.src.label}.</p>
              ) : (
                <ul
                  className="va-grid"
                  style={{ listStyle: "none", margin: 0, padding: 0 }}
                >
                  {g.apps.map((app) => (
                    <AppCard
                      key={app.id}
                      app={app}
                      source={g.src}
                      onChanged={load}
                    />
                  ))}
                </ul>
              )}
            </div>
          ) : (
            <ul
              className="va-grid"
              key={g.src.key}
              style={{ listStyle: "none", margin: 0, padding: 0 }}
            >
              {g.apps.map((app) => (
                <AppCard key={app.id} app={app} source={g.src} onChanged={load} />
              ))}
            </ul>
          ),
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

function labelFor(product: ProductId | string | undefined): string {
  const p = PRODUCTS.find((x) => x.id === product);
  return p ? p.label : product || "Apps";
}
function glyphFor(product: ProductId | string): string {
  const p = PRODUCTS.find((x) => x.id === product);
  return p ? p.glyph : "\u{1F916}";
}
function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
// In aggregate mode a source can install only when the host supplied a token for
// it (read-only sources omit the token).
function hasToken(props: AppsAndBotsProps, src: NormalizedSource): boolean {
  if (props.mode !== "aggregate") return true;
  const list = props.sources || [];
  const match = list.find((s) => `${s.product}:${s.baseUrl || ""}` === src.key);
  return !!(match && match.token);
}
