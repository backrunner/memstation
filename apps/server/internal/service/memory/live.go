package memory

import (
	"context"

	"memstation/apps/server/internal/domain"
)

// Revalidate all returned IDs after reranking. Provider output and cache entries
// cannot restore a revoked node or a source that changed while the model ran.
func (s *Service) filterLiveSearch(ctx context.Context, user string, result *domain.SearchResult, historical bool) error {
	groups := map[string][]string{}
	for _, doc := range result.Memories {
		groups[doc.GenerationID] = append(groups[doc.GenerationID], doc.NodeID)
		for _, related := range doc.Related {
			groups[doc.GenerationID] = append(groups[doc.GenerationID], related.NodeID)
		}
	}
	live := map[string]domain.MemoryDocument{}
	for generation, ids := range groups {
		docs, err := s.repo.ListDocumentsByNodeIDsInGeneration(ctx, user, generation, ids)
		if err != nil {
			return err
		}
		for _, doc := range docs {
			live[doc.NodeID] = doc
		}
	}
	kept := make([]domain.MemoryDocument, 0, len(result.Memories))
	for _, doc := range result.Memories {
		current, ok := live[doc.NodeID]
		if !ok {
			continue
		}
		// Historical reads keep their temporal projection but still require a live
		// identity. Current reads reject a body corrected during the model call.
		if !historical && (doc.Content != current.Content || doc.Revision != current.Revision) {
			continue
		}
		related := []domain.RelatedMemory{}
		for _, edge := range doc.Related {
			if _, ok := live[edge.NodeID]; ok {
				related = append(related, edge)
			}
		}
		doc.Related = related
		kept = append(kept, doc)
	}
	result.Memories = kept
	result.Total = len(kept)
	return nil
}
