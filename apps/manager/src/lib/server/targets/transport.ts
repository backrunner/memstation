import { lookup } from 'node:dns/promises';
import { request as httpRequest } from 'node:http';
import { request as httpsRequest } from 'node:https';
import { isIP } from 'node:net';
import { Readable } from 'node:stream';
import type { RegisteredTarget } from './registry.ts';
import { privateAddress, publicAddress } from './registry.ts';

export async function forward(target: RegisteredTarget, path: string, options: {
  method: string; headers?: Record<string, string>; body?: Uint8Array; signal?: AbortSignal; timeout?: number;
}): Promise<{ response: Response; cookies: string[] }> {
  const deadline = Date.now() + (options.timeout ?? 120_000);
  options.signal?.throwIfAborted();
  const url = new URL(target.baseUrl + path);
  const hostname = url.hostname.replace(/^\[|\]$/g, '');
  const resolved = isIP(hostname) ? [{ address: hostname, family: isIP(hostname) }] : await resolveAddresses(hostname, deadline, options.signal);
  const explicit = target.addresses ?? (hostname === 'localhost' ? ['127.0.0.1', '::1'] : isIP(hostname) ? [hostname] : undefined);
  if (!resolved.length || resolved.some(({ address }) => explicit ? !explicit.includes(address) : !(publicAddress(address) || target.allowPrivateNetwork && privateAddress(address)))) {
    throw new Error('Target DNS address is outside its configured policy');
  }
  const pinned = resolved[0];
  return new Promise((resolve, reject) => {
    const request = (url.protocol === 'https:' ? httpsRequest : httpRequest)(url, {
      method: options.method,
      // Node does not automatically frame DELETE bodies. Recompute this from
      // the bounded payload; never trust a browser-supplied Content-Length.
      headers: { ...options.headers, ...(options.body ? { 'content-length': String(options.body.byteLength) } : {}) },
      agent: false, family: pinned.family,
      // Pin the validated address for this connection, preserving Host and TLS SNI.
      lookup: (_hostname, _options, callback) => callback(null, pinned.address, pinned.family),
      signal: options.signal
    }, (incoming) => {
      const status = incoming.statusCode ?? 502;
      if (status >= 300 && status < 400) {
        incoming.destroy(); request.destroy(); reject(new Error('Target redirects are not allowed')); return;
      }
      const headers = new Headers();
      for (const key of Object.keys(incoming.headers).filter((name) => name.startsWith('x-memstation-') || ['content-type', 'content-encoding', 'content-disposition', 'content-range', 'accept-ranges', 'retry-after', 'x-request-id'].includes(name))) {
        const value = incoming.headers[key];
        if (typeof value === 'string') headers.set(key, value);
      }
      headers.set('cache-control', 'no-store');
      headers.set('x-content-type-options', 'nosniff');
      // Uploaded HTML/SVG and error pages must not gain the Manager's origin.
      headers.set('content-security-policy', "sandbox; default-src 'none'");
      resolve({ response: new Response([204, 205, 304].includes(status) ? null : Readable.toWeb(incoming) as ReadableStream<Uint8Array>, { status, headers }), cookies: incoming.headers['set-cookie'] ?? [] });
    });
    const timeout = setTimeout(() => request.destroy(new Error('Target request timed out')), Math.max(1, deadline - Date.now()));
    timeout.unref();
    request.on('close', () => clearTimeout(timeout));
    request.on('error', reject);
    request.end(options.body);
  });
}

export async function boundedBody(request: Pick<Request, 'body'>, limit: number): Promise<Uint8Array | undefined> {
  if (!request.body) return undefined;
  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let length = 0;
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      length += value.length;
      if (length > limit) { await reader.cancel(); throw new Error('Request body is too large'); }
      chunks.push(value);
    }
  } finally { reader.releaseLock(); }
  return Buffer.concat(chunks);
}

async function resolveAddresses(hostname: string, deadline: number, signal?: AbortSignal) {
  let timeout: ReturnType<typeof setTimeout> | undefined;
  let abort: (() => void) | undefined;
  try {
    return await Promise.race([
      lookup(hostname, { all: true }),
      new Promise<never>((_, reject) => {
        timeout = setTimeout(() => reject(new Error('Target DNS lookup timed out')), Math.max(1, deadline - Date.now()));
        timeout.unref();
        abort = () => reject(new Error('Target request aborted'));
        signal?.addEventListener('abort', abort, { once: true });
        if (signal?.aborted) abort();
      })
    ]);
  } finally {
    clearTimeout(timeout);
    if (abort) signal?.removeEventListener('abort', abort);
  }
}
