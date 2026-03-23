import { afterEach, describe, expect, it, vi } from 'vitest';

import { deleteAdminKeyMemory, revokeUserAPIKey } from '$lib/api/client';
import type { MemStationTarget } from '$lib/types';

const target: MemStationTarget = {
  id: 'fixture',
  label: 'Fixture',
  baseUrl: 'http://127.0.0.1:18080',
  environment: 'test',
  clusterName: 'standalone',
  region: 'local',
  kind: 'single'
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe('api client delete flows', () => {
  it('treats 204 key memory deletion responses as success', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(deleteAdminKeyMemory(target, 'key-1', 'admin')).resolves.toBeUndefined();
    expect(fetchMock).toHaveBeenCalledWith(
      'http://127.0.0.1:18080/api/admin/memory/keys/key-1?user_id=admin',
      expect.objectContaining({ method: 'DELETE' })
    );
  });

  it('treats 204 API key revoke responses as success', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(revokeUserAPIKey(target, 'admin', 'api-key-1')).resolves.toBeUndefined();
    expect(fetchMock).toHaveBeenCalledWith(
      'http://127.0.0.1:18080/api/admin/users/admin/api-keys/api-key-1',
      expect.objectContaining({ method: 'DELETE' })
    );
  });
});
