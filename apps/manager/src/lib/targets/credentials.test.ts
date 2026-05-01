import { describe, expect, it } from 'vitest';

import { forgetTargetCredential, readTargetCredential, rememberTargetCredential } from '$lib/targets/credentials';

describe('target credentials', () => {
  it('keeps admin credentials in tab memory by target id', () => {
    rememberTargetCredential('target-1', ' admin-token ');
    expect(readTargetCredential('target-1')).toBe('admin-token');
    expect(readTargetCredential('target-2')).toBe('');

    rememberTargetCredential('target-1', undefined);
    expect(readTargetCredential('target-1')).toBe('');
  });

  it('can forget credentials explicitly', () => {
    rememberTargetCredential('target-2', 'credential');
    forgetTargetCredential('target-2');
    expect(readTargetCredential('target-2')).toBe('');
  });
});
