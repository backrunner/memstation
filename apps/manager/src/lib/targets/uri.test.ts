import { describe, expect, it } from 'vitest';

import { parseTargetLocator } from '$lib/targets/uri';

describe('parseTargetLocator', () => {
  it('parses memstation URIs into http targets and one-time admin keys', () => {
    const parsed = parseTargetLocator(
      'memstation://127.0.0.1:8080?key=admin-token&label=Fixture%20Target&env=e2e&cluster=standalone&region=local&kind=node'
    );

    expect(parsed).toEqual({
      target: {
        baseUrl: 'http://127.0.0.1:8080',
        label: 'Fixture Target',
        environment: 'e2e',
        clusterName: 'standalone',
        region: 'local',
        kind: 'node'
      },
      adminKey: 'admin-token'
    });
  });

  it('supports secure memstations URIs and falls back to generated labels', () => {
    const parsed = parseTargetLocator('memstations://memstation.example.com:9443/control?environment=production');

    expect(parsed.target).toEqual({
      baseUrl: 'https://memstation.example.com:9443/control',
      label: 'memstation.example.com:9443',
      environment: 'production',
      clusterName: undefined,
      region: undefined,
      kind: undefined
    });
    expect(parsed.adminKey).toBeUndefined();
  });

  it('accepts plain http base URLs', () => {
    const parsed = parseTargetLocator('https://memstation.example.com');

    expect(parsed.target.baseUrl).toBe('https://memstation.example.com');
    expect(parsed.target.label).toBe('memstation.example.com');
  });

  it('rejects unsupported schemes', () => {
    expect(() => parseTargetLocator('ftp://memstation.example.com')).toThrow(
      'Connection URI must use http://, https://, memstation://, or memstations://'
    );
  });
});
