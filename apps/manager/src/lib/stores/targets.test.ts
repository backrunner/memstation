import { get } from 'svelte/store';
import { describe, expect, it, vi } from 'vitest';
vi.mock('$app/env', () => ({ browser: false }));
import { beginTargetSessionCheck, finishTargetSessionCheck, markTargetSessionConnected, selectTarget, targetState } from './targets.ts';

const targets = ['one', 'two'].map((id) => ({ id, label: id, baseUrl: `https://${id}.example`, environment: 'test', clusterName: 'standalone', region: 'global', kind: 'single' as const }));
describe('target session request ordering', () => {
  it('ignores older responses after switching targets or reconnecting the same target', () => {
    targetState.set({ loaded: true, targets, selectedId: 'one', session: { authenticated: true, checking: false } });
    const old = beginTargetSessionCheck('one');
    selectTarget('two');
    const current = beginTargetSessionCheck('two');
    finishTargetSessionCheck('one', old, { authenticated: true, checking: false });
    expect(get(targetState).session.checking).toBe(true);
    finishTargetSessionCheck('two', current, { authenticated: true, checking: false });
    expect(get(targetState).session.authenticated).toBe(true);
    const stale = beginTargetSessionCheck('two');
    markTargetSessionConnected('two');
    finishTargetSessionCheck('two', stale, { authenticated: false, checking: false });
    expect(get(targetState).session.authenticated).toBe(true);
  });
});
