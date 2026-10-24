package analysis

import (
	"context"
	"errors"

	"memstation/apps/server/internal/domain"
)

type KeyMemoryCandidate struct {
	Key               string   `json:"key"`
	Value             string   `json:"value"`
	Category          string   `json:"category"`
	Importance        float64  `json:"importance"`
	DecayFactor       float64  `json:"decay_factor"`
	SourceDocumentIDs []string `json:"source_document_ids"`
}

func (s *Service) ReconcileKeyMemories(ctx context.Context, input Input, documents []domain.MemoryDocument, current []domain.KeyMemory, allowed []string) ([]KeyMemoryCandidate, error) {
	facts := []map[string]any{}
	ids := map[string]bool{}
	keys := map[string]bool{}
	for _, key := range allowed {
		keys[key] = true
	}
	for _, doc := range documents {
		if doc.UserID != input.UserID || doc.ConversationID != input.ConversationID {
			return nil, errors.New("foreign document in key memory reconciliation")
		}
		if doc.Type != domain.MemoryTypeFact {
			continue
		}
		if metadataString(doc.Metadata, "superseded_by_id") != "" || doc.Metadata["is_superseded"] == true {
			continue
		}
		facts = append(facts, map[string]any{"id": doc.ID, "content": doc.Content, "subject": metadataString(doc.Metadata, "subject"), "confidence": doc.Metadata["confidence"]})
		ids[doc.ID] = true
	}
	if len(keys) == 0 || len(facts) == 0 {
		return []KeyMemoryCandidate{}, nil
	}
	existing := []map[string]string{}
	for _, item := range current {
		if item.UserID != input.UserID {
			return nil, errors.New("foreign key memory in reconciliation")
		}
		if keys[item.Key] {
			existing = append(existing, map[string]string{"key": item.Key, "value": item.Value})
		}
	}
	var result struct {
		Memories []KeyMemoryCandidate `json:"memories"`
	}
	err := s.generate(ctx, input, "keymemory.reconcile", "analysis_model", `Consolidate only durable core memories supported by the supplied validated facts. Return {"memories":[]}; an empty array is valid. Each memory has key,value,category,importance,decay_factor,source_document_ids. Choose keys only from allowed_keys, at most one update per key. Keep existing supported knowledge; do not replace unrelated attributes or copy a conversation ID as memory content. Preserve source language. Every update must cite supplied fact IDs. Importance 0.5..2, decay_factor 0.5..3. Never invent a profile or infer sensitive attributes.`, map[string]any{"facts": facts, "current_memories": existing, "allowed_keys": allowed}, &result)
	if err != nil {
		return nil, err
	}
	if result.Memories == nil || len(result.Memories) > len(keys) {
		return nil, errors.New("invalid LLM key memory collection")
	}
	seen := map[string]bool{}
	for _, item := range result.Memories {
		if !keys[item.Key] || seen[item.Key] || !validText(item.Value, 8000) || !validText(item.Category, 80) || !validScore(item.Importance, item.DecayFactor) || len(item.SourceDocumentIDs) == 0 || len(item.SourceDocumentIDs) > 16 {
			return nil, errors.New("invalid LLM key memory update")
		}
		for _, id := range item.SourceDocumentIDs {
			if !ids[id] {
				return nil, errors.New("LLM key memory references an unknown fact")
			}
		}
		seen[item.Key] = true
	}
	return result.Memories, nil
}
