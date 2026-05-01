function parseURL(value: string): URL | null {
  try {
    return new URL(value);
  } catch {
    return null;
  }
}

export function isHTTPURL(value: string): boolean {
  const parsed = parseURL(value.trim());
  return Boolean(parsed && (parsed.protocol === 'http:' || parsed.protocol === 'https:'));
}

export function isPostgresURL(value: string): boolean {
  const parsed = parseURL(value.trim());
  return Boolean(parsed && (parsed.protocol === 'postgres:' || parsed.protocol === 'postgresql:'));
}

export function isNonNegativeInteger(value: number): boolean {
  return Number.isInteger(value) && value >= 0;
}

export function isPositiveInteger(value: number): boolean {
  return Number.isInteger(value) && value > 0;
}

export function isNumberInRange(value: number, min: number, max: number): boolean {
  return Number.isFinite(value) && value >= min && value <= max;
}

export function parseLineList(value: string): string[] {
  return value
    .split(/\n|,/)
    .map((item) => item.trim())
    .filter(Boolean);
}

export function isOriginValue(value: string): boolean {
  const parsed = parseURL(value);
  return Boolean(parsed && (parsed.protocol === 'http:' || parsed.protocol === 'https:') && parsed.origin === value);
}

export function isMimeType(value: string): boolean {
  return /^[a-z0-9!#$&^_.+-]+\/[a-z0-9!#$&^_.+-]+$/i.test(value.trim());
}
