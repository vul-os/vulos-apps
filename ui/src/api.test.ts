import { describe, expect, it } from "vitest";
import { makeClient, type Fetcher } from "./api";
import type { AppSummary, CreateAppResponse } from "./types";

// A tiny fetcher recorder that returns canned JSON and records the requests it
// saw, so we can assert the tokens-only contract without a backend.
function recorder(status: number, payload: unknown) {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetcher: Fetcher = async (url, init) => {
    calls.push({ url, init });
    return new Response(JSON.stringify(payload), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  };
  return { fetcher, calls };
}

describe("makeClient", () => {
  it("lists apps from {baseUrl}{basePath}", async () => {
    const apps: AppSummary[] = [
      {
        id: "a1",
        name: "Echo",
        icon: "",
        description: "",
        scopes: ["chat:write"],
        products: ["talk"],
        events: [],
        slash_commands: [],
        webhook_url: "",
        incoming_webhook: { id: "h1", enabled: true },
        owner_id: "u1",
        created_at: "2026-01-01T00:00:00Z",
      },
    ];
    const { fetcher, calls } = recorder(200, apps);
    const client = makeClient({ baseUrl: "/talk", token: "sess", fetcher });
    const got = await client.list();
    expect(got).toEqual(apps);
    expect(calls[0].url).toBe("/talk/api/apps");
  });

  it("sends Authorization: Bearer (tokens-only, no cookies)", async () => {
    const { fetcher, calls } = recorder(200, []);
    const client = makeClient({ token: "secret-session", fetcher });
    await client.list();
    const headers = calls[0].init?.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer secret-session");
  });

  it("omits Authorization when no token (read-only source)", async () => {
    const { fetcher, calls } = recorder(200, []);
    const client = makeClient({ fetcher });
    await client.list();
    const headers = calls[0].init?.headers as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
  });

  it("returns the one-time secrets shape from create", async () => {
    const resp: CreateAppResponse = {
      app: {
        id: "a2",
        name: "New",
        icon: "",
        description: "",
        scopes: [],
        products: ["mail"],
        events: [],
        slash_commands: [],
        webhook_url: "",
        incoming_webhook: { id: "h2", enabled: true },
        owner_id: "u1",
        created_at: "2026-01-01T00:00:00Z",
      },
      token: "vat_xxx",
      signing_secret: "vas_yyy",
      incoming_webhook_url: "/api/apps/hooks/h2",
    };
    const { fetcher, calls } = recorder(201, resp);
    const client = makeClient({ token: "sess", fetcher });
    const got = await client.create({ name: "New", products: ["mail"] });
    expect(got.token).toBe("vat_xxx");
    expect(got.signing_secret).toBe("vas_yyy");
    expect(calls[0].init?.method).toBe("POST");
  });

  it("throws an ApiError carrying the HTTP status on failure", async () => {
    const { fetcher } = recorder(403, { error: "missing required scope" });
    const client = makeClient({ token: "sess", fetcher });
    await expect(client.list()).rejects.toMatchObject({
      message: "missing required scope",
      status: 403,
    });
  });
});
