import { limitedText } from "./security";
import type { ServiceEnv } from "./env";
export interface Account {
  account: string;
  customer: string;
  iban: string;
  currency: string;
  balance: number;
  available: number;
}
export interface Party {
  name?: string;
  iban?: string;
}
export interface Transaction {
  orderId: number;
  transactionNr?: string;
  customId: string;
  amount: number;
  currency: string;
  state: string;
  reference?: string;
  bookingDate?: string;
  valuta?: string;
  direction?: string;
  creditor: Party;
  debitor: Party;
}
export interface Page<T> {
  moreResults?: boolean;
  resultSetSize?: number;
  transactions?: T[];
  accounts?: Account[];
}
export interface Payment {
  customId: string;
  type: "SEPA";
  amount: number;
  currency: "EUR";
  reference: string;
  debitor: { iban: string };
  creditor: { name: string; iban: string };
}
export class Bank {
  constructor(private env: ServiceEnv) {}
  private async request<T>(
    path: string,
    method = "GET",
    data?: unknown,
  ): Promise<T> {
    if (!this.env.RELAY_URL || !this.env.RELAY_TOKEN)
      throw new Error("The private banking relay is not configured.");
    const response = await fetch(this.env.RELAY_URL + path, {
      method,
      headers: {
        Accept: "application/json",
        Authorization: `Bearer ${this.env.RELAY_TOKEN}`,
        ...(data === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: data === undefined ? undefined : JSON.stringify(data),
      redirect: "manual",
      signal: AbortSignal.timeout(45000),
    });
    if (!response.ok)
      throw new Error(
        `Banking relay unavailable (HTTP ${response.status}). Check pending orders before retrying creation.`,
      );
    return JSON.parse(await limitedText(response, 4 * 1024 * 1024)) as T;
  }
  async accounts(): Promise<Account[]> {
    if (this.env.MODE === "demo")
      return [
        {
          account: "100/001",
          customer: "100 Example Owner",
          iban: "DE89370400440532013000",
          currency: "EUR",
          balance: 12500,
          available: 12500,
        },
      ];
    const result = await this.request<Page<Transaction>>("/accounts");
    if (result.moreResults) throw new Error("Account list is incomplete.");
    return result.accounts ?? [];
  }
  async transactions(
    account: Account,
    query: URLSearchParams,
  ): Promise<Page<Transaction>> {
    if (this.env.MODE === "demo")
      return {
        transactions:
          query.get("status") !== "BOOKED"
            ? []
            : [
                {
                  orderId: 42,
                  customId: "demo-42",
                  amount: 75,
                  currency: "EUR",
                  state: "BOOKED",
                  reference: "Example invoice",
                  bookingDate: "2026-10-02",
                  direction: "DEBIT",
                  creditor: {
                    name: "Example recipient",
                    iban: "DE12500105170648489890",
                  },
                  debitor: { iban: account.iban },
                },
              ],
        moreResults: false,
      };
    const parameters = new URLSearchParams({
      account: account.iban,
      offset: query.get("firstPosition") ?? "0",
      limit: query.get("maxResults") ?? "100",
    });
    if (query.get("status")) parameters.set("status", query.get("status")!);
    return this.request("/transactions?" + parameters);
  }
  async order(id: string): Promise<Transaction> {
    if (this.env.MODE === "demo")
      return {
        orderId: Number(id),
        customId: "demo",
        amount: 75,
        currency: "EUR",
        state: "PREPARED",
        reference: "Example payment",
        creditor: { name: "Example recipient", iban: "DE12500105170648489890" },
        debitor: { iban: "DE89370400440532013000" },
      };
    const result = await this.request<Page<Transaction>>(
      "/orders/" + encodeURIComponent(id),
    );
    if (!result.transactions?.[0]) throw new Error("Order not found.");
    return result.transactions[0];
  }
  async create(payment: Payment): Promise<Transaction[]> {
    if (this.env.MODE === "demo")
      return [{ ...payment, orderId: 12345, state: "PREPARED" }];
    return (
      (await this.request<Page<Transaction>>("/payments", "POST", payment))
        .transactions ?? []
    );
  }
}
export function iban(raw: string): string {
  const value = raw.replace(/\s/g, "").toUpperCase();
  if (!/^[A-Z]{2}\d{2}[A-Z0-9]{11,30}$/.test(value))
    throw new Error("Enter a valid IBAN.");
  const moved = value.slice(4) + value.slice(0, 4);
  let remainder = 0;
  for (const char of moved)
    for (const digit of /[A-Z]/.test(char)
      ? String(char.charCodeAt(0) - 55)
      : char)
      remainder = (remainder * 10 + Number(digit)) % 97;
  if (remainder !== 1) throw new Error("IBAN checksum is incorrect.");
  return value;
}
export function paymentFromForm(
  form: URLSearchParams,
  accounts: Account[],
): Payment {
  const from = iban(form.get("from") ?? "");
  if (
    !accounts.some(
      (a) =>
        a.iban.replace(/\s/g, "").toUpperCase() === from &&
        a.currency === "EUR",
    )
  )
    throw new Error("Select your EUR account.");
  const amount = (form.get("amount") ?? "").trim();
  if (!/^\d{1,9}(\.\d{1,2})?$/.test(amount) || Number(amount) <= 0)
    throw new Error(
      "Enter a positive EUR amount with at most two decimal places.",
    );
  const name = (form.get("name") ?? "").trim(),
    reference = (form.get("reference") ?? "").trim();
  if (!name || name.length > 140 || reference.length > 140)
    throw new Error(
      "Recipient name and reference must be at most 140 characters.",
    );
  const customId = form.get("customId") ?? "";
  if (!/^frick-worker-[a-f0-9-]{36}$/.test(customId))
    throw new Error("Start a new payment to obtain its reference.");
  return {
    customId,
    type: "SEPA",
    amount: Number(amount),
    currency: "EUR",
    reference,
    debitor: { iban: from },
    creditor: { name, iban: iban(form.get("to") ?? "") },
  };
}
