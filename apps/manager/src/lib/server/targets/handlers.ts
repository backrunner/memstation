import { error, json, type RequestEvent } from '@sveltejs/kit';
import { allowedProxyPath, targetRegistry, targetSessionBinding } from './registry.ts';
import { ensureManagerSession } from './sessions.ts';
import { boundedBody, forward } from './transport.ts';

function requestTarget(event: RequestEvent) {
  const target = targetRegistry(process.env).find((item) => item.id === event.params.target);
  if (!target) error(404, 'Target is not registered in this Manager.');
  return target;
}

export function requireSameOrigin(event: Pick<RequestEvent, 'request' | 'url'>) {
  const origin = event.request.headers.get('origin');
  const site = event.request.headers.get('sec-fetch-site');
  if (site && !['same-origin', 'none'].includes(site)) error(403, 'Cross-origin Manager request rejected.');
  if (!['GET', 'HEAD'].includes(event.request.method) && origin !== event.url.origin) error(403, 'Same-origin Manager request required.');
}

export async function connection(event: RequestEvent): Promise<Response> {
  requireSameOrigin(event);
  const target = requestTarget(event);
  const binding = targetSessionBinding(target);
  const { id, store } = await ensureManagerSession(event.cookies, event.url);
  const old = await store.connection(id, binding);
  const method = event.request.method;
  if (method === 'DELETE') {
    if (old) {
      await store.disconnect(id, binding, old.sealed);
      try {
        const { response } = await forward(target, '/api/admin/logout', { method: 'POST', headers: { cookie: old.cookie }, timeout: 3_000 });
        await response.body?.cancel();
      } catch { /* Local logout succeeds even when the target is offline. */ }
    }
    return json({ authenticated: false }, { headers: { 'cache-control': 'no-store' } });
  }
  if (method === 'GET' && !old) return json({ authenticated: false }, { headers: { 'cache-control': 'no-store' } });
  let body: Uint8Array | undefined;
  if (method === 'POST') {
    if (!await store.allowLogin(event.getClientAddress(), target.id)) return json({ message: 'Too many connection attempts. Retry in a minute.' }, { status: 429, headers: { 'retry-after': '60', 'cache-control': 'no-store' } });
    try {
      const input: unknown = JSON.parse(Buffer.from(await boundedBody(event.request, 16_384) ?? []).toString('utf8'));
      if (!input || typeof input !== 'object' || !('token' in input) || typeof input.token !== 'string' || !input.token.trim()) error(400, 'Admin token is required.');
      body = Buffer.from(JSON.stringify({ token: input.token.trim() }));
    } catch { error(400, 'Invalid connection request.'); }
  }
  let upstream;
  try {
    upstream = await forward(target, method === 'POST' ? '/api/admin/login' : '/api/admin/session', {
      method, body, headers: method === 'POST' ? { 'content-type': 'application/json' } : { cookie: old!.cookie },
      signal: event.request.signal, timeout: 15_000
    });
  } catch {
    if (event.request.signal.aborted) return new Response(null, { status: 499 });
    error(502, 'Cannot reach this target. Check its address and connection policy.');
  }
  const { response, cookies } = upstream;
  let payload: { authenticated?: boolean } | null;
  try { payload = JSON.parse(Buffer.from(await boundedBody(response, 16_384) ?? []).toString('utf8')); }
  catch { error(502, 'Target returned an invalid session response.'); }
  if (!response.ok || payload?.authenticated !== true) {
    if (old && (response.status === 401 || response.status === 403 || response.ok)) await store.disconnect(id, binding, old.sealed);
    if (method === 'POST') error(response.status >= 500 ? 502 : 401, 'Admin token was rejected by the target.');
    if (response.status >= 500) error(502, 'Target session check failed. Retry the connection.');
    return json({ authenticated: false }, { headers: { 'cache-control': 'no-store' } });
  }
  if (method === 'POST') {
    const session = cookies.find((value) => value.startsWith('memstation_admin_session='));
    const cookie = session?.split(';')[0];
    if (!cookie || !/^memstation_admin_session=[A-Za-z0-9_.~-]+$/.test(cookie)) error(502, 'Target did not issue a compatible admin session.');
    const age = session?.match(/(?:^|;)\s*Max-Age=(\d+)/i)?.[1];
    await store.save(id, binding, cookie, age ? Number(age) : undefined);
  }
  return json({ authenticated: true, checkedAt: new Date().toISOString() }, { headers: { 'cache-control': 'no-store' } });
}

export async function proxy(event: RequestEvent): Promise<Response> {
  requireSameOrigin(event);
  const target = requestTarget(event);
  const binding = targetSessionBinding(target);
  const path = `/${event.params.path ?? ''}${event.url.search}`;
  const kind = allowedProxyPath(path, event.request.method);
  if (!kind) error(404, 'Unsupported Manager endpoint.');
  const { id, store } = await ensureManagerSession(event.cookies, event.url);
  const session = await store.connection(id, binding);
  if (!session && kind !== 'health') error(401, 'Reconnect this target.');
  const headers: Record<string, string> = {};
  for (const key of ['accept', 'content-type', 'range', 'idempotency-key']) {
    const value = event.request.headers.get(key); if (value) headers[key] = value;
  }
  if (kind === 'admin') headers.cookie = session!.cookie;
  if (kind === 'gateway') {
    for (const [key, value] of event.request.headers) if (key === 'authorization' || key.startsWith('x-memstation-')) headers[key] = value;
  }
  let body;
  try { body = await boundedBody(event.request, 32 * 1024 * 1024); } catch { error(413, 'Request body exceeds the Manager upload limit.'); }
  let response;
  try { ({ response } = await forward(target, path, { method: event.request.method, headers, body, signal: event.request.signal })); }
  catch {
    if (event.request.signal.aborted) return new Response(null, { status: 499 });
    error(502, 'Target request failed. Check the connection and retry.');
  }
  if (kind === 'admin' && response.status === 401 && session) await store.disconnect(id, binding, session.sealed);
  return response;
}
