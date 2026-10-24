package sql

import (
	"context"
	"sort"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func (r *HybridMemoryRepository) BrowseDocuments(ctx context.Context, input domain.MemoryBrowseQuery, position *domain.MemoryPosition) ([]domain.MemoryDocument, error) {
	if db := currentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).BrowseDocuments(ctx, input, position)
	}
	return r.fallback.BrowseDocuments(ctx, input, position)
}

func (r *PostgresMemoryRepository) BrowseDocuments(ctx context.Context, input domain.MemoryBrowseQuery, position *domain.MemoryPosition) ([]domain.MemoryDocument, error) {
	return r.listDocumentsInGeneration(ctx, input.UserID, input.GenerationID, input.Kinds, input.Limit, &input, position)
}

func (r *InMemoryMemoryRepository) BrowseDocuments(ctx context.Context, input domain.MemoryBrowseQuery, position *domain.MemoryPosition) ([]domain.MemoryDocument, error) {
	if _, err := security.PersonalScope(ctx, input.UserID, ""); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []domain.MemoryDocument{}
	query := strings.ToLower(input.Query)
	for _, doc := range r.documents {
		if doc.UserID != input.UserID || !matchesDocumentGeneration(input.UserID, input.GenerationID, doc) || !matchesMemoryType(doc.Type, input.Kinds) {
			continue
		}
		if !strings.Contains(strings.ToLower(doc.Content), query) && !strings.Contains(strings.ToLower(doc.ID), query) {
			continue
		}
		if position != nil && !memoryPositionBefore(doc, *position) {
			continue
		}
		items = append(items, attachDocumentRuntimeMetadata(doc))
	}
	sort.Slice(items, func(i, j int) bool {
		return memoryPositionBefore(items[j], domain.MemoryPosition{UpdatedAt: items[i].UpdatedAt, Kind: items[i].Type, ID: items[i].ID})
	})
	if input.Limit > 0 && len(items) > input.Limit {
		items = items[:input.Limit]
	}
	return items, nil
}

func memoryPositionBefore(doc domain.MemoryDocument, position domain.MemoryPosition) bool {
	if !doc.UpdatedAt.Equal(position.UpdatedAt) {
		return doc.UpdatedAt.Before(position.UpdatedAt)
	}
	if doc.Type != position.Kind {
		return doc.Type < position.Kind
	}
	return doc.ID < position.ID
}
