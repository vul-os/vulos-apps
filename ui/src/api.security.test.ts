import { describe, expect, it } from "vitest";
import { ALL_SCOPES, makeClient, PRODUCTS, type Fetcher } from "./api";

// A fetcher recorder returning canned JSON and recording every request, so we
// can assert URL construction, methods, headers and the tokens-only contract
// across the whole client surface without a backend.
function recorder(status: number, payload: unknown, contentType = "application/json") {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetcher: Fetcher = async (url, init) => {
    calls.push({ url, init });
    return new Response(typeof payload === "string" ? payload : JSON.stringify(payload), {
      status,
      headers: { "Content-Type": contentType },
    });
  };
  return { fetcher, calls };
}

function headersOf(init?: RequestInit): Record<string, string> {
  return (init?.headers as Record<string, string>) ?? {};
}

describe("AppsClient URL + method contract", () => {
  it("routes every method to the correct path and verb under {baseUrl}{basePath}", async () => {
    const { fetcher, calls } = recorder(200, {});
    const client = makeClient({ baseUrl: "https://talk.example", basePath: "/api/apps", token: "s", fetcher });

    await client.list();
    await client.get("a1");
    await client.create({ name: "X" });
    await client.update("a1", { name: "Y" });
    await client.remove("a1");
    await client.rotateToken("a1");
    await client.rotateSecret("a1");
    await client.commands();

    const seen = calls.map((c) => `${c.init?.method} ${c.url}`);
    expect(seen).toEqual([
      "GET https://talk.example/api/apps",
      "GET https://talk.example/api/apps/a1",
      "POST https://talk.example/api/apps",
      "PUT https://talk.example/api/apps/a1",
      "DELETE https://talk.example/api/apps/a1",
      "POST https://talk.example/api/apps/a1/rotate/token",
      "POST https://talk.example/api/apps/a1/rotate/secret",
      "GET https://talk.example/api/apps/commands",
    ]);
  });

  it("honors a custom basePath (per-product mount)", async () => {
    const { fetcher, calls } = recorder(200, []);
    const client = makeClient({ basePath: "/mail/apps", fetcher });
    await client.list();
    expect(calls[0].url).toBe("/mail/apps");
  });

  it("sets Content-Type only on requests with a body", async () => {
    const { fetcher, calls } = recorder(200, {});
    const client = makeClient({ token: "s", fetcher });
    await client.list(); // no body
    await client.create({ name: "X" }); // body
    expect(headersOf(calls[0].init)["Content-Type"]).toBeUndefined();
    expect(headersOf(calls[1].init)["Content-Type"]).toBe("application/json");
  });

  it("always sends Accept: application/json", async () => {
    const { fetcher, calls } = recorder(200, []);
    const client = makeClient({ fetcher });
    await client.list();
    expect(headersOf(calls[0].init).Accept).toBe("application/json");
  });

  it("never attaches Authorization when no token is configured (read-only source)", async () => {
    const { fetcher, calls } = recorder(200, []);
    const client = makeClient({ fetcher });
    await client.create({ name: "X" });
    expect(headersOf(calls[0].init).Authorization).toBeUndefined();
  });
});

describe("AppsClient error handling", () => {
  it("throws an ApiError with the server error message + status", async () => {
    const { fetcher } = recorder(403, { error: "missing required scope: chat:write" });
    const client = makeClient({ token: "s", fetcher });
    await expect(client.create({ name: "X" })).rejects.toMatchObject({
      message: "missing required scope: chat:write",
      status: 403,
    });
  });

  it("falls back to a generic message when the body has no error field", async () => {
    const { fetcher } = recorder(500, {});
    const client = makeClient({ token: "s", fetcher });
    await expect(client.list()).rejects.toMatchObject({ status: 500 });
  });

  it("does not throw on a 2xx with an empty body", async () => {
    const { fetcher } = recorder(200, "");
    const client = makeClient({ token: "s", fetcher });
    await expect(client.remove("a1")).resolves.toBeNull();
  });

  it("tolerates a non-JSON body without throwing a parse error", async () => {
    const { fetcher } = recorder(200, "plain text", "text/plain");
    const client = makeClient({ token: "s", fetcher });
    // The client returns the raw text rather than crashing on JSON.parse.
    await expect(client.list()).resolves.toBe("plain text");
  });
});

describe("static metadata", () => {
  it("exposes the five known products", () => {
    expect(PRODUCTS.map((p) => p.id).sort()).toEqual(["mail", "meet", "office", "os", "talk"]);
  });

  it("lists the built-in scope set (no privileged extras leak in)", () => {
    expect(ALL_SCOPES).toContain("apps:read");
    expect(ALL_SCOPES).toContain("apps:write");
    expect(ALL_SCOPES).not.toContain("admin:all");
  });
});
