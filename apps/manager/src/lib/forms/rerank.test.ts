import { describe, expect, it } from 'vitest';
import { parseRerankModel, rerankKeyOptional } from './rerank.ts';

describe('rerank model references', () => {
  it('preserves namespaces and tags after the canonical provider separator', () => {
    for (const value of ['openrouter:qwen/model:free']) {
      expect(parseRerankModel(value)).toEqual({ provider: 'openrouter', model: 'qwen/model:free' });
    }
    expect(parseRerankModel('rerank_compatible:owner/model:Q8_0')).toEqual({ provider: 'rerank_compatible', model: 'owner/model:Q8_0' });
  });
  it('does not silently disable unknown saved values', () => {
    expect(parseRerankModel('')).toEqual({ provider: 'disabled', model: '' });
    for (const value of ['openrouter/qwen/model:free','jina_ai:model','rerank-compatible:model']) expect(parseRerankModel(value)).toEqual({provider:'unknown',model:value});
    expect(parseRerankModel('future:model')).toEqual({ provider: 'unknown', model: 'future:model' });
    expect(rerankKeyOptional('rerank_compatible')).toBe(true);
    expect(rerankKeyOptional('openrouter')).toBe(false);
  });
});
