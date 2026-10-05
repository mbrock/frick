# Frick remote MCP

Endpoint: **https://less.rest/frick/mcp**. OAuth issuer:
**https://less.rest/frick/oauth**. Manage/revoke authorized connections at
**https://less.rest/frick/oauth/connections**.

The MCP adapter reuses `../web/src/index.ts` directly. Its tools return the same
HTML links and forms as the browser desk:

- `read_page(path)` starts at `/`, then follows local account/history/order links.
- `submit_form(path, fields)` submits only `/payments/review` and
  `/payments/create`. Review tokens bind the exact payment and expire after
  30 minutes. Creation produces an **unsigned** EUR SEPA order.
- `get_profile()` returns a stable, opaque owner ID.

There is no signing, trading, TAN, credential administration, arbitrary URL fetch,
or bank API passthrough. Signing remains at the bank.

## Authentication

`frick:read` allows account and order reads. `frick:prepare` additionally permits
unsigned-order preparation; that tool requires both scopes. Access tokens are
opaque, expire after 15 minutes, and are bound to this exact MCP endpoint. Each
request verifies its grant, expiry, owner and audience. Refresh tokens rotate and
are limited by the 90-day grant. PKCE S256 is required. Authorization codes expire
after 60 seconds and cannot be reused.

The maintained `oidc-provider` implements the authorization protocol. Clients use
OAuth metadata discovery, resource indicators, dynamic client registration, or
CIMD. CIMD document fetching is limited to `chatgpt.com` and `codex.openai.com` to
avoid server-side requests to arbitrary hosts. Other clients can use registration.

At the authorization page, log in once using HTTP Basic: username `frick`, with
the **Frick Worker Service** password in 1Password. Review the client identity,
redirect address and permissions, then click Connect. The MCP client stores and
refreshes its own restricted grant; agents never receive the owner password or
bank API key. The owner can revoke grants on the management page. OAuth uses
short-lived browser interaction cookies; MCP requests themselves use bearer
tokens and have no transport sessions or cookies.

`@modelcontextprotocol/server` v2 implements protocol 2026-07-28 with a fresh
server per request. Stateless compatibility for 2025-era clients remains enabled;
no `Mcp-Session-Id` is issued. The current protocol includes its version/capability
envelope on every request instead of an initialization handshake.

## Connect

Add the endpoint URL as a **remote MCP connection with OAuth** in ChatGPT or your
MCP client. Leave client ID/secret blank for dynamic registration/CIMD. Authorize
on the Frick page. A ChatGPT-hosted connection can then be used from its supported
web/mobile clients; adding a server only to a local Codex configuration does not
by itself create a hosted ChatGPT connection.

For a local Codex connection, use `codex mcp add frick --url
https://less.rest/frick/mcp`, then `codex mcp login frick`. No custom bearer token
needs to be pasted into configuration.

## Develop and deploy

Requires Node 24 LTS or newer supported LTS. The provider deliberately warns on
non-LTS Node releases. Run `npm ci`, `npm run check`, `npm test`, `npm run build`.
The integration test uses fictional accounts and a temporary SQLite store; it
checks PKCE, consent, code reuse, read/prepare scopes, HTML review/create, current
and legacy MCP requests, persistence, refresh, audience rejection and revocation.
It does not submit a live bank payment.

Igloo runs a separate loopback service on port 8788; the existing browser service
on 8787 is unchanged. The versioned systemd unit and Caddy fragment are in
`deploy/`. Deploy from the repository, install dependencies, build the bundle,
create `~/.local/state/frick-mcp` with mode 700, install the user unit, and enable
it. A dedicated Node installation is `~/.local/lib/node-frick`; do not replace
Igloo's existing `/usr/bin/node` Bun shim.

The service uses the existing private frontend environment file for the HTML
handler's restricted relay access. New OAuth private signing keys, cookie keys,
owner ID, clients, grants and refresh state live only in the private state
directory (files mode 600), never in Git. Back up that directory privately;
losing it requires clients to reconnect. Grant revocation takes effect on the
next MCP request. Restarting preserves grants and used-code records.
