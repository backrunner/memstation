import { createHash } from 'node:crypto';
import { isIP } from 'node:net';
import type { MemStationTarget } from '#lib/types.ts';

export interface RegisteredTarget extends MemStationTarget { addresses?: string[]; allowPrivateNetwork?: boolean }

export function targetRegistry(env: Record<string, string | undefined>): RegisteredTarget[] {
  const configured: unknown = env.MEMSTATION_MANAGER_TARGETS
    ? JSON.parse(env.MEMSTATION_MANAGER_TARGETS)
    : (env.MEMSTATION_MANAGER_DEV_PROXY_TARGET || env.PUBLIC_MEMSTATION_DEFAULT_TARGET)
      ? [{ baseUrl: env.MEMSTATION_MANAGER_DEV_PROXY_TARGET || env.PUBLIC_MEMSTATION_DEFAULT_TARGET, label: 'Local Dev' }]
      : [];
  if (!Array.isArray(configured) || configured.length > 50) throw new Error('Invalid Manager target configuration');
  const targets = configured.map((item: unknown): RegisteredTarget => {
    if (!item || typeof item !== 'object') throw new Error('Invalid Manager target configuration');
    const input = item as Record<string, unknown>;
    if (typeof input.baseUrl !== 'string' || /[%\\\r\n]/.test(input.baseUrl)) throw new Error('Invalid Manager target URL');
    const url = new URL(String(input.baseUrl));
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || /%|\\/.test(url.pathname)) {
      throw new Error('Manager target must be an HTTP(S) base URL without credentials, query or fragment');
    }
    const baseUrl = url.href.replace(/\/+$/, '');
    const id = typeof input.id === 'string' ? input.id : `target-${createHash('sha256').update(baseUrl).digest('hex').slice(0, 24)}`;
    if (!/^[a-zA-Z0-9_-]{1,80}$/.test(id)) throw new Error('Invalid Manager target ID');
    let addresses: string[] | undefined;
    if (input.addresses !== undefined) {
      if (!Array.isArray(input.addresses) || !input.addresses.length || !input.addresses.every((v) => typeof v === 'string' && isIP(v))) {
        throw new Error('Target addresses must contain literal IP addresses');
      }
      addresses = input.addresses as string[];
    }
    return { id, label: typeof input.label === 'string' ? input.label.slice(0, 120) : url.host, baseUrl,
      environment: 'custom', clusterName: 'standalone', region: 'global', kind: 'single', addresses, allowPrivateNetwork: input.allowPrivateNetwork === true };
  });
  if (new Set(targets.map((t) => t.id)).size !== targets.length) throw new Error('Duplicate Manager target ID');
  return targets;
}

export function targetSessionBinding(target: RegisteredTarget): string {
  return `${target.id}:${createHash('sha256').update(target.baseUrl).digest('hex')}`;
}

export function publicTarget(target: RegisteredTarget): MemStationTarget {
  return { id: target.id, label: target.label, baseUrl: target.baseUrl, environment: target.environment,
    clusterName: target.clusterName, region: target.region, kind: target.kind };
}

// Hostnames can resolve to public addresses by default. Private DNS destinations
// require explicit addresses in the server registry; literal URLs are explicit.
export function publicAddress(address: string): boolean {
  if (isIP(address) === 4) {
    const [a, b] = address.split('.').map(Number);
    return !(a === 0 || a === 10 || a === 127 || a >= 224 || (a === 100 && b >= 64 && b <= 127) ||
      (a === 169 && b === 254) || (a === 172 && b >= 16 && b <= 31) || (a === 192 && [0, 168].includes(b)) ||
      (a === 198 && [18, 19].includes(b)));
  }
  // Check numeric prefix words: compression and zero-padding must not bypass
  // exclusions for transition, benchmark, documentation and identifier ranges.
  if (isIP(address) !== 6) return false;
  const [network, subnet] = address.split(':').map(word => Number.parseInt(word || '0', 16));
  return network >= 0x2000 && network <= 0x3fff && network !== 0x2002 &&
    !(network === 0x2001 && (subnet === 0 || subnet === 2 || subnet === 0xdb8 || subnet >= 0x10 && subnet <= 0x2f));
}

export function privateAddress(address: string): boolean {
  if (isIP(address) === 4) {
    const [a, b] = address.split('.').map(Number);
    return a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168);
  }
  return isIP(address) === 6 && /^(fc|fd)/i.test(address);
}

export function allowedProxyPath(path: string, method: string): 'admin' | 'gateway' | 'health' | null {
  // No path decoding ambiguity, traversal, host override or backslash normalization.
  if (!path.startsWith('/') || path.startsWith('//') || /[\\\r\n#]/.test(path)) return null;
  let pathname = path.split('?')[0];
  let decoded: string;
  try { decoded = decodeURIComponent(pathname); } catch { return null; }
  if (decoded.includes('%') || decoded.includes('\\') || decoded.split('/').some((p) => p === '.' || p === '..')) return null;
  if (decoded !== pathname && /%2f|%5c|%2e/i.test(pathname)) return null;
  pathname = decoded;
  if (['/health/live', '/health/ready'].includes(pathname)) return method === 'GET' ? 'health' : null;
  if (pathname === '/graphql') return method === 'POST' ? 'admin' : null;
  if (/^\/api\/admin\/(login|logout|session)(\/|$)/.test(pathname)) return null;
  if (pathname.startsWith('/api/admin/') && ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) return 'admin';
  if (['/v1/chat/completions', '/v1/responses', '/v1/messages'].includes(pathname)) return method === 'POST' ? 'gateway' : null;
  if (/^\/v1\/(models|files)(\/|$)/.test(pathname) || /^\/api\/v1\/(memory|identity|workspaces)(\/|$)/.test(pathname)) {
    return ['GET', 'POST', 'PUT', 'DELETE'].includes(method) ? 'gateway' : null;
  }
  return null;
}
