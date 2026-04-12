import type { MemStationTarget, TargetKind } from '$lib/types';

export interface ParsedTargetLocator {
  target: Partial<MemStationTarget>;
  adminKey?: string;
}

const validKinds: TargetKind[] = ['single', 'cluster-entry', 'node'];

function trimTrailingSlash(value: string): string {
  return value.trim().replace(/\/$/, '');
}

function isTruthy(value: string | null): boolean {
  if (!value) {
    return false;
  }
  return ['1', 'true', 'yes', 'on'].includes(value.trim().toLowerCase());
}

function defaultLabel(hostname: string, port: string): string {
  if (hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '::1') {
    return 'Local Dev';
  }
  return port ? `${hostname}:${port}` : hostname;
}

function normalizeKind(value: string | null): TargetKind | undefined {
  if (!value) {
    return undefined;
  }
  return validKinds.includes(value as TargetKind) ? (value as TargetKind) : undefined;
}

function inferredHTTPURL(raw: string): URL {
  if (/^[a-z]+:\/\//i.test(raw)) {
    return new URL(raw);
  }
  return new URL(`http://${raw}`);
}

function resolveBaseURL(value: URL): string {
  if (value.protocol === 'memstation:' || value.protocol === 'memstations:') {
    const secure = value.protocol === 'memstations:' || isTruthy(value.searchParams.get('secure')) || value.searchParams.get('scheme') === 'https';
    const protocol = secure ? 'https:' : 'http:';
    return trimTrailingSlash(`${protocol}//${value.host}${value.pathname === '/' ? '' : value.pathname}`);
  }
  if (value.protocol === 'http:' || value.protocol === 'https:') {
    return trimTrailingSlash(`${value.protocol}//${value.host}${value.pathname === '/' ? '' : value.pathname}`);
  }
  throw new Error('Connection URI must use http://, https://, memstation://, or memstations://');
}

export function parseTargetLocator(input: string): ParsedTargetLocator {
  const trimmed = input.trim();
  if (!trimmed) {
    throw new Error('Enter a base URL or MemStation connection URI.');
  }

  const value = inferredHTTPURL(trimmed);
  const baseUrl = resolveBaseURL(value);
  const label = value.searchParams.get('label')?.trim() || defaultLabel(value.hostname, value.port);
  const environment = value.searchParams.get('env')?.trim() || value.searchParams.get('environment')?.trim() || undefined;
  const clusterName = value.searchParams.get('cluster')?.trim() || undefined;
  const region = value.searchParams.get('region')?.trim() || undefined;
  const kind = normalizeKind(value.searchParams.get('kind'));
  const adminKey = value.searchParams.get('key')?.trim() || undefined;

  return {
    target: {
      label,
      baseUrl,
      environment,
      clusterName,
      region,
      kind
    },
    adminKey
  };
}
