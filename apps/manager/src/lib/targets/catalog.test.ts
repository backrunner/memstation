import { describe, expect, it } from 'vitest';
import { configureTargetCatalog, managerTargetURL, registeredTarget } from './catalog.ts';

describe('browser target preferences', () => {
  it('cannot turn local target metadata into a new forwarding destination', () => {
    configureTargetCatalog([{ id: 'registered', label: 'Local', baseUrl: 'http://localhost:8080', environment: 'custom', clusterName: 'standalone', region: 'global', kind: 'single' }]);
    expect(registeredTarget({ baseUrl: 'http://localhost:8080/' }).id).toBe('registered');
    expect(managerTargetURL({ baseUrl: 'http://localhost:8080' }, '/api/admin/users')).toBe('/manager/api/targets/registered/api/admin/users');
    expect(() => registeredTarget({ id: 'other', baseUrl: 'http://localhost:8080' })).toThrow();
    expect(() => registeredTarget({ baseUrl: 'http://169.254.169.254' })).toThrow();
  });
});
