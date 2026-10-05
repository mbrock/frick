import { createService } from "./service.ts";
const env = {
  MODE: process.env.MODE ?? "live",
  SERVICE_TOKEN: process.env.SERVICE_TOKEN ?? "",
  RELAY_URL: process.env.RELAY_URL,
  RELAY_TOKEN: process.env.RELAY_TOKEN,
};
if (
  !env.SERVICE_TOKEN ||
  (env.MODE !== "demo" && (!env.RELAY_URL || !env.RELAY_TOKEN))
)
  throw new Error("Missing service configuration");
if (!process.env.FRICK_OAUTH_STATE)
  throw new Error("OAuth state directory is required");
const service = createService(
  env,
  process.env.PUBLIC_ORIGIN ?? "https://less.rest",
  process.env.FRICK_OAUTH_STATE,
);
const server = service.app.listen(
  Number(process.env.PORT ?? 8788),
  "127.0.0.1",
  () => console.log("Frick OAuth MCP ready on loopback"),
);
server.requestTimeout = 120000;
for (const signal of ["SIGTERM", "SIGINT"] as const)
  process.on(signal, () =>
    server.close(() => {
      void service.close().then(() => process.exit(0));
    }),
  );
