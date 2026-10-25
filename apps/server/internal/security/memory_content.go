package security

import (
	"context"
	"slices"

	"memstation/apps/server/internal/domain"
)

// Memory read permits derived records and evidence identities. Raw segments,
// source excerpts and extensible source metadata require a separate capability.
// Copy collections so a restricted response never mutates a shared cached value.
func MemoryContent(ctx context.Context, doc domain.MemoryDocument) (domain.MemoryDocument, bool) {
	doc = domain.ProjectMemoryDocument(doc)
	if RequireCapability(ctx, domain.CapabilitySourcesRead) == nil {
		return doc, true
	}
	if doc.Type == domain.MemoryTypeSegment {
		return domain.MemoryDocument{}, false
	}
	metadata := map[string]any{}
	for _, key := range []string{"semantic_score", "base_semantic_score", "provider_score", "score_definition", "importance", "time_decay", "keyword_score", "relation_expansion", "rerank_score", "rerank_weight", "rerank_rank", "effective_score", "valid_from", "valid_to", "is_superseded", "superseded_by_id", "support_kind", "subject_scope", "kind", "status", "review_status"} {
		if v, ok := doc.Metadata[key]; ok {
			metadata[key] = v
		}
	}
	doc.Metadata = metadata
	doc.Evidences = slices.Clone(doc.Evidences)
	for i := range doc.Evidences {
		doc.Evidences[i].ExcerptText = ""
		doc.Evidences[i].Metadata = nil
	}
	doc.Related = slices.DeleteFunc(slices.Clone(doc.Related), func(v domain.RelatedMemory) bool { return v.DocumentType == string(domain.MemoryTypeSegment) })
	return doc, true
}

func MemoryContents(ctx context.Context, docs []domain.MemoryDocument) []domain.MemoryDocument {
	result := make([]domain.MemoryDocument, 0, len(docs))
	for _, doc := range docs {
		if visible, ok := MemoryContent(ctx, doc); ok {
			result = append(result, visible)
		}
	}
	return result
}
