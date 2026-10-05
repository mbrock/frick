# Frick

Bank Frick command-line client and private hypermedia banking desk.

- Go CLI and restricted Tailscale relay: repository root; [deployment](deploy/README.md).
- TypeScript HTML service: [`web/`](web/README.md), served at [less.rest/frick](https://less.rest/frick/).

The web service presents accounts, recent booked transactions, unsigned payment preparation, and pending / failed orders on one page. Plain HTML links and forms expose its available actions. Every request authenticates through HTTP headers; there are no sessions or browser scripts.

The HTML frontend runs on Igloo. Its restricted Go relay runs on Chapel under a dedicated tsnet identity. Bank credentials stay on Chapel. Payment orders created through the service remain unsigned and require separate approval.

Both components live in this repository. `mbrock/frick-worker` is the historical frontend repository; production no longer uses Cloudflare Workers.

## Checks

```sh
go test ./...
cd web
npm ci
npm run check
npm test
```

Use `nix build` for the Go executable, or `go build .` with Go 1.26.4 or newer. See the deployment documents for service configuration and secret provisioning.
