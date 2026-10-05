import type { ServiceEnv } from "./env";
import {
  Bank,
  paymentFromForm,
  type Account,
  type Payment,
  type Transaction,
} from "./bank";
import { escape as e, limitedText, sameSecret, seal, unseal } from "./security";

const style = `*{box-sizing:border-box}body{font:14px/1.4 system-ui,sans-serif;color:#202923;background:#fafaf7;margin:0}main{max-width:1600px;margin:auto;padding:14px 20px}a{color:#17618a}h1{font-size:18px;margin:8px 0}h2{font-size:16px;margin:0 0 8px}h3{font-size:14px;margin:12px 0 4px}nav{display:flex;gap:18px;flex-wrap:wrap;border-bottom:1px solid #b7bdb6;padding-bottom:9px}nav a:first-child{font-weight:750;color:inherit}label{display:block;font-size:13px;font-weight:600}input,select,button{font:inherit;padding:5px 7px;border:1px solid #aeb4ab;border-radius:2px}input,select{display:block;width:100%;margin-top:3px;background:white}button{background:#edf0e9;color:#202923;font-weight:600;cursor:pointer}button:hover{background:#dfe8dd}button:focus-visible,a:focus-visible,input:focus-visible,select:focus-visible{outline:2px solid #17618a;outline-offset:2px}table{border-collapse:collapse;width:100%;margin:5px 0 8px}th,td{text-align:left;border-bottom:1px solid #dce0d8;padding:5px 8px;vertical-align:top}th{font-size:12px;color:#5d675e;background:#f0f2ec}th:first-child,td:first-child{padding-left:0}small,.muted{color:#657067}small{font-size:12px}.notice{padding:8px 10px;background:#edf2e9;border-left:3px solid #367645}.error{color:#a02d21}.scroll{overflow-x:auto}dt{font-weight:600}dd{margin:0 0 8px;overflow-wrap:anywhere}code{font:12px ui-monospace,SFMono-Regular,monospace;overflow-wrap:anywhere}.inline{display:inline}.num{text-align:right;white-space:nowrap;font-variant-numeric:tabular-nums}.date{white-space:nowrap}.state{font-size:11px;font-weight:650;white-space:nowrap}.state-PREPARED,.state-IN_PROGRESS{color:#17618a}.state-ERROR,.state-REJECTED,.state-EXPIRED{color:#a02d21}section{margin:12px 0}.desk{display:grid;grid-template-columns:minmax(0,1fr) 380px;gap:24px}.desk section{margin:0}.desk #history{margin-top:18px}.payment{display:grid;grid-template-columns:1fr 1fr;gap:8px 12px}.payment .wide{grid-column:1/-1}.payment button{justify-self:start}.section-head{display:flex;gap:12px;align-items:baseline;justify-content:space-between}.section-head h2{margin-bottom:0}.empty{margin:5px 0;color:#657067}footer{border-top:1px solid #b7bdb6;margin-top:22px;padding-top:8px;color:#657067;font-size:12px}.amount-in{color:#367645}.summary{margin:5px 0 10px;font-size:12px;color:#657067}@media(max-width:850px){main{padding:12px}.desk{grid-template-columns:1fr;gap:18px}nav{gap:12px}table{font-size:13px}.accounts{min-width:680px}.tx{min-width:650px}}`;

function document(title: string, body: string, demo: boolean) {
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${e(title)} · Frick</title><style>${style}</style></head><body><main><nav><a href="/">Frick</a><a href="/#accounts">Accounts</a><a href="/#orders">Orders</a><a href="/#payment">Prepare a payment</a><a href="/#history">History</a><a href="/help">How this works</a></nav>${demo ? '<p class="notice">Demo — simulated accounts and payments.</p>' : ""}<h1>${e(title)}</h1>${body}<footer>Orders created here remain unsigned. Approve payments separately with Bank Frick.</footer></main></body></html>`;
}
function page(title: string, body: string, env: ServiceEnv, status = 200) {
  return new Response(document(title, body, env.MODE === "demo"), {
    status,
    headers: { "Content-Type": "text/html; charset=utf-8" },
  });
}
function money(amount: number, currency: string) {
  return `${new Intl.NumberFormat("en", { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(amount)} ${e(currency)}`;
}
function details(t: Transaction | Payment) {
  return `<dl><dt>Amount</dt><dd>${money(t.amount, t.currency)}</dd><dt>Recipient</dt><dd>${e(t.creditor.name)}</dd><dt>Recipient IBAN</dt><dd><code>${e(t.creditor.iban)}</code></dd><dt>From</dt><dd><code>${e(t.debitor.iban)}</code></dd><dt>Reference</dt><dd>${e(t.reference) || "—"}</dd><dt>Request ID</dt><dd><code>${e(t.customId)}</code></dd>${"state" in t ? `<dt>Status</dt><dd>${e(t.state)}</dd>` : ""}</dl>`;
}
function rows(transactions: Transaction[]) {
  return transactions.length
    ? `<div class="scroll"><table class="tx"><thead><tr><th>Date / order</th><th>Counterparty / reference</th><th class="num">Amount</th><th>Status</th></tr></thead><tbody>${transactions.map((t) => {
      const incoming = ["incoming", "credit"].includes((t.direction ?? "").toLowerCase());
      const outgoing = ["outgoing", "debit"].includes((t.direction ?? "").toLowerCase());
      const party = incoming ? t.debitor : t.creditor;
      return `<tr><td class="date">${e(t.bookingDate || t.valuta || "—")} · ${t.orderId > 0 ? `<a href="/orders/${e(t.orderId)}">#${e(t.orderId)}</a>` : `<code>${e(t.transactionNr || "—")}</code>`}</td><td>${e(party?.name || party?.iban || "—")}<br><small>${e(t.reference) || "—"}</small></td><td class="num ${incoming ? "amount-in" : ""}">${incoming ? "+" : outgoing ? "−" : ""}${money(Math.abs(t.amount), t.currency)}</td><td><span class="state state-${e(t.state)}">${e(t.state)}</span></td></tr>`;
    }).join("")}</tbody></table></div>`
    : '<p class="empty">No transactions in this view.</p>';
}
function paymentForm(accounts: Account[]) {
  const eur = accounts.filter((a) => a.currency === "EUR");
  if (!eur.length) return '<p class="empty">No EUR account available for SEPA payments.</p>';
  return `<p class="summary">EUR SEPA · review first, then create an unsigned order.</p><form class="payment" method="post" action="/payments/review"><input type="hidden" name="customId" value="frick-worker-${crypto.randomUUID()}"><label class="wide">From account<select name="from">${eur.map((a) => `<option value="${e(a.iban)}">${e(a.iban)} · ${money(a.available, a.currency)}</option>`).join("")}</select></label><label class="wide">Recipient name<input name="name" maxlength="140" autocomplete="off" required></label><label class="wide">Recipient IBAN<input name="to" required spellcheck="false" autocomplete="off"></label><label>Amount in EUR<input name="amount" inputmode="decimal" pattern="[0-9]+([.][0-9]{1,2})?" placeholder="100.00" required></label><label>Payment reference<input name="reference" maxlength="140"></label><button class="wide">Review payment →</button></form>`;
}
const orderStates = new Set(["PREPARED", "IN_PROGRESS", "ERROR", "REJECTED", "EXPIRED", "DELETED", "DELETION_REQUESTED"]);
async function outstanding(bank: Bank, account: Account) {
  const groups = await Promise.all([...orderStates].map(async (status) => {
    const orders: Transaction[] = [];
    for (let offset = 0; offset < 2500; offset += 100) {
      const result = await bank.transactions(account, new URLSearchParams({ status, firstPosition: String(offset), maxResults: "100" }));
      orders.push(...(result.transactions ?? []));
      if (!result.moreResults) return { orders, status, complete: true };
    }
    return { orders, status, complete: false };
  }));
  const unique = new Map(groups.flatMap((g) => g.orders).map((t) => [t.orderId, t]));
  return { orders: [...unique.values()].sort((a,b) => b.orderId-a.orderId), remaining: groups.filter((g) => !g.complete).map((g) => g.status) };
}
function remainingOrders(account: Account, remaining: string[]) {
  return remaining.map((status) => `<p class="notice">More ${e(status.toLowerCase())} orders remain. <a href="/history?account=${encodeURIComponent(account.iban)}&amp;status=${e(status)}&amp;offset=2500">Continue →</a></p>`).join("");
}

async function authenticated(
  request: Request,
  env: ServiceEnv,
): Promise<"bearer" | "basic" | false> {
  const auth = request.headers.get("Authorization") ?? "";
  if (
    auth.startsWith("Bearer ") &&
    (await sameSecret(auth.slice(7), env.SERVICE_TOKEN))
  )
    return "bearer";
  if (auth.startsWith("Basic "))
    try {
      const credentials = atob(auth.slice(6));
      if (
        credentials.startsWith("frick:") &&
        (await sameSecret(credentials.slice(6), env.SERVICE_TOKEN))
      )
        return "basic";
    } catch {
      /* Malformed HTTP credentials are rejected. */
    }
  return false;
}
async function form(request: Request) {
  if (
    !request.headers
      .get("Content-Type")
      ?.startsWith("application/x-www-form-urlencoded")
  )
    throw new Error("Submit the provided HTML form.");
  return new URLSearchParams(await limitedText(request, 16384));
}
async function handle(request: Request, env: ServiceEnv): Promise<Response> {
  const url = new URL(request.url),
    path = url.pathname;
  if (path === "/health" && request.method === "GET")
    return Response.json({ status: "ok" });
  if (!env.SERVICE_TOKEN)
    return page(
      "Service not configured",
      "<p>The service access credential has not been installed.</p>",
      env,
      503,
    );
  const auth = await authenticated(request, env);
  // Browsers resend Basic credentials automatically, so writes still need CSRF protection.
  if (
    request.method === "POST" &&
    auth === "basic" &&
    request.headers.get("Origin") !== url.origin
  )
    return page(
      "Request rejected",
      "<p>Submit the form from this service.</p>",
      env,
      403,
    );
  if (!auth) {
    const response = page(
      "Authentication required",
      "<p>Use HTTP authentication: username <strong>frick</strong>, and the <strong>Frick Worker Service</strong> password from 1Password. Agents can send that credential as a Bearer token.</p>",
      env,
      401,
    );
    response.headers.set(
      "WWW-Authenticate",
      'Basic realm="Frick", charset="UTF-8"',
    );
    return response;
  }
  if (path === "/help" && request.method === "GET")
    return page(
      "How this works",
      "<p>Browse accounts, follow a history link, or prepare a EUR SEPA payment. Review the recipient and amount, then create an unsigned order. Creation sends the order to the bank; approval happens separately.</p><p>Every request authenticates through HTTP headers: Basic for a browser, Bearer for an agent. Follow the links and submit the forms shown here. GET requests only read data. There are no login cookies or server sessions.</p><p>A payment review expires after 30 minutes. Its request ID stays the same when submitted again, so a network retry cannot create a second bank order with a different ID. If the bank response is uncertain, check pending orders before retrying.</p>",
      env,
    );
  const bank = new Bank(env);
  if (path === "/" && request.method === "GET") {
    const accounts = await bank.accounts();
    const groups = await Promise.all(accounts.map(async (account) => {
      const [history, orders] = await Promise.allSettled([
        bank.transactions(account, new URLSearchParams({ status: "BOOKED", maxResults: "15" })),
        outstanding(bank, account),
      ]);
      return { account, history, orders };
    }));
    const historyLink = (a: Account) => `/history?account=${encodeURIComponent(a.iban)}`;
    const unavailable = '<p class="error">Bank data unavailable for this account. <a href="/">Try again</a>.</p>';
    const active = new Set(["PREPARED", "IN_PROGRESS", "DELETION_REQUESTED"]);
    const orderGroups = (pending: boolean) => groups.map((g) => {
      if (g.orders.status === "rejected") return `<h3>${e(g.account.currency)}</h3>${unavailable}`;
      const list = g.orders.value.orders.filter((t) => active.has(t.state) === pending);
      return list.length ? `<h3>${e(g.account.currency)}</h3>${rows(list)}${remainingOrders(g.account,g.orders.value.remaining.filter((status) => active.has(status) === pending))}` : remainingOrders(g.account,g.orders.value.remaining.filter((status) => active.has(status) === pending));
    }).join("");
    const pending = orderGroups(true), failed = orderGroups(false);
    const history = [...groups].sort((a,b) => Number(b.account.currency === "EUR")-Number(a.account.currency === "EUR")).map((g) => `<div class="section-head"><h3>${e(g.account.currency)} · <code>${e(g.account.iban)}</code></h3><a href="${historyLink(g.account)}">Full history →</a></div>${g.history.status === "rejected" ? unavailable : `${rows(g.history.value.transactions ?? [])}${g.history.value.moreResults ? `<p><a href="${historyLink(g.account)}&amp;offset=15">Older transactions →</a></p>` : ""}`}`).join("");
    return page("Banking desk",
      `<section id="accounts"><div class="section-head"><h2>Accounts</h2><a href="/">Refresh ↻</a></div><div class="scroll"><table class="accounts"><thead><tr><th>Currency</th><th>IBAN</th><th>Customer / account</th><th class="num">Balance</th><th class="num">Available</th><th></th></tr></thead><tbody>${accounts.map((a) => `<tr><td><strong>${e(a.currency)}</strong></td><td><code>${e(a.iban)}</code></td><td>${e(a.customer)}<br><small>${e(a.account)}</small></td><td class="num">${money(a.balance,a.currency)}</td><td class="num">${money(a.available,a.currency)}</td><td><a href="${historyLink(a)}">History →</a></td></tr>`).join("")}</tbody></table></div></section>
      <div class="desk"><div><section id="orders"><div class="section-head"><h2>Pending orders</h2><a href="#failed">Rejected / expired ↓</a></div>${pending || '<p class="empty">No pending orders.</p>'}</section><section id="history"><h2>Recent transactions</h2>${history}</section></div><section id="payment"><h2>Prepare a payment</h2>${paymentForm(accounts)}</section></div>
      <section id="failed"><div class="section-head"><h2>Failed / expired / deleted orders</h2><a href="/pending">All pending / failed orders →</a></div><p class="summary">Historical unsuccessful orders. Check the details and booked history before preparing a replacement.</p>${failed || '<p class="empty">No failed orders.</p>'}</section>`,env);
  }
  if (path === "/history" && request.method === "GET") {
    const account = (await bank.accounts()).find(
      (a) => a.iban === url.searchParams.get("account"),
    );
    if (!account)
      return page(
        "Account not found",
        '<p><a href="/">Choose an account</a></p>',
        env,
        404,
      );
    const offset = Number(url.searchParams.get("offset") ?? 0);
    if (!Number.isSafeInteger(offset) || offset < 0)
      throw new Error("Invalid page offset.");
    const query = new URLSearchParams({
      maxResults: "100",
      firstPosition: String(offset),
      order: "desc",
    });
    const status = url.searchParams.get("status");
    if (status) query.set("status", status);
    else if (url.searchParams.get("view") !== "orders") query.set("status", "BOOKED");
    const result = await bank.transactions(account, query);
    const next = new URL(url);
    next.searchParams.set("offset", String(offset + 100));
    return page(
      "Transaction history",
      `<p><code>${e(account.iban)}</code> · <a href="/history?account=${encodeURIComponent(account.iban)}">Booked transactions</a> · <a href="/history?account=${encodeURIComponent(account.iban)}&amp;view=orders">Payment orders</a></p>${rows(result.transactions ?? [])}${result.moreResults ? `<a rel="next" href="${e(next.pathname + next.search)}">Older transactions →</a>` : ""}`,
      env,
    );
  }
  if (path === "/pending" && request.method === "GET") {
    const accounts = await bank.accounts();
    const groups = await Promise.all(accounts.map(async (account) => ({ account, result: await outstanding(bank,account) })));
    return page("Pending / failed orders", `<p>Approval happens separately with Bank Frick.</p>${groups.map((g) => `<h2>${e(g.account.currency)} account</h2>${rows(g.result.orders)}${remainingOrders(g.account,g.result.remaining)}`).join("")}`, env);
  }
  if (/^\/orders\/\d+$/.test(path) && request.method === "GET") {
    const order = await bank.order(path.split("/")[2]);
    return page(`Order #${order.orderId}`, details(order), env);
  }
  if (path === "/payments/new" && request.method === "GET") {
    return page("Prepare a payment", paymentForm(await bank.accounts()), env);
  }
  if (path === "/payments/review" && request.method === "POST") {
    const payment = paymentFromForm(await form(request), await bank.accounts());
    const review = await seal(payment, env.SERVICE_TOKEN, "payment");
    return page(
      "Review payment",
      `${details(payment)}<p class="notice">Creating this order does not send money. It will await your separate approval.</p><form method="post" action="/payments/create"><input type="hidden" name="review" value="${e(review)}"><button>Create unsigned order</button></form><p><a href="/payments/new">Start again</a></p>`,
      env,
    );
  }
  if (path === "/payments/create" && request.method === "POST") {
    const reviewed = await unseal<Payment>(
      (await form(request)).get("review") ?? "",
      env.SERVICE_TOKEN,
      "payment",
    );
    if (reviewed.type !== "SEPA" || reviewed.currency !== "EUR")
      throw new Error("This service prepares EUR SEPA payments only.");
    // Validate again even if a service-token holder constructs their own signed review.
    const payment = paymentFromForm(
      new URLSearchParams({
        from: reviewed.debitor.iban,
        to: reviewed.creditor.iban,
        name: reviewed.creditor.name,
        amount: String(reviewed.amount),
        reference: reviewed.reference,
        customId: reviewed.customId,
      }),
      await bank.accounts(),
    );
    const orders = await bank.create(payment);
    return page(
      env.MODE === "demo" ? "Demo order prepared" : "Unsigned order created",
      `<p>The order awaits separate approval.</p>${orders.map((t) => `<h2><a href="/orders/${e(t.orderId)}">Order #${e(t.orderId)}</a></h2>${details(t)}`).join("")}<p><a href="/pending">View pending orders →</a></p>`,
      env,
      201,
    );
  }
  return page(
    "Not found",
    '<p><a href="/">Return to accounts</a></p>',
    env,
    404,
  );
}
export default {
  async fetch(request: Request, env: ServiceEnv): Promise<Response> {
    let response: Response;
    try {
      response = await handle(request, env);
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : "The request could not be completed.";
      // Deliberately exclude request paths, bodies, bank responses and credentials from logs.
      console.error(
        JSON.stringify({
          event: "request_failed",
          errorType: error instanceof Error ? error.name : "Unknown",
        }),
      );
      response = page(
        "Could not complete request",
        `<p>${e(message)}</p><p><a href="/pending">Check pending orders</a> · <a href="/">Return to accounts</a></p>`,
        env,
        400,
      );
    }
    response.headers.set("Cache-Control", "no-store");
    response.headers.set(
      "Content-Security-Policy",
      "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
    );
    response.headers.set("X-Content-Type-Options", "nosniff");
    response.headers.set("Referrer-Policy", "same-origin");
    response.headers.set(
      "Permissions-Policy",
      "camera=(), microphone=(), geolocation=()",
    );
    response.headers.set("Strict-Transport-Security", "max-age=31536000");
    return response;
  },
};
