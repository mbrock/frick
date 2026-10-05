const encoder = new TextEncoder();
export const bytes = (value: string) => encoder.encode(value);
export const base64 = (value: ArrayBuffer) =>
  btoa(String.fromCharCode(...new Uint8Array(value)));
export function unbase64(value: string): Uint8Array<ArrayBuffer> {
  return Uint8Array.from(
    atob(value.replace(/-/g, "+").replace(/_/g, "/")),
    (c) => c.charCodeAt(0),
  );
}
const url64 = (value: ArrayBuffer) =>
  base64(value).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
async function hmacKey(secret: string) {
  return crypto.subtle.importKey(
    "raw",
    bytes(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign", "verify"],
  );
}
export async function sameSecret(actual: string, expected: string) {
  const key = await hmacKey(expected);
  const signature = await crypto.subtle.sign("HMAC", key, bytes(expected));
  return crypto.subtle.verify("HMAC", key, signature, bytes(actual));
}
export async function seal(value: unknown, secret: string, purpose: string) {
  const payload = url64(
    bytes(JSON.stringify({ value, expires: Date.now() + 30 * 60 * 1000 }))
      .buffer,
  );
  const signature = await crypto.subtle.sign(
    "HMAC",
    await hmacKey(secret),
    bytes(`${purpose}:${payload}`),
  );
  return `${payload}.${url64(signature)}`;
}
export async function unseal<T>(
  token: string,
  secret: string,
  purpose: string,
): Promise<T> {
  const [payload, signature, extra] = token.split(".");
  if (!payload || !signature || extra)
    throw new Error("Invalid or expired review. Start again.");
  try {
    if (
      !(await crypto.subtle.verify(
        "HMAC",
        await hmacKey(secret),
        unbase64(signature),
        bytes(`${purpose}:${payload}`),
      ))
    )
      throw new Error();
    const data = JSON.parse(new TextDecoder().decode(unbase64(payload)));
    if (!Number.isFinite(data.expires) || data.expires < Date.now())
      throw new Error();
    return data.value as T;
  } catch {
    throw new Error("Invalid or expired review. Start again.");
  }
}
export function escape(value: unknown): string {
  return String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ]!,
  );
}
export async function limitedText(
  response: Request | Response,
  limit: number,
): Promise<string> {
  const reader = response.body?.getReader();
  if (!reader) return "";
  const chunks: Uint8Array[] = [];
  let size = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > limit) {
      await reader.cancel();
      throw new Error("Response or request exceeds size limit.");
    }
    chunks.push(value);
  }
  const all = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    all.set(chunk, offset);
    offset += chunk.length;
  }
  return new TextDecoder().decode(all);
}
