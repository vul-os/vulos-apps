import { useEffect, useRef, useState } from "react";
import { ALL_SCOPES, PRODUCTS } from "../api";
import type { NormalizedSource } from "../AppsAndBots";
import type { CreateAppResponse, ProductId } from "../types";

export interface InstallFormProps {
  source: NormalizedSource;
  defaultProduct?: ProductId | string;
  onClose: () => void;
  onInstalled?: () => void;
}

// InstallForm is the modal dialog that installs (creates) an app into a product
// place. On success it surfaces the one-time token + signing secret. a11y: focus
// trap entry, Escape to close, labelled dialog.
export default function InstallForm({
  source,
  defaultProduct,
  onClose,
  onInstalled,
}: InstallFormProps) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [icon, setIcon] = useState("\u{1F916}");
  const [scopes, setScopes] = useState<string[]>(["apps:write"]);
  const [products, setProducts] = useState<string[]>(
    defaultProduct ? [defaultProduct] : [],
  );
  const [webhookUrl, setWebhookUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [created, setCreated] = useState<CreateAppResponse | null>(null);
  const firstRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    firstRef.current && firstRef.current.focus();
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  function toggle(
    list: string[],
    setList: (v: string[]) => void,
    v: string,
  ) {
    setList(list.includes(v) ? list.filter((x) => x !== v) : [...list, v]);
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const res = await source.client.create({
        name,
        description,
        icon,
        scopes,
        products: products.length ? products : undefined,
        webhook_url: webhookUrl || undefined,
      });
      setCreated(res);
      onInstalled && onInstalled();
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : String(e2));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="va-overlay"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        className="va-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="va-install-title"
      >
        <h3 className="va-modal__title" id="va-install-title">
          {created ? "App installed" : "Install an app or bot"}
        </h3>

        {created ? (
          <div>
            <p className="va-note">
              Copy these now — secrets are shown <b>once</b> and cannot be recovered
              (rotate to re-issue).
            </p>
            <p className="va-secret">
              Token: <b>{created.token}</b>
            </p>
            <p className="va-secret">
              Signing secret: <b>{created.signing_secret}</b>
            </p>
            {created.incoming_webhook_url ? (
              <p className="va-secret">
                Incoming webhook: <b>{created.incoming_webhook_url}</b>
              </p>
            ) : null}
            <div className="va-modal__actions">
              <button
                type="button"
                className="va-btn va-btn--primary"
                onClick={onClose}
              >
                Done
              </button>
            </div>
          </div>
        ) : (
          <form onSubmit={submit}>
            <div className="va-field">
              <label htmlFor="va-name">Name</label>
              <input
                id="va-name"
                ref={firstRef}
                className="va-input"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
              />
            </div>
            <div className="va-field">
              <label htmlFor="va-desc">Description</label>
              <textarea
                id="va-desc"
                className="va-textarea"
                rows={2}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </div>
            <div className="va-field">
              <label htmlFor="va-icon">Icon (emoji)</label>
              <input
                id="va-icon"
                className="va-input"
                value={icon}
                onChange={(e) => setIcon(e.target.value)}
              />
            </div>

            <div className="va-field">
              <label>Targets which products</label>
              <div className="va-checks">
                {PRODUCTS.map((p) => (
                  <label key={p.id} className="va-check">
                    <input
                      type="checkbox"
                      checked={products.includes(p.id)}
                      onChange={() => toggle(products, setProducts, p.id)}
                    />
                    {p.label}
                  </label>
                ))}
              </div>
            </div>

            <div className="va-field">
              <label>Scopes</label>
              <div className="va-checks">
                {ALL_SCOPES.map((s) => (
                  <label key={s} className="va-check">
                    <input
                      type="checkbox"
                      checked={scopes.includes(s)}
                      onChange={() => toggle(scopes, setScopes, s)}
                    />
                    {s}
                  </label>
                ))}
              </div>
            </div>

            <div className="va-field">
              <label htmlFor="va-webhook">
                Outbound events webhook URL (optional)
              </label>
              <input
                id="va-webhook"
                className="va-input"
                placeholder="https://my-app.example/events"
                value={webhookUrl}
                onChange={(e) => setWebhookUrl(e.target.value)}
              />
            </div>

            {err ? (
              <p className="va-secret va-state--error" role="alert">
                {err}
              </p>
            ) : null}

            <div className="va-modal__actions">
              <button type="button" className="va-btn" onClick={onClose}>
                Cancel
              </button>
              <button
                type="submit"
                className="va-btn va-btn--primary"
                disabled={busy || !name}
              >
                {busy ? "Installing…" : "Install"}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
