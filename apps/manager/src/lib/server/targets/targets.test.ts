import { createServer } from 'node:http';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { allowedProxyPath, privateAddress, publicAddress, publicTarget, targetRegistry, targetSessionBinding } from './registry.ts';
import { createSessionStore, MemorySessionKV, TargetSessions } from './sessions.ts';
import { boundedBody, forward } from './transport.ts';
import { requireSameOrigin } from './handlers.ts';

afterEach(() => vi.useRealTimers());

describe('target registry and forwarding policy', () => {
  it('accepts only explicit server targets and excludes private policy from public data', () => {
    const [target] = targetRegistry({ MEMSTATION_MANAGER_TARGETS: JSON.stringify([{ id: 'one', baseUrl: 'https://memory.example/base/', addresses: ['10.0.0.4'] }]) });
    expect(target.baseUrl).toBe('https://memory.example/base');
    expect(targetSessionBinding(target)).not.toBe(targetSessionBinding({ ...target, baseUrl: 'https://another.example' }));
    expect(publicTarget(target)).not.toHaveProperty('addresses');
    for (const baseUrl of ['file:///etc/passwd', 'http://user:pass@localhost', 'https://host/?key=secret', 'http://host/%2e%2e']) {
      expect(() => targetRegistry({ MEMSTATION_MANAGER_TARGETS: JSON.stringify([{ baseUrl }]) })).toThrow();
    }
    expect(() => targetRegistry({ MEMSTATION_MANAGER_TARGETS: '[{"id":"same","baseUrl":"http://a"},{"id":"same","baseUrl":"http://b"}]' })).toThrow();
  });
  it('rejects cross-origin mutations and ambiguous or unbounded forwarding paths', () => {
    const url = new URL('https://manager.example/manager/session/one');
    expect(() => requireSameOrigin({ url, request: new Request(url, { method: 'POST', headers: { origin: 'https://attacker.example' } }) })).toThrow();
    expect(() => requireSameOrigin({ url, request: new Request(url, { method: 'POST', headers: { origin: url.origin } }) })).not.toThrow();
    for (const path of ['//evil/a', '/api/admin/../login', '/api/admin/%2e%2e/login', '/api/admin/%252e%252e/login', '/api/admin/login', '/api/admin/%6cogin', '/v1/unknown', '/__test/reset']) {
      expect(allowedProxyPath(path, 'POST')).toBeNull();
    }
    expect(allowedProxyPath('/api/admin/users?user_id=alpha', 'GET')).toBe('admin');
    expect(allowedProxyPath('/graphql', 'POST')).toBe('admin');
    expect(allowedProxyPath('/graphql', 'GET')).toBeNull();
    expect(allowedProxyPath('/v1/responses', 'POST')).toBe('gateway');
    for (const address of ['127.0.0.1', '169.254.169.254', '10.1.2.3', '::1', '::ffff:127.0.0.1', 'fc00::1', '2001:db8::1']) expect(publicAddress(address)).toBe(false);
    for (const address of ['2001::1', '2001:0000:4136:e378:8000:63bf:3fff:fdd2', '2001:0002::1', '2001:0db8::1', '2001:0010::1', '2001:001f::1', '2001:0020::1', '2001:002f::1', '2002::1']) expect(publicAddress(address), address).toBe(false);
    expect(publicAddress('8.8.8.8')).toBe(true);
    expect(publicAddress('2001:4860:4860::8888')).toBe(true);
    expect(privateAddress('172.18.0.2')).toBe(true);
    expect(privateAddress('169.254.169.254')).toBe(false);
  });
  it('pins the destination, strips upstream cookies and streams before completion', async () => {
    let headers: Record<string, unknown> = {};
    const server = createServer((req, res) => {
      headers = req.headers;
      if (req.url === '/redirect') { res.writeHead(302, { location: 'http://169.254.169.254/' }).end(); return; }
      res.writeHead(200, { 'content-type': 'text/event-stream', 'set-cookie': 'memstation_admin_session=secret; HttpOnly' });
      res.write('data: first\n\n');
      const timer = setTimeout(() => res.end('data: last\n\n'), 500);
      res.on('close', () => clearTimeout(timer));
    }).listen(0, '127.0.0.1');
    await once(server, 'listening');
    const address = server.address();
    if (!address || typeof address === 'string') throw new Error('Missing test address');
    const [target] = targetRegistry({ MEMSTATION_MANAGER_TARGETS: JSON.stringify([{ baseUrl: `http://127.0.0.1:${address.port}` }]) });
    try {
      const { response, cookies } = await forward(target, '/stream', { method: 'GET', headers: { cookie: 'memstation_admin_session=bound' } });
      expect(headers.cookie).toBe('memstation_admin_session=bound');
      expect(response.headers.get('set-cookie')).toBeNull();
      expect(response.headers.get('content-security-policy')).toContain('sandbox');
      expect(cookies).toHaveLength(1);
      const reader = response.body!.getReader();
      expect(new TextDecoder().decode((await reader.read()).value)).toBe('data: first\n\n');
      await reader.cancel();
      const timed = await forward(target, '/stream', { method: 'GET', timeout: 20 });
      await expect(timed.response.text()).rejects.toThrow();
      await expect(forward(target, '/redirect', { method: 'GET' })).rejects.toThrow('redirects');
      await expect(forward({ ...target, addresses: ['10.0.0.4'] }, '/', { method: 'GET' })).rejects.toThrow('policy');
      await expect(forward(target, '/stream', { method: 'GET', signal: AbortSignal.abort() })).rejects.toThrow();
    } finally { server.closeAllConnections(); server.close(); }
  });
  it('bounds uploads before forwarding', async () => {
    await expect(boundedBody(new Request('https://manager', { method: 'POST', body: 'too long' }), 3)).rejects.toThrow('too large');
    expect(Buffer.from(await boundedBody(new Request('https://manager', { method: 'POST', body: 'ok' }), 3) ?? []).toString()).toBe('ok');
  });
  it('forwards DELETE preconditions with an exact byte length', async () => {
    const payload = Buffer.from(JSON.stringify({ expected_updated_at: '2026-10-24T12:00:00.123456Z', note: '版本' }));
    const server = createServer(async (req, res) => {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ method: req.method, length: req.headers['content-length'], body: Buffer.concat(chunks).toString() }));
    }).listen(0, '127.0.0.1');
    await once(server, 'listening');
    const address = server.address();
    if (!address || typeof address === 'string') throw new Error('Missing test address');
    const [target] = targetRegistry({ MEMSTATION_MANAGER_TARGETS: JSON.stringify([{ baseUrl: `http://127.0.0.1:${address.port}` }]) });
    try {
      const { response } = await forward(target, '/delete', { method: 'DELETE', body: payload, headers: { 'content-type': 'application/json', 'content-length': '1' } });
      expect(await response.json()).toEqual({ method: 'DELETE', length: String(payload.byteLength), body: payload.toString() });
    } finally { server.closeAllConnections(); server.close(); }
  });
});

describe('encrypted server sessions', () => {
  it('isolates browsers and targets and does not remove a new login on a late 401', async () => {
    const kv = new MemorySessionKV();
    const put = vi.spyOn(kv, 'set');
    const sessions = new TargetSessions(kv, randomBytes(32));
    const a = await sessions.create(), b = await sessions.create();
    await sessions.save(a, 'one', 'memstation_admin_session=secret');
    const old = await sessions.connection(a, 'one');
    expect(old?.cookie).toContain('secret');
    expect(put.mock.calls.at(-1)?.[1]).not.toContain('secret');
    expect(await sessions.connection(b, 'one')).toBeNull();
    expect(await sessions.connection(a, 'two')).toBeNull();
    await sessions.save(a, 'one', 'memstation_admin_session=new');
    await sessions.disconnect(a, 'one', old!.sealed);
    expect((await sessions.connection(a, 'one'))?.cookie).toContain('=new');
    const [key, sealed] = put.mock.calls.at(-1)!;
    await kv.set(key.replace(/:one$/, ':two'), sealed, 100);
    expect(await sessions.connection(a, 'two')).toBeNull();
    await sessions.save(a, 'session', 'memstation_admin_session=reserved-name');
    expect(await sessions.exists(a)).toBe(true);
    expect((await sessions.connection(a, 'session'))?.cookie).toContain('reserved-name');
    await sessions.disconnect(a, 'one');
    expect(await sessions.connection(a, 'one')).toBeNull();
  });
  it('expires connections and requires production storage/encryption configuration', async () => {
    vi.useFakeTimers();
    const sessions = new TargetSessions(new MemorySessionKV(), randomBytes(32));
    const id = await sessions.create();
    await sessions.save(id, 'one', 'cookie', 1);
    vi.advanceTimersByTime(1001);
    expect(await sessions.connection(id, 'one')).toBeNull();
    await expect(createSessionStore({ NODE_ENV: 'production' })).rejects.toThrow('Redis');
    await expect(createSessionStore({ MEMSTATION_MANAGER_REDIS_URL: 'redis://localhost', MEMSTATION_MANAGER_SESSION_KEY: 'bad' })).rejects.toThrow('64 hex');
  });
  it('rate limits repeated login attempts across browser sessions and expires the limit', async () => {
    vi.useFakeTimers();
    const sessions = new TargetSessions(new MemorySessionKV(), randomBytes(32));
    expect((await Promise.all(Array.from({ length: 60 }, () => sessions.allowLogin('address', 'one')))).every(Boolean)).toBe(true);
    expect(await sessions.allowLogin('address', 'one')).toBe(false);
    expect(await sessions.allowLogin('address', 'two')).toBe(true);
    vi.advanceTimersByTime(60_001);
    expect(await sessions.allowLogin('address', 'one')).toBe(true);
  });
  it.runIf(Boolean(process.env.MEMSTATION_TEST_MANAGER_REDIS_URL))('shares encrypted sessions across Redis-backed instances', async () => {
    const env = { NODE_ENV: 'production', MEMSTATION_MANAGER_REDIS_URL: process.env.MEMSTATION_TEST_MANAGER_REDIS_URL, MEMSTATION_MANAGER_SESSION_KEY: randomBytes(32).toString('hex') };
    const first = await createSessionStore(env), second = await createSessionStore(env);
    try {
      const id = await first.create();
      await first.save(id, 'target', 'memstation_admin_session=integration');
      expect(await second.exists(id)).toBe(true);
      const old = await second.connection(id, 'target');
      expect(old?.cookie).toContain('integration');
      await first.save(id, 'target', 'memstation_admin_session=new');
      await second.disconnect(id, 'target', old!.sealed);
      expect((await first.connection(id, 'target'))?.cookie).toContain('=new');
      await second.disconnect(id, 'target');
      expect(await first.connection(id, 'target')).toBeNull();
    } finally { first.close(); second.close(); }
  });
});
