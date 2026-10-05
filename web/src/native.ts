// Native deployment on Igloo. The browser interface is the same HTML handler.
import app from "./index";
import type { ServiceEnv } from "./env";

declare const Bun: {
  serve(options: {
    hostname: string;
    port: number;
    maxRequestBodySize: number;
    idleTimeout: number;
    fetch(request: Request): Promise<Response>;
  }): unknown;
};
const prefix = "/frick";
const origin = "https://less.rest";
const env: ServiceEnv = {
  MODE: process.env.MODE ?? "live",
  SERVICE_TOKEN: process.env.SERVICE_TOKEN ?? "",
  RELAY_URL: process.env.RELAY_URL,
  RELAY_TOKEN: process.env.RELAY_TOKEN,
};
if (
  !env.SERVICE_TOKEN ||
  (env.MODE !== "demo" && (!env.RELAY_URL || !env.RELAY_TOKEN))
)
  throw new Error("Required service credentials are missing");
Bun.serve({
  hostname: "127.0.0.1",
  port: Number(process.env.PORT ?? 8787),
  maxRequestBodySize: 16384,
  idleTimeout: 120,
  async fetch(request) {
    const incoming = new URL(request.url);
    if (
      incoming.pathname !== prefix &&
      !incoming.pathname.startsWith(prefix + "/")
    )
      return new Response("Not found", { status: 404 });
    if (incoming.pathname === prefix)
      return new Response(null, {
        status: 308,
        headers: { Location: prefix + "/" },
      });
    const url = new URL(
      incoming.pathname.slice(prefix.length) + incoming.search,
      origin,
    );
    const response = await app.fetch(new Request(url, request), env);
    if (response.headers.get("Content-Type")?.startsWith("text/html")) {
      const html = (await response.text()).replace(
        /\b(href|action)="(\/[^\"]*)"/g,
        (_, attribute, path) => `${attribute}="${prefix}${path}"`,
      );
      return new Response(html, {
        status: response.status,
        headers: response.headers,
      });
    }
    return response;
  },
});
console.log("Frick HTML service ready on loopback");
