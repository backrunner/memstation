import { createCipheriv, createDecipheriv, createHash, randomBytes } from 'node:crypto';
import { createClient } from '@redis/client';
import { error, type Cookies } from '@sveltejs/kit';

export const managerCookie = 'memstation_manager_session';
const ttl = 8 * 60 * 60;
export interface SessionKV {
  get(key: string): Promise<string | null>;
  set(key: string, value: string, seconds: number): Promise<void>;
  remove(key: string, expected?: string): Promise<void>;
  increment(key: string, seconds: number): Promise<number>;
  close?(): void;
}

export class MemorySessionKV implements SessionKV {
  private entries = new Map<string, { value: string; until: number }>();
  async get(key: string) {
    const item = this.entries.get(key);
    if (!item || item.until <= Date.now()) { this.entries.delete(key); return null; }
    return item.value;
  }
  async set(key: string, value: string, seconds: number) {
    for (const [id, item] of this.entries) if (item.until <= Date.now()) this.entries.delete(id);
    if (this.entries.size >= 10_000 && !this.entries.has(key)) throw new Error('Session capacity reached');
    this.entries.set(key, { value, until: Date.now() + seconds * 1000 });
  }
  async remove(key: string, expected?: string) {
    if (expected === undefined || this.entries.get(key)?.value === expected) this.entries.delete(key);
  }
  async increment(key: string, seconds: number) {
    const old = this.entries.get(key);
    const count = old && old.until > Date.now() ? Number(old.value) + 1 : 1;
    const until = count === 1 ? Date.now() + seconds * 1000 : old!.until;
    await this.set(key, String(count), Math.max(1, (until - Date.now()) / 1000));
    return count;
  }
}

export class TargetSessions {
  private closed = false;
  get available() { return !this.closed; }
  constructor(private kv: SessionKV, private secret: Buffer) {
    if (secret.length !== 32) throw new Error('Manager session encryption key must be 32 bytes');
  }
  private key(id: string, target?: string) {
    return `memstation:manager:v1:${createHash('sha256').update(id).digest('hex')}:${target ? `target:${target}` : 'session'}`;
  }
  async exists(id: string) { return Boolean(await this.kv.get(this.key(id))); }
  async allowLogin(address: string, target: string) {
    const key = `memstation:manager:login:${createHash('sha256').update(JSON.stringify([address, target])).digest('hex')}`;
    return await this.kv.increment(key, 60) <= 60;
  }
  async create() {
    const id = randomBytes(32).toString('base64url');
    await this.kv.set(this.key(id), '1', ttl);
    return id;
  }
  async save(id: string, target: string, cookie: string, seconds = ttl) {
    const key = this.key(id, target);
    const nonce = randomBytes(12);
    const cipher = createCipheriv('aes-256-gcm', this.secret, nonce);
    cipher.setAAD(Buffer.from(key));
    const ciphertext = Buffer.concat([cipher.update(cookie, 'utf8'), cipher.final()]);
    const sealed = Buffer.concat([nonce, cipher.getAuthTag(), ciphertext]).toString('base64url');
    await this.kv.set(key, sealed, Math.min(ttl, Math.max(1, seconds)));
  }
  async connection(id: string, target: string): Promise<{ cookie: string; sealed: string } | null> {
    const key = this.key(id, target);
    const sealed = await this.kv.get(key);
    if (!sealed) return null;
    try {
      const bytes = Buffer.from(sealed, 'base64url');
      const cipher = createDecipheriv('aes-256-gcm', this.secret, bytes.subarray(0, 12));
      cipher.setAuthTag(bytes.subarray(12, 28)); cipher.setAAD(Buffer.from(key));
      return { cookie: Buffer.concat([cipher.update(bytes.subarray(28)), cipher.final()]).toString('utf8'), sealed };
    } catch { await this.kv.remove(key, sealed); return null; }
  }
  async disconnect(id: string, target: string, expected?: string) { await this.kv.remove(this.key(id, target), expected); }
  close() { if (!this.closed) { this.closed = true; this.kv.close?.(); } }
}

let singleton: Promise<TargetSessions> | undefined;
process.once('sveltekit:shutdown', () => {
  void singleton?.then((store) => store.close()).catch(() => {});
});
export async function sessionStore(): Promise<TargetSessions> {
  singleton ??= createSessionStore().catch((error: unknown) => { singleton = undefined; throw error; });
  const store = await singleton;
  if (store.available) return store;
  singleton = undefined;
  return sessionStore();
}

export async function createSessionStore(env: Record<string, string | undefined> = process.env): Promise<TargetSessions> {
  const url = env.MEMSTATION_MANAGER_REDIS_URL;
  const key = env.MEMSTATION_MANAGER_SESSION_KEY;
  if (!url) {
    if (env.NODE_ENV === 'production') throw new Error('Production Manager requires Redis and a session encryption key');
    return new TargetSessions(new MemorySessionKV(), randomBytes(32));
  }
  if (!key || !/^[a-fA-F0-9]{64}$/.test(key)) throw new Error('MEMSTATION_MANAGER_SESSION_KEY must contain 64 hex characters');
  const client = createClient({ url, socket: { connectTimeout: 5_000, reconnectStrategy: (retries) => Math.min(100 * (retries + 1), 2_000) }, disableOfflineQueue: true, commandOptions: { timeout: 5_000 } });
  // Do not log connection URLs, cookies or raw Redis errors with credentials.
  client.on('error', () => {});
  let timeout: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([client.connect(), new Promise<never>((_, reject) => {
      timeout = setTimeout(() => reject(new Error('Session connection timed out')), 5_000);
    })]);
  } catch { client.destroy(); throw new Error('Manager session storage unavailable'); }
  finally { clearTimeout(timeout); }
  async function command<T>(operation: Promise<T>): Promise<T> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      return await Promise.race([operation, new Promise<never>((_, reject) => {
        timer = setTimeout(() => {
          // Redis command timeout covers queued commands only. Also bound replies
          // already in flight and drop the socket to avoid accumulating requests.
          sessions.close();
          reject(new Error('Manager session storage timed out'));
        }, 5_000);
      })]);
    } catch { error(503, 'Manager session storage unavailable. Retry shortly.'); }
    finally { clearTimeout(timer); }
  }
  const sessions = new TargetSessions({
    close: () => client.destroy(),
    get: (key) => command(client.get(key)),
    increment: async (key, seconds) => Number(await command(client.eval('local count=redis.call("INCR",KEYS[1]); if count == 1 then redis.call("EXPIRE",KEYS[1],ARGV[1]) end; return count', { keys: [key], arguments: [String(seconds)] }))),
    set: async (key, value, seconds) => { await command(client.set(key, value, { EX: seconds })); },
    remove: async (key, expected) => {
      if (expected === undefined) { await command(client.del(key)); return; }
      await command(client.eval('if redis.call("GET",KEYS[1]) == ARGV[1] then return redis.call("DEL",KEYS[1]) else return 0 end', { keys: [key], arguments: [expected] }));
    }
  }, Buffer.from(key, 'hex'));
  return sessions;
}

export async function ensureManagerSession(cookies: Cookies, url: URL): Promise<{ id: string; store: TargetSessions }> {
  const store = await sessionStore();
  let id = cookies.get(managerCookie);
  if (!id || !/^[\w-]{43}$/.test(id) || !await store.exists(id)) {
    id = await store.create();
    cookies.set(managerCookie, id, { path: '/', httpOnly: true, sameSite: 'strict', secure: url.protocol === 'https:' || process.env.NODE_ENV === 'production', maxAge: ttl });
  }
  return { id, store };
}
