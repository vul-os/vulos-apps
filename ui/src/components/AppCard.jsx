import { useState } from "react";

// AppCard renders one installed app/bot with its manifest summary and the manage
// actions (rotate token, rotate signing secret, uninstall). Mutations call back
// the source client the app was loaded from.
export default function AppCard({ app, source, onChanged }) {
  const [busy, setBusy] = useState("");
  const [reveal, setReveal] = useState(null); // {label, value}
  const [err, setErr] = useState("");

  const can = !!source?.client;

  async function run(kind, fn) {
    if (!can) return;
    setBusy(kind);
    setErr("");
    try {
      await fn();
    } catch (e) {
      setErr(e.message || String(e));
    } finally {
      setBusy("");
    }
  }

  const icon = app.icon && app.icon.length <= 4 ? app.icon : "\u{1F916}"; // emoji or robot fallback

  return (
    <li className="va-card">
      <div className="va-card__top">
        <div className="va-card__icon" aria-hidden="true">{icon}</div>
        <div style={{ minWidth: 0 }}>
          <h4 className="va-card__name">{app.name || app.id}</h4>
          {app.description ? <p className="va-card__desc">{app.description}</p> : null}
        </div>
      </div>

      <div className="va-tags" aria-label="targets and scopes">
        {(app.products || []).map((p) => (
          <span key={p} className="va-tag va-tag--product">{p}</span>
        ))}
        {(app.scopes || []).map((s) => (
          <span key={s} className="va-tag">{s}</span>
        ))}
        {app.incoming_webhook?.enabled ? <span className="va-tag">webhook in</span> : null}
        {app.webhook_url ? <span className="va-tag">events out</span> : null}
      </div>

      {reveal ? (
        <p className="va-secret" role="status">
          New {reveal.label} (shown once): <b>{reveal.value}</b>
        </p>
      ) : null}
      {err ? <p className="va-secret va-state--error" role="alert">{err}</p> : null}

      {can ? (
        <div className="va-card__row">
          <button
            type="button"
            className="va-btn"
            disabled={!!busy}
            onClick={() =>
              run("token", async () => {
                const r = await source.client.rotateToken(app.id);
                setReveal({ label: "token", value: r.token });
              })
            }
          >
            {busy === "token" ? "…" : "Rotate token"}
          </button>
          <button
            type="button"
            className="va-btn"
            disabled={!!busy}
            onClick={() =>
              run("secret", async () => {
                const r = await source.client.rotateSecret(app.id);
                setReveal({ label: "signing secret", value: r.signing_secret });
              })
            }
          >
            {busy === "secret" ? "…" : "Rotate secret"}
          </button>
          <button
            type="button"
            className="va-btn va-btn--danger"
            disabled={!!busy}
            onClick={() =>
              run("delete", async () => {
                await source.client.remove(app.id);
                onChanged && onChanged();
              })
            }
          >
            {busy === "delete" ? "…" : "Uninstall"}
          </button>
        </div>
      ) : (
        <p className="va-note">Read-only (no management token for this product).</p>
      )}
    </li>
  );
}
