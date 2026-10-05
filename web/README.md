# Frick web service

Private HTML banking service at **https://less.rest/frick/**. Caddy on Igloo forwards this path to a loopback-only Bun process. Its TypeScript HTML handler talks over Tailscale to a restricted Go JSON bridge on Chapel, using a dedicated `frick` tsnet identity (`tag:frick`). The bridge calls the bank from Chapel's office connection.

Bank credentials and RSA signing remain on Chapel. Igloo holds only a public service access credential and a separate bridge credential. The original Cloudflare deployment was removed after bank authorization worked locally but failed from Cloudflare.

## Use

Browsers authenticate using HTTP Basic over HTTPS: username **frick**, password from the **Frick Worker Service** item in the Personal 1Password vault. Safari was verified with the real account page. The Codex in-app browser currently fails on the HTTP Basic challenge. An agent sends `Authorization: Bearer <service-token>` instead.

The canonical source for both frontend and relay is `mbrock/frick`: this `web/` directory is deployed to Igloo. The old `mbrock/frick-worker` repository is historical.

The landing page combines an account table, pending orders, the payment form and 15 recent booked transactions per account. Historical failed / expired / deleted orders appear below on that same page so old failures do not bury current activity. It queries each payment-order state separately for prepared, processing, rejected, expired, faulty and deleted states; after 2,500 records per state it explicitly offers continuation rather than claiming completeness. Full history is paginated; booked history and payment orders have separate links. Bank reads start at 2000-01-01 to avoid hiding old records behind the default 30-day window. See the [bank status/filter documentation](https://developers.bankfrick.li/api_docs). Failed reads are shown as unavailable, never as an empty list.

The entry URL and credential are enough to start. Follow HTML links to history, pending orders and individual orders. Follow the payment link, fill the form, review it, then create an **unsigned** EUR SEPA order when instructed. Approval remains separate at the bank. This service cannot sign, trade, delete orders or manage bank credentials.

HTML is the sole application representation. Links identify resources and forms declare methods, destinations and fields. Clients use those returned affordances rather than a banking URL schema. Domain validation remains server-side, repeated on Chapel before creation. No browser scripts, session cookies, session database or separate JSON application interface are needed. The JSON bridge is a private implementation boundary.

The payment review is HMAC authenticated, expires after 30 minutes and carries its own form state. It preserves the request ID on resubmission; the bank enforces custom-ID uniqueness. There are no automatic write retries. Check pending orders if a creation outcome is uncertain. All GETs are read-only. The bank stores account/order state; the web service stores no history or payment drafts.

Every request carries HTTP authentication. Basic-authenticated writes require a same-origin Origin header, since browsers resend Basic credentials automatically. Authenticated agent Bearer requests may omit Origin. Service-token rotation invalidates previous credentials; browsers manage their own HTTP credential cache. Closing the private browsing context clears that client context. There is no fake logout endpoint. HTML escapes bank/recipient text, responses disallow caching, framing and scripts, and application errors omit credentials and bank response bodies.

## Development and tests

```sh
npm ci
npm run check
npm test
```

Tests run the shared web handler in the Workers test runtime, with fictional data and mocked bridge calls. Production runs that same handler under Bun via `src/native.ts`, mounted at `/frick/`. The adapter rewrites its same-site links/form actions with the path prefix and uses the known HTTPS origin for Origin checks. `ServiceEnv` describes only the web service and bridge credentials, never bank secrets.

To run a fictional demo with Bun, set `MODE=demo`, `SERVICE_TOKEN=<local-test-token>`, then run `bun run src/native.ts`. The listener is `127.0.0.1:8787`. Demo creation is not persistent. Keep secret values out of source files, URLs and command arguments.

## Deployment

The bridge's source and user-unit instructions are in [mbrock/frick](https://github.com/mbrock/frick), `deploy/README.md`. Its tsnet hostname is `frick.whale-justice.ts.net`, listening only inside the tailnet at port 8087. Tailscale encrypts the Igloo-to-Chapel leg. Enrollment uses a one-use tagged auth key; the runtime keeps private tsnet state and does not need the Tailscale administration API key.

Igloo runs `deploy/frick-web.service` as a user unit, enabled for boot startup, with `~/.config/frick-relay/frontend.env` (0600). Deploy the five TypeScript files under `src/` to `~/frick-web/src/`, install the user unit and restart it. No runtime npm dependencies are needed. Caddy's `less.rest` site has only this new routing block:

```caddy
@frick path /frick /frick/*
handle @frick {
    reverse_proxy 127.0.0.1:8787
}
```

Validate the Caddyfile before reloading it. Its pre-change backup on Igloo is `/etc/caddy/Caddyfile.before-frick-20261005`. Existing sites/routes remain intact.

`python3 scripts/provision-native.py` copies dedicated credentials from Personal 1Password items to private files through SSH stdin. It never prints secret values and does not create a Tailscale node. Restart each service after changing its credentials. Scripts prefer the stable `/usr/local/bin/op` installed from the PKG, and honor `FRICK_OP` if another executable is needed.

`python3 scripts/read-service.py --check` verifies account/history/pending/form reads. `--interactive` reads the service credential once and retains it in memory until exit, accepting read-only paths. Stable 1Password CLI 2.40.0 contains the coding-agent prompt fix first introduced in 2.40.0-beta.02; the older Homebrew executable may still precede it on PATH.

## Verification and limits

Live HTTPS checks passed for the consolidated dashboard, both booked account histories and older-page links, pending / failed orders and payment preparation. The dashboard loaded in 1.26 seconds with 30 booked transactions and all 20 historical failed orders. The Go suite and 11 web tests passed, including older failure pagination and partial bank-read failures. The review form was submitted with a fictional recipient; the creation form was never submitted. No real payment was created during deployment. Native Safari login and rendering were verified. Both services survived restart without the one-use enrollment key.

The bridge authenticates the bank through HTTPS and creates exact-body RSA PKCS#1 v1.5 SHA-512 signatures. Bank application-level response signatures are not yet verified. The Go bridge rejects bank JWT scopes beyond accounts, transactions, unsigned creation and optional camt053. It accepts a strict limited JSON payment schema and revalidates source ownership, currency, amount, IBANs and request ID.

Architecture references: [HATEOAS](https://htmx.org/essays/hateoas/), [Hypermedia Clients](https://htmx.org/essays/hypermedia-clients/), [Hypermedia-Friendly Scripting](https://htmx.org/essays/hypermedia-friendly-scripting/), [separate hypermedia/data APIs](https://htmx.org/essays/why-tend-not-to-use-content-negotiation/) and Fielding's [stateless constraint](https://ics.uci.edu/~fielding/pubs/dissertation/rest_arch_style.htm#sec_5_1_3) and [cookie critique](https://ics.uci.edu/~fielding/pubs/dissertation/evaluation.htm#sec_6_3_4_2).
