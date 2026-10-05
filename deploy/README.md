# Private Frick relay

`frick serve --state-dir=...` runs a JSON service under its own tsnet hostname (`frick`, `tag:frick`). Its listener belongs to the embedded tailnet node, not the host's public/LAN interface. Use `--local --listen=127.0.0.1:8087` only for local development.

The service requires a dedicated restricted bank API key and RSA private key, separate from the powerful CLI credentials. It checks the JWT scopes at startup and renewal: only `accounts`, `transactions`, `createTransaction`, and optional `camt053` are accepted. It refuses broader keys. Use a separate JWT cache path.

The bridge accepts its own Bearer credential (`FRICK_RELAY_TOKEN`, minimum 32 characters). Routes:

- `GET /accounts`
- `GET /transactions?account=<own-IBAN>&offset=0&status=PREPARED` (status optional, limit 1–100). Reads start at 2000-01-01 to avoid the bank's 30-day default; use status=BOOKED for ledger transactions and omit status for payment orders.
- `GET /orders/<order-id>`
- `POST /payments` — strict JSON, one unsigned EUR SEPA payment, stable `frick-worker-<UUID>` custom ID.
- `GET /health`

There is no arbitrary upstream path, signing, trading, deletion, TAN, or credential-management route. Payment creation validates source account ownership, currency, decimal amount, IBAN checksums and allowed fields. Errors omit upstream bodies and credentials. No write retries. Application-level bank response signatures are not verified; HTTPS authenticates the bank transport.

Production on Chapel uses the user unit in this directory, `~/.config/frick-relay/.env` (0600), `~/.config/frick-relay/private-key.pem` (0600), and `~/.local/state/frick-relay/` (0700). The user manager has linger enabled for boot startup. A one-use `tag:frick` auth key enrolls tsnet via optional `bootstrap.env`; remove that file after enrollment. The retained private tsnet state permits restarts without an auth key. The runtime never needs the Tailscale administration API key.

Igloo's HTML frontend connects to `http://frick.whale-justice.ts.net:8087` inside WireGuard. Caddy exposes only the frontend at `https://less.rest/frick/`. Banking credentials never reach Igloo. The public service credential and bridge credential are separate. The frontend source is in this repository under `web/`; its original Cloudflare deployment has been removed. See [`web/README.md`](../web/README.md) for setup.

Build: `go test ./...`, `go build .`, or `nix build`. tsnet is pinned to Tailscale 1.100.0 to remain on the existing Go 1.26 toolchain. The standalone CLI commands remain available and retain their existing behavior.
