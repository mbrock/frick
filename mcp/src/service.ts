import express from "express";
import Provider, { errors, type Configuration } from "oidc-provider";
import { generateKeyPairSync, randomBytes, randomUUID } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync, chmodSync } from "node:fs";
import { join } from "node:path";
import { sqliteAdapter } from "./store.ts";
import { bankingHandler } from "./tools.ts";
import {
  escape as e,
  sameSecret,
  seal,
  unseal,
} from "../../web/src/security.ts";
import type { ServiceEnv } from "../../web/src/env.ts";

const scopes = ["frick:read", "frick:prepare"];
export function createService(
  env: ServiceEnv,
  origin: string,
  stateDir: string,
) {
  origin = new URL(origin).origin;
  const issuer = origin + "/frick/oauth",
    resource = origin + "/frick/mcp";
  const metadata = origin + "/.well-known/oauth-protected-resource/frick/mcp";
  mkdirSync(stateDir, { recursive: true, mode: 0o700 });
  chmodSync(stateDir, 0o700);
  const keysPath = join(stateDir, "keys.json");
  let keys: {
    jwks: { keys: (JsonWebKey & { kid: string; use: string; alg: string })[] };
    cookies: string[];
    owner: string;
  };
  try {
    keys = JSON.parse(readFileSync(keysPath, "utf8"));
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    const { privateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
    keys = {
      jwks: {
        keys: [
          {
            ...privateKey.export({ format: "jwk" }),
            kid: randomUUID(),
            use: "sig",
            alg: "RS256",
          },
        ],
      },
      cookies: [randomBytes(32).toString("base64url")],
      owner: randomUUID(),
    };
    writeFileSync(keysPath, JSON.stringify(keys), { mode: 0o600, flag: "wx" });
  }
  const store = sqliteAdapter(join(stateDir, "oauth.sqlite"));
  const config: Configuration = {
    adapter: store.Adapter,
    jwks: keys.jwks,
    cookies: { keys: keys.cookies },
    scopes: ["openid", "offline_access"],
    responseTypes: ["code"],
    pkce: { required: () => true },
    features: {
      devInteractions: { enabled: false },
      registration: { enabled: true },
      revocation: { enabled: true },
      clientIdMetadataDocument: {
        enabled: true,
        ack: "draft-02",
        allowFetch: (_ctx, id) => {
          // CIMD causes a server-side fetch. Limit it to the hosted clients we use;
          // other MCP clients can use dynamic registration without such a fetch.
          const url = new URL(id);
          return (
            url.protocol === "https:" &&
            !url.port &&
            ["chatgpt.com", "codex.openai.com"].includes(url.hostname)
          );
        },
      },
      resourceIndicators: {
        enabled: true,
        defaultResource: () => resource,
        useGrantedResource: () => true,
        getResourceServerInfo: (_ctx, target) => {
          if (target !== resource) throw new errors.InvalidTarget();
          return {
            scope: scopes.join(" "),
            audience: resource,
            accessTokenTTL: 900,
            accessTokenFormat: "opaque",
          };
        },
      },
    },
    clientDefaults: {
      grant_types: ["authorization_code", "refresh_token"],
      response_types: ["code"],
      token_endpoint_auth_method: "none",
    },
    ttl: {
      AccessToken: 900,
      AuthorizationCode: 60,
      Interaction: 600,
      RefreshToken: 90 * 86400,
      Session: 90 * 86400,
      Grant: 90 * 86400,
    },
    issueRefreshToken: (_ctx, client) =>
      client.grantTypeAllowed("refresh_token"),
    interactions: {
      url: (_ctx, interaction) => issuer + "/interaction/" + interaction.uid,
    },
    findAccount: async (_ctx, id) =>
      id === keys.owner
        ? { accountId: id, claims: async () => ({ sub: id }) }
        : undefined,
    claims: { openid: ["sub"] },
  };
  const provider = new Provider(issuer, config);
  provider.proxy = true;
  const mcp = bankingHandler(env, origin, keys.owner);
  const app = express();
  app.disable("x-powered-by");
  app.set("trust proxy", "loopback");
  app.use((req, res, next) => {
    res.set({
      "Cache-Control": "no-store",
      "Referrer-Policy": "no-referrer",
      "X-Content-Type-Options": "nosniff",
      "Content-Security-Policy":
        "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
    });
    if (req.get("host") !== new URL(origin).host) {
      res.status(400).send("Invalid host");
      return;
    }
    if (req.get("origin") && req.get("origin") !== origin) {
      res.status(403).send("Invalid origin");
      return;
    }
    next();
  });
  // Bound public registration and unauthenticated requests without retaining
  // unbounded IP entries. No request bodies, headers or tokens are logged.
  const rates = new Map<string, { at: number; count: number }>();
  app.use((req, res, next) => {
    const key = req.ip ?? "unknown",
      now = Date.now();
    if (rates.size > 10000) rates.clear();
    let entry = rates.get(key);
    if (!entry || now - entry.at > 60000) {
      entry = { at: now, count: 0 };
      rates.set(key, entry);
    }
    if (++entry.count > 180) {
      res.set("Retry-After", "60").status(429).send("Try again shortly");
      return;
    }
    next();
  });
  const owner = async (
    req: express.Request,
    res: express.Response,
    next: express.NextFunction,
  ) => {
    const auth = req.get("authorization") ?? "";
    const credentials = auth.startsWith("Basic ")
      ? Buffer.from(auth.slice(6), "base64").toString()
      : "";
    if (
      !credentials.startsWith("frick:") ||
      !(await sameSecret(credentials.slice(6), env.SERVICE_TOKEN))
    ) {
      res
        .set("WWW-Authenticate", 'Basic realm="Frick", charset="UTF-8"')
        .status(401)
        .send(
          "Use username frick and the Frick Worker Service password from 1Password.",
        );
      return;
    }
    next();
  };
  const html = (body: string) =>
    `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Connect Frick</title><style>body{font:16px/1.5 system-ui;max-width:650px;margin:40px auto;padding:16px;background:#fafaf7;color:#202923}button{font:inherit;padding:8px 14px}code{overflow-wrap:anywhere}table{border-collapse:collapse;width:100%}td,th{text-align:left;padding:8px;border-bottom:1px solid #ccc}</style>${body}</html>`;
  app.get("/frick/oauth/interaction/:uid", owner, async (req, res) => {
    const interaction = await provider.interactionDetails(req, res);
    const client = await provider.Client.find(
      String(interaction.params.client_id),
    );
    const requested = String(interaction.params.scope ?? "")
      .split(" ")
      .filter((s) => scopes.includes(s));
    const csrf = await seal(
      { uid: interaction.uid },
      keys.cookies[0]!,
      "oauth-consent",
    );
    res
      .type("html")
      .send(
        html(
          `<h1>Connect Frick</h1><p><strong>${e(client?.clientName ?? "MCP client")}</strong> is requesting access to your banking desk.</p><p>Client: <code>${e(interaction.params.client_id)}</code></p><p>Return to: <code>${e(interaction.params.redirect_uri)}</code></p><ul>${requested.map((s) => `<li>${s === "frick:read" ? "Read accounts, balances, history and orders" : "Prepare unsigned payment orders"}</li>`).join("")}</ul><p>Payments still require your separate approval at the bank. Access can refresh for up to 90 days. You can revoke it below.</p><form method="post"><input type="hidden" name="csrf" value="${e(csrf)}"><button name="decision" value="allow">Connect</button> <button name="decision" value="deny">Cancel</button></form><p><a href="${issuer}/connections">Manage connections</a></p>`,
        ),
      );
  });
  app.post(
    "/frick/oauth/interaction/:uid",
    owner,
    express.urlencoded({ extended: false, limit: "16kb" }),
    async (req, res) => {
      if (req.get("origin") !== origin) {
        res.status(403).send("Submit from the connection page");
        return;
      }
      const interaction = await provider.interactionDetails(req, res);
      const csrf = await unseal<{ uid: string }>(
        String(req.body.csrf ?? ""),
        keys.cookies[0]!,
        "oauth-consent",
      );
      if (csrf.uid !== interaction.uid) {
        res.status(403).send("Invalid consent");
        return;
      }
      if (req.body.decision !== "allow") {
        await provider.interactionFinished(
          req,
          res,
          {
            error: "access_denied",
            error_description: "Owner declined access",
          },
          { mergeWithLastSubmission: false },
        );
        return;
      }
      const grant = interaction.grantId
        ? await provider.Grant.find(interaction.grantId)
        : new provider.Grant({
            accountId: keys.owner,
            clientId: String(interaction.params.client_id),
          });
      if (!grant || grant.accountId !== keys.owner)
        throw new Error("Invalid grant");
      const requested = String(interaction.params.scope ?? "").split(" ");
      grant.addOIDCScope(
        requested
          .filter((s) => ["openid", "offline_access"].includes(s))
          .join(" "),
      );
      grant.addResourceScope(
        resource,
        requested.filter((s) => scopes.includes(s)).join(" "),
      );
      const grantId = await grant.save();
      await provider.interactionFinished(
        req,
        res,
        { login: { accountId: keys.owner }, consent: { grantId } },
        { mergeWithLastSubmission: false },
      );
    },
  );
  // List only grants belonging to our one owner. A grant ID is not a credential.
  // The adapter has no broad browse API, so save IDs separately for management.
  const grantsPath = join(stateDir, "connections.json");
  let grants: string[] = [];
  try {
    grants = JSON.parse(readFileSync(grantsPath, "utf8"));
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
  }
  provider.on("grant.saved", (grant) => {
    if (grant.accountId === keys.owner && !grants.includes(grant.jti)) {
      grants.push(grant.jti);
      writeFileSync(grantsPath, JSON.stringify(grants), { mode: 0o600 });
    }
  });
  app.get("/frick/oauth/connections", owner, async (req, res) => {
    const rows = await Promise.all(
      grants.map(async (id) => {
        const grant = await provider.Grant.find(id);
        if (!grant || grant.accountId !== keys.owner) return "";
        const client = await provider.Client.find(grant.clientId!);
        const csrf = await seal({ id }, keys.cookies[0]!, "oauth-revoke");
        return `<tr><td>${e(client?.clientName ?? grant.clientId)}</td><td><form method="post"><input type="hidden" name="csrf" value="${e(csrf)}"><button>Revoke</button></form></td></tr>`;
      }),
    );
    res
      .type("html")
      .send(
        html(
          `<h1>Frick connections</h1><table>${rows.join("") || "<tr><td>No active connections.</td></tr>"}</table><p><a href="/frick/">Banking desk</a></p>`,
        ),
      );
  });
  app.post(
    "/frick/oauth/connections",
    owner,
    express.urlencoded({ extended: false, limit: "16kb" }),
    async (req, res) => {
      if (req.get("origin") !== origin) {
        res.status(403).send("Submit from the connections page");
        return;
      }
      const { id } = await unseal<{ id: string }>(
        String(req.body.csrf ?? ""),
        keys.cookies[0]!,
        "oauth-revoke",
      );
      const grant = await provider.Grant.find(id);
      if (grant?.accountId === keys.owner) {
        await provider.AccessToken.revokeByGrantId(id);
        await grant.destroy();
      }
      res.redirect(303, issuer + "/connections");
    },
  );
  app.get("/.well-known/oauth-protected-resource/frick/mcp", (_req, res) =>
    res.json({
      resource,
      authorization_servers: [issuer],
      scopes_supported: scopes,
      bearer_methods_supported: ["header"],
      resource_name: "Frick banking desk",
    }),
  );
  app.get("/.well-known/oauth-authorization-server/frick/oauth", (req, res) => {
    req.url = "/.well-known/openid-configuration";
    req.originalUrl = "/frick/oauth" + req.url;
    provider.callback()(req, res);
  });
  app.all(
    "/frick/mcp",
    express.raw({ type: () => true, limit: "16kb" }),
    async (req, res) => {
      const challenge = (error?: string) =>
        res
          .set(
            "WWW-Authenticate",
            `Bearer resource_metadata="${metadata}"${error ? ', error="invalid_token"' : ""}`,
          )
          .status(401)
          .json({ error: "unauthorized" });
      const auth = req.get("authorization") ?? "";
      if (!auth.startsWith("Bearer ")) {
        challenge();
        return;
      }
      const token = await provider.AccessToken.find(auth.slice(7));
      if (
        !token ||
        token.accountId !== keys.owner ||
        token.aud !== resource ||
        !token.exp ||
        token.exp <= Math.floor(Date.now() / 1000) ||
        !token.grantId ||
        !(await provider.Grant.find(token.grantId))
      ) {
        challenge("invalid");
        return;
      }
      const headers = new Headers();
      for (const [key, value] of Object.entries(req.headers)) {
        if (value)
          headers.set(key, Array.isArray(value) ? value.join(",") : value);
      }
      const request = new Request(resource, {
        method: req.method,
        headers,
        ...(["GET", "HEAD"].includes(req.method)
          ? {}
          : { body: req.body?.length ? req.body : undefined }),
      });
      const response = await mcp.fetch(request, {
        authInfo: {
          token: auth.slice(7),
          clientId: token.clientId!,
          scopes: (token.scope ?? "").split(" "),
          expiresAt: token.exp,
          resource: new URL(resource),
          resourceMetadataUrl: metadata,
        },
      });
      res.status(response.status);
      response.headers.forEach((value, key) => res.set(key, value));
      res.send(Buffer.from(await response.arrayBuffer()));
    },
  );
  app.use("/frick/oauth", provider.callback());
  app.use((_req, res) => res.status(404).send("Not found"));
  app.use(
    (
      error: unknown,
      _req: express.Request,
      res: express.Response,
      _next: express.NextFunction,
    ) => {
      // Do not emit provider error objects: they may include private request data.
      console.error(
        "Frick MCP request failed:",
        error instanceof Error ? error.name : "error",
      );
      if (!res.headersSent)
        res
          .status(400)
          .send("Request could not be completed. Restart the connection flow.");
    },
  );
  return {
    app,
    provider,
    resource,
    issuer,
    close: async () => {
      await mcp.close();
      store.close();
    },
  };
}
