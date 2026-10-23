import { describe, expect, it } from 'vitest';

import { buildMemoryTurnDiff } from './memory-diff';

import type { ConversationMemoryPayload, MemoryPreviewPayload } from '#lib/types.ts';

function makeConversationMemory(overrides?: Partial<ConversationMemoryPayload>): ConversationMemoryPayload {
  return {
    conversation_id: 'conv-1',
    documents: [],
    key_memories: [],
    ...overrides
  };
}

function makePreview(overrides?: Partial<MemoryPreviewPayload>): MemoryPreviewPayload {
  return {
    conversation_id: 'conv-1',
    summary_status: 'preview',
    analysis_status: 'preview',
    ...overrides
  };
}

describe('buildMemoryTurnDiff', () => {
  it('captures preview-mode additions across injected and extracted memory', () => {
    const diff = buildMemoryTurnDiff({
      memoryPersist: false,
      previousInjectedMemory: null,
      injectedMemory: {
        query: 'recent travel preferences',
        total: 1,
        query_embedding_tokens: 12,
        items: [
          {
            id: 'summary-1',
            node_id: 'node-1',
            type: 'summary',
            content: 'User recently traveled to Tokyo.',
            score: 0.91,
            effective_score: 0.82,
            conversation_id: 'conv-0'
          }
        ]
      },
      previousMemoryPreview: null,
      memoryPreview: makePreview({
        keywords: ['tokyo'],
        summary: {
          id: 'summary-preview-1',
          type: 'summary',
          content: 'The user discussed a recent Tokyo trip.'
        },
        fact: {
          id: 'fact-preview-1',
          type: 'fact',
          content: 'The user prefers aisle seats.',
          metadata: { confidence: 0.72 }
        },
        impression: {
          id: 'impression-preview-1',
          type: 'impression',
          content: 'The user plans travel carefully.'
        },
        key_memories: [
          {
            key: 'travel_city',
            value: 'Tokyo',
            source: 'preview',
            category: 'travel',
            status: 'candidate'
          }
        ]
      })
    });

    expect(diff.changeCount).toBe(7);
    expect(diff.sections.map((section) => section.id)).toEqual([
      'injected',
      'preview-documents',
      'preview-key-memories',
      'preview-keywords'
    ]);

    const injectedSection = diff.sections.find((section) => section.id === 'injected');
    expect(injectedSection?.entries).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ title: 'Retrieval query', change: 'added', after: 'recent travel preferences' }),
        expect.objectContaining({ title: 'Summary memory', change: 'added', after: 'User recently traveled to Tokyo.' })
      ])
    );

    const previewDocuments = diff.sections.find((section) => section.id === 'preview-documents');
    expect(previewDocuments?.entries).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ title: 'Summary', change: 'added' }),
        expect.objectContaining({ title: 'Fact', change: 'added' }),
        expect.objectContaining({ title: 'Impression', change: 'added' })
      ])
    );

    const previewKeyMemories = diff.sections.find((section) => section.id === 'preview-key-memories');
    expect(previewKeyMemories?.entries).toEqual([
      expect.objectContaining({ title: 'travel_city', change: 'added', after: 'Tokyo' })
    ]);

    const previewKeywords = diff.sections.find((section) => section.id === 'preview-keywords');
    expect(previewKeywords?.entries).toEqual([
      expect.objectContaining({ title: 'tokyo', change: 'added', after: 'tokyo' })
    ]);
  });

  it('captures persisted-mode updates, additions, and removals', () => {
    const previousMemory = makeConversationMemory({
      documents: [
        {
          id: 'summary-1',
          node_id: 'node-summary-1',
          user_id: 'user-1',
          conversation_id: 'conv-1',
          type: 'summary',
          content: 'User discussed travel plans.',
          metadata: { topic_label: 'travel' }
        },
        {
          id: 'impression-1',
          node_id: 'node-impression-1',
          user_id: 'user-1',
          conversation_id: 'conv-1',
          type: 'impression',
          content: 'The user is decisive.'
        }
      ],
      key_memories: [
        {
          id: 'km-1',
          key: 'favorite_airport',
          value: 'PVG',
          category: 'travel',
          source: 'analysis',
          review_status: 'confirmed'
        }
      ]
    });

    const currentMemory = makeConversationMemory({
      documents: [
        {
          id: 'summary-1',
          node_id: 'node-summary-1',
          user_id: 'user-1',
          conversation_id: 'conv-1',
          type: 'summary',
          content: 'User finalized travel plans for Tokyo.',
          metadata: { topic_label: 'tokyo travel' }
        },
        {
          id: 'fact-1',
          node_id: 'node-fact-1',
          user_id: 'user-1',
          conversation_id: 'conv-1',
          type: 'fact',
          content: 'The user flies from PVG to HND.'
        }
      ],
      key_memories: [
        {
          id: 'km-1',
          key: 'favorite_airport',
          value: 'HND',
          category: 'travel',
          source: 'analysis',
          review_status: 'pending'
        },
        {
          id: 'km-2',
          key: 'preferred_airline',
          value: 'ANA',
          category: 'travel',
          source: 'tool',
          review_status: 'pending'
        }
      ]
    });

    const diff = buildMemoryTurnDiff({
      memoryPersist: true,
      previousInjectedMemory: { query: 'airport', total: 1, items: [] },
      injectedMemory: { query: 'airport lounges', total: 2, items: [] },
      previousConversationMemory: previousMemory,
      conversationMemory: currentMemory,
      previousMemoryPreview: makePreview({ keywords: ['ignored'] }),
      memoryPreview: makePreview({ keywords: ['still ignored'] })
    });

    expect(diff.sections.map((section) => section.id)).toEqual([
      'injected',
      'persisted-documents',
      'persisted-key-memories'
    ]);

    const documents = diff.sections.find((section) => section.id === 'persisted-documents');
    expect(documents?.entries).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ title: 'Summary document', change: 'updated', before: 'User discussed travel plans.', after: 'User finalized travel plans for Tokyo.' }),
        expect.objectContaining({ title: 'Fact document', change: 'added', after: 'The user flies from PVG to HND.' }),
        expect.objectContaining({ title: 'Impression document', change: 'removed', before: 'The user is decisive.' })
      ])
    );

    const keyMemories = diff.sections.find((section) => section.id === 'persisted-key-memories');
    expect(keyMemories?.entries).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ title: 'favorite_airport', change: 'updated', before: 'PVG', after: 'HND' }),
        expect.objectContaining({ title: 'preferred_airline', change: 'added', after: 'ANA' })
      ])
    );

    expect(diff.sections.find((section) => section.id === 'preview-keywords')).toBeUndefined();
  });

  it('returns no sections when nothing changed', () => {
    const preview = makePreview({
      keywords: ['travel'],
      summary: {
        id: 'summary-1',
        type: 'summary',
        content: 'Stable summary.'
      }
    });

    const diff = buildMemoryTurnDiff({
      memoryPersist: false,
      previousInjectedMemory: {
        query: 'stable query',
        total: 0,
        items: []
      },
      injectedMemory: {
        query: 'stable query',
        total: 0,
        items: []
      },
      previousMemoryPreview: preview,
      memoryPreview: preview
    });

    expect(diff).toEqual({ sections: [], changeCount: 0 });
  });
});
