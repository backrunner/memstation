package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"memstation/apps/server/internal/domain"
)

type InMemoryMediaRepository struct {
	mu        sync.RWMutex
	blobs     map[string]domain.BlobObject
	parts     map[string][]domain.MessagePart
	artifacts map[string][]domain.MessagePartArtifact
}

func NewInMemoryMediaRepository() *InMemoryMediaRepository {
	return &InMemoryMediaRepository{
		blobs:     map[string]domain.BlobObject{},
		parts:     map[string][]domain.MessagePart{},
		artifacts: map[string][]domain.MessagePartArtifact{},
	}
}

func (r *InMemoryMediaRepository) PutBlob(_ context.Context, blob domain.BlobObject) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	if blob.CreatedAt.IsZero() {
		blob.CreatedAt = now
	}
	if blob.UpdatedAt.IsZero() {
		blob.UpdatedAt = now
	}
	blob.Metadata = cloneMetadata(blob.Metadata)
	r.blobs[key(blob.UserID, blob.BlobID)] = blob
	return nil
}

func (r *InMemoryMediaRepository) GetBlob(_ context.Context, userID, blobID string) (domain.BlobObject, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	blob, ok := r.blobs[key(userID, blobID)]
	if !ok {
		return domain.BlobObject{}, errors.New("blob not found")
	}
	blob.Metadata = cloneMetadata(blob.Metadata)
	return blob, nil
}

func (r *InMemoryMediaRepository) DeleteBlob(_ context.Context, userID, blobID string, deletedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	blob, ok := r.blobs[key(userID, blobID)]
	if !ok {
		return nil
	}
	if deletedAt.IsZero() {
		deletedAt = time.Now().UTC()
	}
	blob.DeletedAt = &deletedAt
	blob.UpdatedAt = deletedAt
	r.blobs[key(userID, blobID)] = blob
	return nil
}

func (r *InMemoryMediaRepository) ReplaceMessageParts(_ context.Context, userID, conversationID string, parts []domain.MessagePart) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]domain.MessagePart, 0, len(parts))
	now := time.Now().UTC()
	for _, part := range parts {
		if part.UserID == "" {
			part.UserID = userID
		}
		if part.ConversationID == "" {
			part.ConversationID = conversationID
		}
		if part.CreatedAt.IsZero() {
			part.CreatedAt = now
		}
		if part.UpdatedAt.IsZero() {
			part.UpdatedAt = now
		}
		part.Metadata = cloneMetadata(part.Metadata)
		items = append(items, part)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SequenceNum == items[j].SequenceNum {
			if items[i].PartIndex == items[j].PartIndex {
				return items[i].StartMS < items[j].StartMS
			}
			return items[i].PartIndex < items[j].PartIndex
		}
		return items[i].SequenceNum < items[j].SequenceNum
	})
	r.parts[key(userID, conversationID)] = items
	return nil
}

func (r *InMemoryMediaRepository) ListMessageParts(_ context.Context, userID, conversationID string) ([]domain.MessagePart, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := append([]domain.MessagePart(nil), r.parts[key(userID, conversationID)]...)
	for idx := range items {
		items[idx].Metadata = cloneMetadata(items[idx].Metadata)
	}
	return items, nil
}

func (r *InMemoryMediaRepository) ReplacePartArtifacts(_ context.Context, userID, conversationID string, artifacts []domain.MessagePartArtifact) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]domain.MessagePartArtifact, 0, len(artifacts))
	now := time.Now().UTC()
	for _, artifact := range artifacts {
		if artifact.UserID == "" {
			artifact.UserID = userID
		}
		if artifact.ConversationID == "" {
			artifact.ConversationID = conversationID
		}
		if artifact.CreatedAt.IsZero() {
			artifact.CreatedAt = now
		}
		if artifact.UpdatedAt.IsZero() {
			artifact.UpdatedAt = now
		}
		artifact.Metadata = cloneMetadata(artifact.Metadata)
		items = append(items, artifact)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].PartID == items[j].PartID {
			if items[i].StartMS == items[j].StartMS {
				return items[i].ArtifactType < items[j].ArtifactType
			}
			return items[i].StartMS < items[j].StartMS
		}
		return items[i].PartID < items[j].PartID
	})
	r.artifacts[key(userID, conversationID)] = items
	return nil
}

func (r *InMemoryMediaRepository) ListPartArtifacts(_ context.Context, userID, conversationID string) ([]domain.MessagePartArtifact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := append([]domain.MessagePartArtifact(nil), r.artifacts[key(userID, conversationID)]...)
	for idx := range items {
		items[idx].Metadata = cloneMetadata(items[idx].Metadata)
	}
	return items, nil
}

type PostgresMediaRepository struct {
	db *sqlpkg.DB
}

func NewPostgresMediaRepository(db *sqlpkg.DB) *PostgresMediaRepository {
	return &PostgresMediaRepository{db: db}
}

func (r *PostgresMediaRepository) PutBlob(ctx context.Context, blob domain.BlobObject) error {
	now := time.Now().UTC()
	if blob.CreatedAt.IsZero() {
		blob.CreatedAt = now
	}
	if blob.UpdatedAt.IsZero() {
		blob.UpdatedAt = now
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO blob_objects (
			blob_id, user_id, source_kind, storage_backend, storage_key, mime_type, sha256,
			size_bytes, original_filename, metadata, deleted_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (blob_id) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			source_kind = EXCLUDED.source_kind,
			storage_backend = EXCLUDED.storage_backend,
			storage_key = EXCLUDED.storage_key,
			mime_type = EXCLUDED.mime_type,
			sha256 = EXCLUDED.sha256,
			size_bytes = EXCLUDED.size_bytes,
			original_filename = EXCLUDED.original_filename,
			metadata = EXCLUDED.metadata,
			deleted_at = EXCLUDED.deleted_at,
			updated_at = EXCLUDED.updated_at
	`, blob.BlobID, blob.UserID, string(blob.SourceKind), blob.StorageBackend, blob.StorageKey, blob.MIMEType, blob.SHA256, blob.SizeBytes, blob.OriginalFilename, marshalJSON(blob.Metadata), blob.DeletedAt, blob.CreatedAt, blob.UpdatedAt)
	return err
}

func (r *PostgresMediaRepository) GetBlob(ctx context.Context, userID, blobID string) (domain.BlobObject, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT source_kind, storage_backend, storage_key, mime_type, sha256, size_bytes,
		       original_filename, metadata, deleted_at, created_at, updated_at
		FROM blob_objects
		WHERE blob_id = $1 AND user_id = $2
	`, blobID, userID)

	var blob domain.BlobObject
	var sourceKind string
	var metadataBytes []byte
	var deletedAt sqlpkg.NullTime
	blob.BlobID = blobID
	blob.UserID = userID
	if err := row.Scan(&sourceKind, &blob.StorageBackend, &blob.StorageKey, &blob.MIMEType, &blob.SHA256, &blob.SizeBytes, &blob.OriginalFilename, &metadataBytes, &deletedAt, &blob.CreatedAt, &blob.UpdatedAt); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.BlobObject{}, errors.New("blob not found")
		}
		return domain.BlobObject{}, err
	}
	blob.SourceKind = domain.BlobSourceKind(sourceKind)
	_ = json.Unmarshal(metadataBytes, &blob.Metadata)
	if deletedAt.Valid {
		value := deletedAt.Time.UTC()
		blob.DeletedAt = &value
	}
	return blob, nil
}

func (r *PostgresMediaRepository) DeleteBlob(ctx context.Context, userID, blobID string, deletedAt time.Time) error {
	if deletedAt.IsZero() {
		deletedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE blob_objects
		SET deleted_at = $1, updated_at = $1
		WHERE blob_id = $2 AND user_id = $3
	`, deletedAt, blobID, userID)
	return err
}

func (r *PostgresMediaRepository) ReplaceMessageParts(ctx context.Context, userID, conversationID string, parts []domain.MessagePart) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	if _, err = tx.ExecContext(ctx, `
		DELETE FROM message_parts
		WHERE user_id = $1 AND conversation_id = $2
	`, userID, conversationID); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, part := range parts {
		if part.UserID == "" {
			part.UserID = userID
		}
		if part.ConversationID == "" {
			part.ConversationID = conversationID
		}
		if part.CreatedAt.IsZero() {
			part.CreatedAt = now
		}
		if part.UpdatedAt.IsZero() {
			part.UpdatedAt = now
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO message_parts (
				part_id, user_id, conversation_id, sequence_num, part_index, role, part_type, mime_type,
				blob_id, source_url, provider_file_id, text_content, raw_payload, page_index,
				start_ms, end_ms, metadata, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		`, part.PartID, part.UserID, part.ConversationID, part.SequenceNum, part.PartIndex, string(part.Role), string(part.PartType), part.MIMEType, nullString(part.BlobID), nullString(part.SourceURL), nullString(part.ProviderFileID), nullString(part.TextContent), nullString(part.RawPayload), part.PageIndex, part.StartMS, part.EndMS, marshalJSON(part.Metadata), part.CreatedAt, part.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *PostgresMediaRepository) ListMessageParts(ctx context.Context, userID, conversationID string) ([]domain.MessagePart, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT part_id, sequence_num, part_index, role, part_type, mime_type, blob_id, source_url,
		       provider_file_id, text_content, raw_payload, page_index, start_ms, end_ms, metadata, created_at, updated_at
		FROM message_parts
		WHERE user_id = $1 AND conversation_id = $2
		ORDER BY sequence_num ASC, part_index ASC, start_ms ASC, created_at ASC
	`, userID, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]domain.MessagePart, 0)
	for rows.Next() {
		var part domain.MessagePart
		var role string
		var partType string
		var blobID sqlpkg.NullString
		var sourceURL sqlpkg.NullString
		var providerFileID sqlpkg.NullString
		var textContent sqlpkg.NullString
		var rawPayload sqlpkg.NullString
		var metadataBytes []byte
		part.UserID = userID
		part.ConversationID = conversationID
		if err := rows.Scan(&part.PartID, &part.SequenceNum, &part.PartIndex, &role, &partType, &part.MIMEType, &blobID, &sourceURL, &providerFileID, &textContent, &rawPayload, &part.PageIndex, &part.StartMS, &part.EndMS, &metadataBytes, &part.CreatedAt, &part.UpdatedAt); err != nil {
			return nil, err
		}
		part.Role = domain.MessageRole(role)
		part.PartType = domain.ContentBlockType(partType)
		part.BlobID = blobID.String
		part.SourceURL = sourceURL.String
		part.ProviderFileID = providerFileID.String
		part.TextContent = textContent.String
		part.RawPayload = rawPayload.String
		_ = json.Unmarshal(metadataBytes, &part.Metadata)
		items = append(items, part)
	}
	return items, rows.Err()
}

func (r *PostgresMediaRepository) ReplacePartArtifacts(ctx context.Context, userID, conversationID string, artifacts []domain.MessagePartArtifact) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	if _, err = tx.ExecContext(ctx, `
		DELETE FROM message_part_artifacts
		WHERE user_id = $1 AND conversation_id = $2
	`, userID, conversationID); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, artifact := range artifacts {
		if artifact.UserID == "" {
			artifact.UserID = userID
		}
		if artifact.ConversationID == "" {
			artifact.ConversationID = conversationID
		}
		if artifact.CreatedAt.IsZero() {
			artifact.CreatedAt = now
		}
		if artifact.UpdatedAt.IsZero() {
			artifact.UpdatedAt = now
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO message_part_artifacts (
				artifact_id, part_id, user_id, conversation_id, artifact_type, status, extractor_kind,
				model_name, text_content, page_index, start_ms, end_ms, confidence, metadata, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		`, artifact.ArtifactID, artifact.PartID, artifact.UserID, artifact.ConversationID, artifact.ArtifactType, artifact.Status, nullString(artifact.ExtractorKind), nullString(artifact.ModelName), nullString(artifact.TextContent), artifact.PageIndex, artifact.StartMS, artifact.EndMS, artifact.Confidence, marshalJSON(artifact.Metadata), artifact.CreatedAt, artifact.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *PostgresMediaRepository) ListPartArtifacts(ctx context.Context, userID, conversationID string) ([]domain.MessagePartArtifact, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT artifact_id, part_id, artifact_type, status, extractor_kind, model_name, text_content,
		       page_index, start_ms, end_ms, confidence, metadata, created_at, updated_at
		FROM message_part_artifacts
		WHERE user_id = $1 AND conversation_id = $2
		ORDER BY part_id ASC, start_ms ASC, page_index ASC, created_at ASC
	`, userID, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]domain.MessagePartArtifact, 0)
	for rows.Next() {
		var artifact domain.MessagePartArtifact
		var extractorKind sqlpkg.NullString
		var modelName sqlpkg.NullString
		var textContent sqlpkg.NullString
		var metadataBytes []byte
		artifact.UserID = userID
		artifact.ConversationID = conversationID
		if err := rows.Scan(&artifact.ArtifactID, &artifact.PartID, &artifact.ArtifactType, &artifact.Status, &extractorKind, &modelName, &textContent, &artifact.PageIndex, &artifact.StartMS, &artifact.EndMS, &artifact.Confidence, &metadataBytes, &artifact.CreatedAt, &artifact.UpdatedAt); err != nil {
			return nil, err
		}
		artifact.ExtractorKind = extractorKind.String
		artifact.ModelName = modelName.String
		artifact.TextContent = textContent.String
		_ = json.Unmarshal(metadataBytes, &artifact.Metadata)
		items = append(items, artifact)
	}
	return items, rows.Err()
}
