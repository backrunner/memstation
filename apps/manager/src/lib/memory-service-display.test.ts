import { describe, expect, it } from 'vitest';
import { latestReceipts, receiptProgress } from './memory-service-display.ts';
import type { MemoryReceipt } from './memory-service.ts';

function receipt(overrides: Partial<MemoryReceipt> = {}): MemoryReceipt {
  return { id: 'one', stream_id: 'stream', generation_id: 'index', status: 'complete', accepted_sequence: 7, indexed_sequence: 4, retry_count: 0, dead_letter: false, events: [], ...overrides };
}

describe('memory processing display', () => {
  it('shows the latest continuous stream watermark without double counting batch receipts', () => {
    const latest = receipt({ id: 'latest', accepted_sequence: 10, indexed_sequence: 6 });
    const anotherIndex = receipt({ id: 'other-index', generation_id: 'next-index' });
    expect(latestReceipts([receipt(), latest, anotherIndex, receipt({ accepted_sequence: 5 })])).toEqual([latest, anotherIndex]);
  });
  it('keeps failure visible when another receipt reports the same stream watermarks', () => {
    const failed = receipt({ id: 'failed', status: 'failed', dead_letter: true });
    expect(latestReceipts([failed, receipt()])).toEqual([failed]);
    expect(latestReceipts([receipt(), failed])).toEqual([failed]);
  });
  it('does not show indexing beyond the accepted range or a completed empty stream', () => {
    expect(receiptProgress(receipt())).toEqual({ done: 4, total: 7, percent: 57 });
    expect(receiptProgress(receipt({ indexed_sequence: 10 }))).toEqual({ done: 7, total: 7, percent: 100 });
    expect(receiptProgress(receipt({ accepted_sequence: 0, indexed_sequence: 0 }))).toEqual({ done: 0, total: 0, percent: 0 });
  });
});
