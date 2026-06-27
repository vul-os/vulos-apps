import { useState } from "react";
import { createRoot } from "react-dom/client";
import AppsAndBots from "../AppsAndBots";
import { makeMockFetcher } from "./mockFetcher";

const fetcher = makeMockFetcher();

function Demo() {
  const [mode, setMode] = useState<"aggregate" | "product">("aggregate");

  return (
    <div
      style={{
        minHeight: "100vh",
        background: "#0c0c0c",
        fontFamily: "ui-monospace, monospace",
      }}
    >
      <div
        style={{
          display: "flex",
          gap: 8,
          padding: 16,
          borderBottom: "1px solid #1a1a1a",
        }}
      >
        <button
          onClick={() => setMode("aggregate")}
          className="va-btn"
          style={btn(mode === "aggregate")}
        >
          Workspace (aggregate)
        </button>
        <button
          onClick={() => setMode("product")}
          className="va-btn"
          style={btn(mode === "product")}
        >
          Talk (per-product)
        </button>
      </div>

      {mode === "aggregate" ? (
        <AppsAndBots
          mode="aggregate"
          fetcher={fetcher}
          sources={[
            { product: "talk", baseUrl: "/talk", token: "demo-session-talk" },
            { product: "mail", baseUrl: "/mail", token: "demo-session-mail" },
            { product: "meet", baseUrl: "/meet", token: "demo-session-meet" },
            { product: "office", baseUrl: "/office", token: "demo-session-office" },
          ]}
        />
      ) : (
        <AppsAndBots
          mode="product"
          product="talk"
          baseUrl="/talk"
          token="demo-session"
          fetcher={fetcher}
        />
      )}
    </div>
  );
}

function btn(active: boolean): React.CSSProperties {
  return active
    ? { background: "#3b82f6", borderColor: "#3b82f6", color: "#fff" }
    : { background: "#1a1a1a", color: "#e5e5e5" };
}

const rootEl = document.getElementById("root");
if (rootEl) createRoot(rootEl).render(<Demo />);
