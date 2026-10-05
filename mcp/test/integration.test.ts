import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { createService } from "../src/service.ts";

const password = "fictional-owner-password";
const verifier = "a".repeat(64),
  challenge = createHash("sha256").update(verifier).digest("base64url");
const state = mkdtempSync(join(tmpdir(), "frick-oauth-test-"));
let service: ReturnType<typeof createService>,
  server: ReturnType<ReturnType<typeof createService>["app"]["listen"]>;
let origin: string, issuer: string, resource: string;
const cookieJar = new Map<string, string>();
async function request(path: string, init: RequestInit = {}) {
  const headers = new Headers(init.headers);
  headers.set("Connection", "close");
  if (cookieJar.size)
    headers.set(
      "Cookie",
      [...cookieJar].map(([k, v]) => k + "=" + v).join("; "),
    );
  const response = await fetch(new URL(path, origin), {
    ...init,
    headers,
    redirect: "manual",
  });
  for (const cookie of response.headers.getSetCookie()) {
    const pair = cookie.split(";")[0]!;
    const pos = pair.indexOf("=");
    cookieJar.set(pair.slice(0, pos), pair.slice(pos + 1));
  }
  return response;
}
async function start() {
  // Bind first to obtain a port, then instantiate with the real origin.
  const express = (await import("express")).default;
  const outer = express();
  server = outer.listen(0, "127.0.0.1");
  await new Promise<void>((r) => server.once("listening", r));
  const address = server.address();
  assert.ok(address && typeof address === "object");
  origin = "http://127.0.0.1:" + address.port;
  service = createService(
    { MODE: "demo", SERVICE_TOKEN: password },
    origin,
    state,
  );
  issuer = service.issuer;
  resource = service.resource;
  outer.use(service.app);
}
async function registration() {
  const response = await request(issuer + "/reg", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      client_name: "Frick integration test",
      redirect_uris: ["http://127.0.0.1:9292/callback"],
      grant_types: ["authorization_code", "refresh_token"],
      response_types: ["code"],
      token_endpoint_auth_method: "none",
    }),
  });
  const body = await response.json();
  assert.equal(response.status, 201, JSON.stringify(body));
  return body.client_id as string;
}
const basic = {
  Authorization: "Basic " + Buffer.from("frick:" + password).toString("base64"),
};
async function authorize(client: string, scope: string) {
  const params = new URLSearchParams({
    client_id: client,
    response_type: "code",
    redirect_uri: "http://127.0.0.1:9292/callback",
    scope,
    resource,
    code_challenge: challenge,
    code_challenge_method: "S256",
    state: "test-state",
    prompt: "consent",
  });
  let response = await request(issuer + "/auth?" + params);
  assert.equal(response.status, 303, await response.clone().text());
  let location = response.headers.get("location")!;
  for (let attempt = 0; attempt < 5; attempt++) {
    if (location.startsWith("http://127.0.0.1:9292/")) {
      const url = new URL(location);
      assert.equal(url.searchParams.get("state"), "test-state");
      assert.ok(url.searchParams.get("code"), location);
      return url.searchParams.get("code")!;
    }
    response = await request(location, { headers: basic });
    if (response.status === 200) {
      const html = await response.text();
      const csrf = html.match(/name="csrf" value="([^"]+)"/)?.[1];
      assert.ok(csrf, html);
      response = await request(location, {
        method: "POST",
        headers: {
          ...basic,
          Origin: origin,
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: new URLSearchParams({ csrf, decision: "allow" }),
      });
    }
    assert.ok(
      [302, 303].includes(response.status),
      await response.clone().text(),
    );
    location = response.headers.get("location")!;
  }
  throw new Error("Too many redirects");
}
async function token(client: string, grant: Record<string, string>) {
  return request(issuer + "/token", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ client_id: client, resource, ...grant }),
  });
}
async function rpc(
  access: string,
  method: string,
  params?: object,
  modern = true,
) {
  return request(resource, {
    method: "POST",
    headers: {
      Authorization: "Bearer " + access,
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      "MCP-Protocol-Version": modern ? "2026-07-28" : "2025-11-25",
      ...(modern
        ? {
            "Mcp-Method": method,
            ...(method === "tools/call"
              ? { "Mcp-Name": String((params as { name: string }).name) }
              : {}),
          }
        : {}),
    },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method,
      params: {
        ...params,
        ...(modern
          ? {
              _meta: {
                "io.modelcontextprotocol/protocolVersion": "2026-07-28",
                "io.modelcontextprotocol/clientCapabilities": {},
                "io.modelcontextprotocol/clientInfo": {
                  name: "test",
                  version: "1",
                },
              },
            }
          : {}),
      },
    }),
  });
}
async function rpcJson(response: Response) {
  const text = await response.text();
  if (response.headers.get("content-type")?.includes("event-stream"))
    return JSON.parse(
      text
        .split("\n")
        .find((l) => l.startsWith("data: "))!
        .slice(6),
    );
  return JSON.parse(text);
}
test("OAuth PKCE, persistent state, scopes, stateless modern/legacy MCP, refresh and revocation", async () => {
  try {
    await start();
    let response = await request(
      "/.well-known/oauth-protected-resource/frick/mcp",
    );
    assert.equal((await response.json()).resource, resource);
    response = await request(
      "/.well-known/oauth-authorization-server/frick/oauth",
    );
    const metadata = await response.json();
    assert.equal(metadata.issuer, issuer);
    assert.ok(metadata.code_challenge_methods_supported.includes("S256"));
    response = await request(resource, { method: "POST" });
    assert.equal(response.status, 401);
    assert.match(
      response.headers.get("www-authenticate")!,
      /resource_metadata/,
    );
    const client = await registration();
    // No PKCE must be rejected before owner consent.
    response = await request(
      issuer +
        "/auth?" +
        new URLSearchParams({
          client_id: client,
          response_type: "code",
          redirect_uri: "http://127.0.0.1:9292/callback",
          resource,
          scope: "frick:read",
        }),
    );
    assert.ok([302, 303, 400].includes(response.status));
    assert.doesNotMatch(response.headers.get("location") ?? "", /interaction/);
    const code = await authorize(client, "frick:read offline_access");
    response = await token(client, {
      grant_type: "authorization_code",
      code,
      redirect_uri: "http://127.0.0.1:9292/callback",
      code_verifier: "b".repeat(64),
    });
    assert.equal(response.status, 400);
    // An invalid verifier consumes the code, as it should. Start a fresh flow.
    const code2 = await authorize(client, "frick:read offline_access");
    response = await token(client, {
      grant_type: "authorization_code",
      code: code2,
      redirect_uri: "http://127.0.0.1:9292/callback",
      code_verifier: verifier,
    });
    let tokens = await response.json();
    assert.equal(response.status, 200, JSON.stringify(tokens));
    assert.ok(tokens.refresh_token);
    assert.equal(tokens.expires_in, 900);
    response = await token(client, {
      grant_type: "authorization_code",
      code: code2,
      redirect_uri: "http://127.0.0.1:9292/callback",
      code_verifier: verifier,
    });
    assert.equal(response.status, 400);
    // Code replay may revoke the grant: acquire a fresh one for MCP checks.
    const code3 = await authorize(client, "frick:read offline_access");
    response = await token(client, {
      grant_type: "authorization_code",
      code: code3,
      redirect_uri: "http://127.0.0.1:9292/callback",
      code_verifier: verifier,
    });
    tokens = await response.json();
    assert.equal(response.status, 200);
    response = await rpc(tokens.access_token, "tools/list");
    assert.equal(response.status, 200, await response.clone().text());
    assert.equal(response.headers.get("mcp-session-id"), null);
    const tools = await rpcJson(response);
    assert.deepEqual(
      tools.result.tools.map((t: { name: string }) => t.name),
      ["read_page", "submit_form", "get_profile"],
    );
    response = await rpc(tokens.access_token, "tools/call", {
      name: "read_page",
      arguments: { path: "/" },
    });
    const page = await rpcJson(response);
    assert.match(page.result.content[0].text, /Demo/);
    response = await rpc(tokens.access_token, "tools/call", {
      name: "submit_form",
      arguments: { path: "/payments/review", fields: {} },
    });
    assert.equal(response.status, 403, await response.clone().text());
    response = await rpc(tokens.access_token, "tools/call", {
      name: "read_page",
      arguments: { path: "https://example.com" },
    });
    const blocked = await rpcJson(response);
    assert.ok(blocked.error || blocked.result.isError);
    response = await rpc(
      tokens.access_token,
      "initialize",
      {
        protocolVersion: "2025-11-25",
        capabilities: {},
        clientInfo: { name: "test", version: "1" },
      },
      false,
    );
    assert.equal(response.status, 200, await response.clone().text());
    assert.equal(response.headers.get("mcp-session-id"), null);
    response = await rpc(tokens.access_token, "tools/list", {}, false);
    assert.equal(response.status, 200);
    response = await rpc("invalid-token", "tools/list");
    assert.equal(response.status, 401);
    const preparedClient = await registration();
    const preparedCode = await authorize(
      preparedClient,
      "frick:read frick:prepare offline_access",
    );
    response = await token(preparedClient, {
      grant_type: "authorization_code",
      code: preparedCode,
      redirect_uri: "http://127.0.0.1:9292/callback",
      code_verifier: verifier,
    });
    const preparedTokens = await response.json();
    assert.equal(response.status, 200);
    response = await rpc(preparedTokens.access_token, "tools/call", {
      name: "read_page",
      arguments: { path: "/" },
    });
    const desk = (await rpcJson(response)).result.content[0].text as string;
    const from = desk.match(/<option value="([^"]+)"/)?.[1];
    const customId = desk.match(/name="customId" value="([^"]+)"/)?.[1];
    assert.ok(from);
    assert.ok(customId);
    response = await rpc(preparedTokens.access_token, "tools/call", {
      name: "submit_form",
      arguments: {
        path: "/payments/review",
        fields: {
          from,
          customId,
          to: "DE89370400440532013000",
          name: "Fictional recipient",
          amount: "12.34",
          reference: "Demo only",
        },
      },
    });
    const review = (await rpcJson(response)).result.content[0].text as string;
    const reviewToken = review.match(/name="review" value="([^"]+)"/)?.[1];
    assert.ok(reviewToken, review);
    response = await rpc(preparedTokens.access_token, "tools/call", {
      name: "submit_form",
      arguments: { path: "/payments/create", fields: { review: reviewToken } },
    });
    const created = await rpcJson(response);
    assert.equal(created.result.isError, false, JSON.stringify(created));
    assert.match(created.result.content[0].text, /unsigned/i);
    response = await rpc(preparedTokens.access_token, "tools/call", {
      name: "submit_form",
      arguments: { path: "/payments/create", fields: { review: "tampered" } },
    });
    assert.equal((await rpcJson(response)).result.isError, true);
    response = await rpc(preparedTokens.access_token, "tools/call", {
      name: "submit_form",
      arguments: { path: "/sign", fields: {} },
    });
    const noSign = await rpcJson(response);
    assert.ok(noSign.error || noSign.result.isError);
    const access = tokens.access_token;
    // Reload the provider against the same SQLite data and persisted signing keys.
    await new Promise<void>((r) => server.close(() => r()));
    await service.close();
    cookieJar.clear();
    const express = (await import("express")).default,
      outer = express();
    service = createService(
      { MODE: "demo", SERVICE_TOKEN: password },
      origin,
      state,
    );
    outer.use(service.app);
    server = outer.listen(Number(new URL(origin).port), "127.0.0.1");
    await new Promise<void>((r) => server.once("listening", r));
    response = await rpc(access, "tools/call", {
      name: "get_profile",
      arguments: {},
    });
    assert.equal(response.status, 200);
    assert.ok((await rpcJson(response)).result.structuredContent.id);
    response = await token(client, {
      grant_type: "refresh_token",
      refresh_token: tokens.refresh_token,
    });
    tokens = await response.json();
    assert.equal(response.status, 200, JSON.stringify(tokens));
    assert.ok(tokens.refresh_token);
    response = await request(issuer + "/token", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        client_id: client,
        grant_type: "refresh_token",
        refresh_token: tokens.refresh_token,
        resource: "https://wrong.example/mcp",
      }),
    });
    assert.equal(response.status, 400);
    response = await request(issuer + "/connections", { headers: basic });
    let html = await response.text();
    assert.match(html, /Frick integration test/);
    const csrf = html.match(/name="csrf" value="([^"]+)"/)?.[1];
    assert.ok(csrf);
    response = await request(issuer + "/connections", {
      method: "POST",
      headers: {
        ...basic,
        Origin: "https://attacker.example",
        "Content-Type": "application/x-www-form-urlencoded",
      },
      body: new URLSearchParams({ csrf }),
    });
    assert.equal(response.status, 403);
    response = await request(issuer + "/connections", {
      method: "POST",
      headers: {
        ...basic,
        Origin: origin,
        "Content-Type": "application/x-www-form-urlencoded",
      },
      body: new URLSearchParams({ csrf }),
    });
    assert.equal(response.status, 303);
    response = await rpc(tokens.access_token, "tools/list");
    assert.equal(response.status, 401);
    response = await token(client, {
      grant_type: "refresh_token",
      refresh_token: tokens.refresh_token,
    });
    assert.equal(response.status, 400);
  } finally {
    if (server) await new Promise<void>((r) => server.close(() => r()));
    if (service) await service.close();
    rmSync(state, { recursive: true, force: true });
  }
});
