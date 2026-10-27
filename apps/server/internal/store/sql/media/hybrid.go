package media

import (
	"context"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
)

type HybridMediaRepository struct {
	db       runtimeconfig.DBHandle
	fallback *InMemoryMediaRepository
}

func NewHybridMediaRepository(db runtimeconfig.DBHandle) *HybridMediaRepository {
	return &HybridMediaRepository{
		db:       db,
		fallback: NewInMemoryMediaRepository(),
	}
}

func (r *HybridMediaRepository) PutBlob(ctx context.Context, blob domain.BlobObject) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMediaRepository(db).PutBlob(ctx, blob)
	}
	return r.fallback.PutBlob(ctx, blob)
}

func (r *HybridMediaRepository) GetBlob(ctx context.Context, userID, blobID string) (domain.BlobObject, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMediaRepository(db).GetBlob(ctx, userID, blobID)
	}
	return r.fallback.GetBlob(ctx, userID, blobID)
}

func (r *HybridMediaRepository) DeleteBlob(ctx context.Context, userID, blobID string, deletedAt time.Time) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMediaRepository(db).DeleteBlob(ctx, userID, blobID, deletedAt)
	}
	return r.fallback.DeleteBlob(ctx, userID, blobID, deletedAt)
}

func (r *HybridMediaRepository) ReplaceMessageParts(ctx context.Context, userID, conversationID string, parts []domain.MessagePart) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMediaRepository(db).ReplaceMessageParts(ctx, userID, conversationID, parts)
	}
	return r.fallback.ReplaceMessageParts(ctx, userID, conversationID, parts)
}

func (r *HybridMediaRepository) ListMessageParts(ctx context.Context, userID, conversationID string) ([]domain.MessagePart, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMediaRepository(db).ListMessageParts(ctx, userID, conversationID)
	}
	return r.fallback.ListMessageParts(ctx, userID, conversationID)
}

func (r *HybridMediaRepository) ReplacePartArtifacts(ctx context.Context, userID, conversationID string, artifacts []domain.MessagePartArtifact) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMediaRepository(db).ReplacePartArtifacts(ctx, userID, conversationID, artifacts)
	}
	return r.fallback.ReplacePartArtifacts(ctx, userID, conversationID, artifacts)
}

func (r *HybridMediaRepository) ListPartArtifacts(ctx context.Context, userID, conversationID string) ([]domain.MessagePartArtifact, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMediaRepository(db).ListPartArtifacts(ctx, userID, conversationID)
	}
	return r.fallback.ListPartArtifacts(ctx, userID, conversationID)
}
