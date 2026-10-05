import { env } from "cloudflare:workers";
import { describe, expect, it, vi } from "vitest";
import worker from "../src/index";
import { Bank, iban, paymentFromForm, type Account } from "../src/bank";
import { seal, unseal } from "../src/security";
const secret = "test-service-secret";
const call = (path: string, init: RequestInit = {}) =>
  worker.fetch(new Request("https://frick.example" + path, init), env);
const authorized = { Authorization: `Bearer ${secret}` };
const formHeaders = {
  ...authorized,
  "Content-Type": "application/x-www-form-urlencoded",
};
const account: Account = {
  account: "100/001",
  customer: "100 Example",
  iban: "DE89370400440532013000",
  currency: "EUR",
  available: 100,
  balance: 100,
};
const fields = new URLSearchParams({
  from: account.iban,
  to: "DE12500105170648489890",
  name: "Example & Partner",
  amount: "12.34",
  reference: "Invoice <123>",
  customId: "frick-worker-00000000-0000-4000-8000-000000000001",
});

describe("Restricted service", () => {
  it("does not reveal bank data without authentication", async () => {
    const response = await call("/", {
      headers: { Accept: "application/json" },
    });
    expect(response.status).toBe(401);
    expect(await response.text()).not.toContain(account.iban);
    expect(response.headers.get("Cache-Control")).toBe("no-store");
  });
  it("serves discoverable HTML even when a client asks for JSON", async () => {
    const response = await call("/", {
      headers: { ...authorized, Accept: "application/json" },
    });
    expect(response.headers.get("Content-Type")).toContain("text/html");
    const html = await response.text();
    expect(html).toContain(account.iban);
    expect(html).toContain('action="/payments/review"');
    for (const id of ["accounts", "orders", "payment", "history"]) expect(html).toContain(`id="${id}"`);
    expect(html).toContain("/history?account=");
    expect(response.headers.has("Set-Cookie")).toBe(false);
  });
  it("loads older pending and failed orders and keeps incoming counterparties correct", async () => {
    const requests: URL[] = [];
    vi.stubGlobal("fetch", async (raw: string) => {
      const url = new URL(raw); requests.push(url);
      if (url.pathname === "/accounts") return Response.json({ accounts: [account] });
      const base = { customId: "example", amount: 10, currency: "EUR", creditor: { name: "My account" }, debitor: { name: "Sender <one>" } };
      if (url.searchParams.get("status") === "BOOKED") return Response.json({ transactions: [{ ...base, orderId: 1, state: "BOOKED", direction: "incoming" }], moreResults: true });
      if (url.searchParams.get("offset") === "0") return Response.json({ transactions: [{ ...base, orderId: 2, state: "PREPARED" }], moreResults: true });
      return Response.json({ transactions: [{ ...base, orderId: 3, state: "ERROR" }, { ...base, orderId: 4, state: "REJECTED" }, { ...base, orderId: 5, state: "EXPIRED" }], moreResults: false });
    });
    try {
      const response = await worker.fetch(new Request("https://frick.example/", { headers: authorized }), { MODE: "live", SERVICE_TOKEN: secret, RELAY_URL: "http://frick:8087", RELAY_TOKEN: "relay" });
      const html = await response.text();
      expect(response.status).toBe(200);
      for (const id of [2,3,4,5]) expect(html).toContain(`href="/orders/${id}"`);
      expect(html).toContain("Sender &lt;one&gt;");
      expect(html).toContain("+10.00 EUR");
      expect(html).toContain("&amp;offset=15");
      expect(requests.some((u) => u.searchParams.get("offset") === "100")).toBe(true);
      expect(requests.some((u) => u.searchParams.get("limit") === "15")).toBe(true);
    } finally { vi.unstubAllGlobals(); }
  });
  it("does not mistake unavailable order data for an empty list", async () => {
    vi.stubGlobal("fetch", async (raw: string) => new URL(raw).pathname === "/accounts" ? Response.json({ accounts: [account] }) : new Response("unavailable", { status: 502 }));
    try {
      const response = await worker.fetch(new Request("https://frick.example/", { headers: authorized }), { MODE: "live", SERVICE_TOKEN: secret, RELAY_URL: "http://frick:8087", RELAY_TOKEN: "relay" });
      const html = await response.text();
      expect(html).toContain("Bank data unavailable");
      expect(html).not.toContain("No pending orders");
      expect(html).toContain('action="/payments/review"');
    } finally { vi.unstubAllGlobals(); }
  });
  it("uses per-request HTTP authentication and rejects cross-origin Basic writes", async () => {
    const basic = { Authorization: `Basic ${btoa("frick:" + secret)}` };
    expect((await call("/", { headers: basic })).status).toBe(200);
    expect(
      (await call("/", { headers: { Cookie: "__Host-frick=old-session" } }))
        .status,
    ).toBe(401);
    const rejected = await call("/payments/review", {
      method: "POST",
      headers: {
        ...basic,
        "Content-Type": "application/x-www-form-urlencoded",
        Origin: "https://evil.example",
      },
      body: fields.toString(),
    });
    expect(rejected.status).toBe(403);
    const accepted = await call("/payments/review", {
      method: "POST",
      headers: {
        ...basic,
        "Content-Type": "application/x-www-form-urlencoded",
        Origin: "https://frick.example",
      },
      body: fields.toString(),
    });
    expect(accepted.status).toBe(200);
    const challenge = await call("/");
    expect(challenge.headers.get("WWW-Authenticate")).toContain("Basic");
    expect(
      (await call("/login", { method: "POST", headers: formHeaders })).status,
    ).toBe(404);
  });
  it("review escapes bank and recipient text and preserves the request ID", async () => {
    const response = await call("/payments/review", {
      method: "POST",
      headers: formHeaders,
      body: fields.toString(),
    });
    const html = await response.text();
    expect(html).toContain("Example &amp; Partner");
    expect(html).toContain("Invoice &lt;123&gt;");
    const review = html.match(/name="review" value="([^"]+)"/)![1];
    const create = () =>
      call("/payments/create", {
        method: "POST",
        headers: { ...formHeaders, Accept: "application/json" },
        body: new URLSearchParams({ review }).toString(),
      });
    const first = await (await create()).text();
    const repeated = await (await create()).text();
    expect(first).toContain("PREPARED");
    expect(first).toContain(fields.get("customId"));
    expect(repeated).toContain(fields.get("customId"));
  });
  it("rejects a tampered review and GET requests to the creation route", async () => {
    const review = await seal({ amount: 1 }, secret, "payment");
    const changed = review.slice(0, 8) + "X" + review.slice(9);
    const response = await call("/payments/create", {
      method: "POST",
      headers: formHeaders,
      body: new URLSearchParams({ review: changed }).toString(),
    });
    expect(response.status).toBe(400);
    expect(
      (await call("/payments/create", { headers: authorized })).status,
    ).toBe(404);
    expect((await call("/sign", { headers: authorized })).status).toBe(404);
  });
  it("rejects another source account, bad decimals and bad IBAN checksums", () => {
    expect(() => iban("DE00370400440532013000")).toThrow();
    for (const amount of ["-1", "1e5", "12.345", "NaN", "0"]) {
      const f = new URLSearchParams(fields);
      f.set("amount", amount);
      expect(() => paymentFromForm(f, [account])).toThrow();
    }
    expect(() => paymentFromForm(fields, [])).toThrow();
  });
  it("separates signature purposes", async () => {
    const token = await seal("review", secret, "other-purpose");
    await expect(unseal(token, secret, "payment")).rejects.toThrow();
    await expect(
      unseal(token, "different-secret", "other-purpose"),
    ).rejects.toThrow();
  });
  it("rejects expired reviews", async () => {
    const token = await seal("review", secret, "payment");
    const now = vi
      .spyOn(Date, "now")
      .mockReturnValue(Date.now() + 31 * 60 * 1000);
    try {
      await expect(unseal(token, secret, "payment")).rejects.toThrow();
    } finally {
      now.mockRestore();
    }
  });
  it("keeps bank credentials out of the frontend and uses a fixed private relay", async () => {
    const requests: string[] = [];
    vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
      requests.push(url);
      expect(init.redirect).toBe("manual");
      const headers = new Headers(init.headers);
      expect(headers.get("Authorization")).toBe("Bearer private-relay-token");
      expect(headers.has("Signature")).toBe(false);
      if (init.method === "POST")
        return Response.json({
          transactions: [
            {
              ...JSON.parse(String(init.body)),
              state: "PREPARED",
              orderId: 100,
            },
          ],
        });
      return Response.json({ accounts: [account] });
    });
    try {
      const bank = new Bank({
        MODE: "live",
        SERVICE_TOKEN: secret,
        RELAY_URL: "http://frick:8087",
        RELAY_TOKEN: "private-relay-token",
      });
      expect((await bank.accounts())[0].iban).toBe(account.iban);
      expect(
        (await bank.create(paymentFromForm(fields, [account])))[0].state,
      ).toBe("PREPARED");
      expect(requests).toEqual([
        "http://frick:8087/accounts",
        "http://frick:8087/payments",
      ]);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
