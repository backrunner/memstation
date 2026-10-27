import type { MemoryReceipt } from './memory-service.ts';

export const memoryServiceViews = [
  { id: 'activity', label: 'Activity', description: 'Follow messages as they become memory.' },
  { id: 'review', label: 'Review', description: 'Check suggestions before they become lasting memory.' },
  { id: 'sharing', label: 'Sharing', description: 'Publish approved copies to another workspace.' },
  { id: 'connections', label: 'Connections', description: 'Choose message sources and private audiences.' },
  { id: 'data', label: 'Data', description: 'Export, delete, or rebuild workspace memory.' }
] as const;
export type MemoryServiceView = typeof memoryServiceViews[number]['id'];
export function memoryServiceView(value: string | null): MemoryServiceView {
  return memoryServiceViews.find(view => view.id === value)?.id ?? 'activity';
}

const labels: Record<string, string> = {
  fact: 'Fact', preference: 'Preference', event: 'Event', relationship: 'Relationship',
  group_habit: 'Group habit', summary: 'Summary', inference: 'Unconfirmed inference',
  pending: 'Needs review', approved: 'Approved', rejected: 'Rejected', invalidated: 'Source changed',
  active: 'Active', paused: 'Paused', retracted: 'Retracted', revoked: 'Retracted', replaced: 'Replaced',
  accepted: 'Saved', processing: 'Processing', complete: 'Complete', completed: 'Complete',
  failed: 'Failed', dead_letter: 'Stopped', queued: 'Queued', skipped: 'Skipped',
  erased: 'Deleted', expired: 'Expired', running: 'Building', discarded: 'Discarded', activated: 'Active',
  'summary.generate': 'Create summary', 'analysis.extract': 'Extract memories',
  'embedding.generate': 'Update search index', 'keymemory.reconcile': 'Update key memories',
  'media.extract': 'Read attachments', 'conversation.finalize': 'Close message window',
  'ingestion.accepted': 'Accept messages', 'memory.lifecycle': 'Clean up data',
  'memory.rebuild.carry': 'Reindex approved memory', 'memory.rebuild.activate': 'Activate index',
  provider_timeout: 'Model request timed out', processing_failed: 'Processing failed',
  source_obsolete: 'Source has changed', cleanup_failed: 'Cleanup needs another attempt',
  analysis: 'Memory extraction', embedding: 'Search indexing', rerank: 'Search ranking'
};
export function memoryLabel(value: string): string {
  return labels[value] ?? value.replaceAll('_', ' ').replaceAll('.', ' ');
}
export function shortMemoryID(value: string): string {
  return value.length > 22 ? `${value.slice(0, 12)}…${value.slice(-6)}` : value;
}
export function receiptProgress(receipt: MemoryReceipt): { done: number; total: number; percent: number } {
  const total = Math.max(0, receipt.accepted_sequence);
  const done = Math.min(total, Math.max(0, receipt.indexed_sequence));
  return { done, total, percent: total === 0 ? 0 : Math.round(done / total * 100) };
}
export function latestReceipts(receipts: MemoryReceipt[]): MemoryReceipt[] {
  const streams = new Map<string, MemoryReceipt>();
  const attention = (receipt: MemoryReceipt) => receipt.status === 'failed' ? 3 : receipt.status === 'processing' ? 2 : receipt.status === 'accepted' ? 1 : 0;
  for (const receipt of receipts) {
    const key = `${receipt.stream_id}:${receipt.generation_id ?? ''}`;
    const previous = streams.get(key);
    if (!previous || receipt.accepted_sequence > previous.accepted_sequence || receipt.accepted_sequence === previous.accepted_sequence && (receipt.indexed_sequence > previous.indexed_sequence || receipt.indexed_sequence === previous.indexed_sequence && attention(receipt) > attention(previous))) streams.set(key, receipt);
  }
  return [...streams.values()];
}
