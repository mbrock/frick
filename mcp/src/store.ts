import { DatabaseSync } from "node:sqlite";
import type { AdapterPayload } from "oidc-provider";

// OAuth state is separate from banking credentials. Expiry and consumption are
// persisted so restarting the process cannot resurrect codes or refresh tokens.
export function sqliteAdapter(path: string) {
  const db = new DatabaseSync(path);
  db.exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
    CREATE TABLE IF NOT EXISTS oauth (model TEXT NOT NULL, id TEXT NOT NULL,
      payload TEXT NOT NULL, expires INTEGER, uid TEXT, userCode TEXT, grantId TEXT,
      PRIMARY KEY(model,id));
    CREATE INDEX IF NOT EXISTS oauth_grant ON oauth(grantId);
    CREATE INDEX IF NOT EXISTS oauth_uid ON oauth(model,uid);`);
  const now = () => Math.floor(Date.now() / 1000);
  class Adapter {
    constructor(readonly model: string) {}
    async upsert(id: string, payload: AdapterPayload, expiresIn?: number) {
      db.prepare(
        "DELETE FROM oauth WHERE expires IS NOT NULL AND expires <= ?",
      ).run(now());
      db.prepare("INSERT OR REPLACE INTO oauth VALUES (?,?,?,?,?,?,?)").run(
        this.model,
        id,
        JSON.stringify(payload),
        expiresIn === undefined ? null : now() + expiresIn,
        String(payload.uid ?? "") || null,
        String(payload.userCode ?? "") || null,
        String(payload.grantId ?? "") || null,
      );
    }
    async find(id: string): Promise<AdapterPayload | undefined> {
      return this.lookup("id", id);
    }
    private lookup(
      column: "id" | "uid" | "userCode",
      value: string,
    ): AdapterPayload | undefined {
      const row = db
        .prepare(
          `SELECT payload FROM oauth WHERE model=? AND ${column}=? AND (expires IS NULL OR expires>?)`,
        )
        .get(this.model, value, now());
      return row ? JSON.parse(String(row.payload)) : undefined;
    }
    async findByUid(uid: string) {
      return this.lookup("uid", uid);
    }
    async findByUserCode(code: string) {
      return this.lookup("userCode", code);
    }
    async consume(id: string) {
      const payload = await this.find(id);
      if (payload) {
        payload.consumed = now();
        db.prepare("UPDATE oauth SET payload=? WHERE model=? AND id=?").run(
          JSON.stringify(payload),
          this.model,
          id,
        );
      }
    }
    async destroy(id: string) {
      db.prepare("DELETE FROM oauth WHERE model=? AND id=?").run(
        this.model,
        id,
      );
    }
    async revokeByGrantId(grantId: string) {
      db.prepare(
        "DELETE FROM oauth WHERE grantId=? OR (model='Grant' AND id=?)",
      ).run(grantId, grantId);
    }
  }
  return { Adapter, close: () => db.close() };
}
