import { describe, expect, it } from 'vitest';

import { parseTargetLocator } from '$lib/targets/uri';

describe('parseTargetLocator', () => {
  it('parses memstation URIs into http targets and one-time admin credentials', () => {
    const parsed = parseTargetLocator(
      'memstation://127.0.0.1:8080?key=$memstation-admin-argon2id$v=19$m=65536,t=1,p=4$salt$hash&label=Fixture%20Target'
    );

    expect(parsed).toEqual({
      target: {
        baseUrl: 'http://127.0.0.1:8080',
        label: 'Fixture Target'
      },
      adminKey: '$memstation-admin-argon2id$v=19$m=65536,t=1,p=4$salt$hash'
    });
  });

  it('supports secure memstations URIs and falls back to generated labels', () => {
    const parsed = parseTargetLocator('memstations://memstation.example.com:9443/control?environment=production');

    expect(parsed.target).toEqual({
      baseUrl: 'https://memstation.example.com:9443/control',
      label: 'memstation.example.com:9443'
    });
    expect(parsed.adminKey).toBeUndefined();
  });

  it('accepts plain http base URLs', () => {
    const parsed = parseTargetLocator('https://memstation.example.com');

    expect(parsed.target.baseUrl).toBe('https://memstation.example.com');
    expect(parsed.target.label).toBe('memstation.example.com');
  });

  it('rejects raw admin tokens in connection URI key parameters', () => {
    expect(() => parseTargetLocator('memstation://127.0.0.1:8080?key=admin-token')).toThrow(
      'Connection URI key= must contain a MemStation admin token hash, not a raw admin token.'
    );
  });

  it('rejects unsupported schemes', () => {
    expect(() => parseTargetLocator('ftp://memstation.example.com')).toThrow(
      'Connection URI must use http://, https://, memstation://, or memstations://'
    );
  });
});
