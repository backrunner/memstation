package memory

import (
	"context"
	"errors"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

var ErrInvalidEdit = errors.New("invalid memory edit")

func (s *Service) EditDocument(ctx context.Context, user, id string, kind domain.MemoryType, content string, condition domain.MemoryWriteCondition) (domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, user, ""); err != nil {
		return domain.MemoryDocument{}, err
	}
	if condition.UpdatedAt.IsZero() || condition.GenerationID == "" || strings.TrimSpace(content) == "" || len(content) > 128<<10 || (kind != domain.MemoryTypeSummary && kind != domain.MemoryTypeFact && kind != domain.MemoryTypeImpression && !domain.IsGenericMemoryType(kind)) {
		return domain.MemoryDocument{}, ErrInvalidEdit
	}
	return s.updateDocument(ctx, user, id, kind, content, &condition)
}

func (s *Service) EditKeyMemory(ctx context.Context, user, id, key, value, category, status string, condition domain.MemoryWriteCondition) (domain.KeyMemory, error) {
	if _, err := security.DataScope(ctx, user, ""); err != nil {
		return domain.KeyMemory{}, err
	}
	key = strings.TrimSpace(key)
	if strings.TrimSpace(value) == "" || len(value) > 128<<10 || len(category) > 256 || (status != "pending" && status != "approved" && status != "rejected") || condition.GenerationID != "" {
		return domain.KeyMemory{}, ErrInvalidEdit
	}
	km := domain.KeyMemory{UserID: user, Key: key, Source: "admin_rest"}
	if condition.Create {
		if key == "" || len(key) > 256 || id != "" || !condition.UpdatedAt.IsZero() {
			return domain.KeyMemory{}, ErrInvalidEdit
		}
	} else {
		if condition.UpdatedAt.IsZero() || id == "" {
			return domain.KeyMemory{}, ErrInvalidEdit
		}
		existing, ok, err := s.GetKeyMemoryByID(ctx, user, id)
		if err != nil {
			return km, err
		}
		if !ok {
			return km, domain.ErrAccessNotFound
		}
		km = existing
		condition.ID = id
	}
	km.Value = value
	km.Category = category
	km.ReviewStatus = status
	km.Source = "admin_rest"
	return s.upsertKeyMemory(ctx, km, &condition)
}

func (s *Service) RemoveKeyMemory(ctx context.Context, user, id string, condition domain.MemoryWriteCondition) error {
	if condition.UpdatedAt.IsZero() || condition.GenerationID != "" {
		return ErrInvalidEdit
	}
	item, ok, err := s.GetKeyMemoryByID(ctx, user, id)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrAccessNotFound
	}
	condition.ID = id
	return s.deleteKeyMemory(ctx, user, item.Key, &condition)
}
