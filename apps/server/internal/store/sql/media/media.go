package media

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"
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

func (r *InMemoryMediaRepository) PutBlob(ctx context.Context, blob domain.BlobObject) error {
	workspace, err := security.DataScope(ctx, blob.UserID, blob.WorkspaceID)
	if err != nil {
		return err
	}
	blob.WorkspaceID = workspace
	if blob.PartitionID != "" && blob.PartitionID != security.WritePartition(ctx) {
		return domain.ErrAccessDenied
	}
	blob.PartitionID = security.WritePartition(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	if blob.CreatedAt.IsZero() {
		blob.CreatedAt = now
	}
	if blob.UpdatedAt.IsZero() {
		blob.UpdatedAt = now
	}
	if old, ok := r.blobs[recordutilstore.Key(blob.UserID, blob.BlobID)]; ok && (old.WorkspaceID != workspace || old.PartitionID != blob.PartitionID || old.StorageKey != blob.StorageKey || old.DeletedAt != nil) {
		return domain.ErrAccessDenied
	}
	blob.Metadata = recordutilstore.CloneMetadata(blob.Metadata)
	r.blobs[recordutilstore.Key(blob.UserID, blob.BlobID)] = blob
	return nil
}

func (r *InMemoryMediaRepository) GetBlob(ctx context.Context, userID, blobID string) (domain.BlobObject, error) {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return domain.BlobObject{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	blob, ok := r.blobs[recordutilstore.Key(userID, blobID)]
	if !ok || blob.WorkspaceID != workspace || !security.CanReadPartition(ctx, blob.PartitionID) {
		return domain.BlobObject{}, errors.New("blob not found")
	}
	blob.Metadata = recordutilstore.CloneMetadata(blob.Metadata)
	return blob, nil
}

func (r *InMemoryMediaRepository) DeleteBlob(ctx context.Context, userID, blobID string, deletedAt time.Time) error {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	blob, ok := r.blobs[recordutilstore.Key(userID, blobID)]
	if !ok || blob.WorkspaceID != workspace || !security.CanReadPartition(ctx, blob.PartitionID) {
		return nil
	}
	if deletedAt.IsZero() {
		deletedAt = time.Now().UTC()
	}
	blob.DeletedAt = &deletedAt
	blob.UpdatedAt = deletedAt
	r.blobs[recordutilstore.Key(userID, blobID)] = blob
	return nil
}

func (r *InMemoryMediaRepository) ReplaceMessageParts(ctx context.Context, userID, conversationID string, parts []domain.MessagePart) error {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return err
	}
	parts, err = domain.NormalizeMessageParts(userID, workspace, conversationID, parts)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, old := range r.parts[recordutilstore.Key(userID, conversationID)] {
		if old.WorkspaceID != workspace || !security.CanReadPartition(ctx, old.PartitionID) {
			return domain.ErrAccessDenied
		}
	}
	for _, part := range parts {
		part.PartitionID = security.WritePartition(ctx)
		if part.BlobID != "" {
			if blob, ok := r.blobs[recordutilstore.Key(userID, part.BlobID)]; ok && (blob.WorkspaceID != workspace || blob.DeletedAt != nil) {
				return domain.ErrAccessDenied
			}
		}
	}
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
		part.Metadata = recordutilstore.CloneMetadata(part.Metadata)
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
	r.parts[recordutilstore.Key(userID, conversationID)] = items
	return nil
}

func (r *InMemoryMediaRepository) ListMessageParts(ctx context.Context, userID, conversationID string) ([]domain.MessagePart, error) {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []domain.MessagePart{}
	for _, item := range r.parts[recordutilstore.Key(userID, conversationID)] {
		if item.UserID == userID && item.WorkspaceID == workspace && security.CanReadPartition(ctx, item.PartitionID) {
			items = append(items, item)
		}
	}
	for idx := range items {
		items[idx].Metadata = recordutilstore.CloneMetadata(items[idx].Metadata)
	}
	return items, nil
}

func (r *InMemoryMediaRepository) ReplacePartArtifacts(ctx context.Context, userID, conversationID string, artifacts []domain.MessagePartArtifact) error {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return err
	}
	artifacts, err = domain.NormalizePartArtifacts(userID, workspace, conversationID, artifacts)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, old := range r.artifacts[recordutilstore.Key(userID, conversationID)] {
		if old.WorkspaceID != workspace || !security.CanReadPartition(ctx, old.PartitionID) {
			return domain.ErrAccessDenied
		}
	}
	items := make([]domain.MessagePartArtifact, 0, len(artifacts))
	now := time.Now().UTC()
	for _, artifact := range artifacts {
		artifact.PartitionID = security.WritePartition(ctx)
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
		artifact.Metadata = recordutilstore.CloneMetadata(artifact.Metadata)
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
	r.artifacts[recordutilstore.Key(userID, conversationID)] = items
	return nil
}

func (r *InMemoryMediaRepository) ListPartArtifacts(ctx context.Context, userID, conversationID string) ([]domain.MessagePartArtifact, error) {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []domain.MessagePartArtifact{}
	for _, item := range r.artifacts[recordutilstore.Key(userID, conversationID)] {
		if item.UserID == userID && item.WorkspaceID == workspace && security.CanReadPartition(ctx, item.PartitionID) {
			items = append(items, item)
		}
	}
	for idx := range items {
		items[idx].Metadata = recordutilstore.CloneMetadata(items[idx].Metadata)
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
	workspace, err := security.DataScope(ctx, blob.UserID, blob.WorkspaceID)
	if err != nil {
		return err
	}
	blob.WorkspaceID = workspace
	if blob.PartitionID != "" && blob.PartitionID != security.WritePartition(ctx) {
		return domain.ErrAccessDenied
	}
	blob.PartitionID = security.WritePartition(ctx)
	now := time.Now().UTC()
	if blob.CreatedAt.IsZero() {
		blob.CreatedAt = now
	}
	if blob.UpdatedAt.IsZero() {
		blob.UpdatedAt = now
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	if err = scopestore.EnsureMemoryWorkspaceTx(ctx, tx, blob.UserID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO blob_objects (
			blob_id, user_id, source_kind, storage_backend, storage_key, mime_type, sha256,
			size_bytes, original_filename, metadata, deleted_at, created_at, updated_at, workspace_id,partition_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (blob_id) DO UPDATE SET
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
 WHERE blob_objects.user_id=EXCLUDED.user_id AND blob_objects.workspace_id=EXCLUDED.workspace_id AND blob_objects.partition_id=EXCLUDED.partition_id AND blob_objects.storage_key=EXCLUDED.storage_key AND blob_objects.deleted_at IS NULL
	`, blob.BlobID, blob.UserID, string(blob.SourceKind), blob.StorageBackend, blob.StorageKey, blob.MIMEType, blob.SHA256, blob.SizeBytes, blob.OriginalFilename, dbutilstore.MarshalJSON(blob.Metadata), blob.DeletedAt, blob.CreatedAt, blob.UpdatedAt, workspace, blob.PartitionID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrAccessDenied
	}
	return tx.Commit()
}

func (r *PostgresMediaRepository) GetBlob(ctx context.Context, userID, blobID string) (domain.BlobObject, error) {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return domain.BlobObject{}, err
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT source_kind, storage_backend, storage_key, mime_type, sha256, size_bytes,
		       original_filename, metadata, deleted_at, created_at, updated_at,partition_id
		FROM blob_objects
		WHERE `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND blob_id = $1 AND user_id = $2
	`, blobID, userID)

	var blob domain.BlobObject
	var sourceKind string
	var metadataBytes []byte
	var deletedAt sqlpkg.NullTime
	blob.BlobID = blobID
	blob.UserID = userID
	blob.WorkspaceID = workspace
	if err := row.Scan(&sourceKind, &blob.StorageBackend, &blob.StorageKey, &blob.MIMEType, &blob.SHA256, &blob.SizeBytes, &blob.OriginalFilename, &metadataBytes, &deletedAt, &blob.CreatedAt, &blob.UpdatedAt, &blob.PartitionID); err != nil {
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
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return err
	}
	if deletedAt.IsZero() {
		deletedAt = time.Now().UTC()
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE blob_objects
		SET deleted_at = $1, updated_at = $1
		WHERE `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=$4 AND blob_id = $2 AND user_id = $3
	`, deletedAt, blobID, userID, workspace)
	return err
}

func (r *PostgresMediaRepository) ReplaceMessageParts(ctx context.Context, userID, conversationID string, parts []domain.MessagePart) error {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return err
	}
	parts, err = domain.NormalizeMessageParts(userID, workspace, conversationID, parts)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	var parent string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND finalize_status<>'invalidated' FOR SHARE`, conversationID, userID, workspace).Scan(&parent); err != nil {
		return domain.ErrAccessDenied
	}

	if _, err = tx.ExecContext(ctx, `
		DELETE FROM message_parts
		WHERE `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1 AND conversation_id = $2
	`, userID, conversationID); err != nil {
		return err
	}
	if err = validatePartBlobsTx(ctx, tx, userID, workspace, parts); err != nil {
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
				start_ms, end_ms, metadata, created_at, updated_at, workspace_id
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		`, part.PartID, part.UserID, part.ConversationID, part.SequenceNum, part.PartIndex, string(part.Role), string(part.PartType), part.MIMEType, dbutilstore.NullString(part.BlobID), dbutilstore.NullString(part.SourceURL), dbutilstore.NullString(part.ProviderFileID), dbutilstore.NullString(part.TextContent), dbutilstore.NullString(part.RawPayload), part.PageIndex, part.StartMS, part.EndMS, dbutilstore.MarshalJSON(part.Metadata), part.CreatedAt, part.UpdatedAt, workspace); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *PostgresMediaRepository) ListMessageParts(ctx context.Context, userID, conversationID string) ([]domain.MessagePart, error) {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT part_id, sequence_num, part_index, role, part_type, mime_type, blob_id, source_url,
		       provider_file_id, text_content, raw_payload, page_index, start_ms, end_ms, metadata, created_at, updated_at
		FROM message_parts
		WHERE `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1 AND conversation_id = $2
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
		part.WorkspaceID = workspace
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
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return err
	}
	artifacts, err = domain.NormalizePartArtifacts(userID, workspace, conversationID, artifacts)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	var parent string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND finalize_status<>'invalidated' FOR SHARE`, conversationID, userID, workspace).Scan(&parent); err != nil {
		return domain.ErrAccessDenied
	}

	if _, err = tx.ExecContext(ctx, `
		DELETE FROM message_part_artifacts
		WHERE `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1 AND conversation_id = $2
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
				model_name, text_content, page_index, start_ms, end_ms, confidence, metadata, created_at, updated_at, workspace_id
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		`, artifact.ArtifactID, artifact.PartID, artifact.UserID, artifact.ConversationID, artifact.ArtifactType, artifact.Status, dbutilstore.NullString(artifact.ExtractorKind), dbutilstore.NullString(artifact.ModelName), dbutilstore.NullString(artifact.TextContent), artifact.PageIndex, artifact.StartMS, artifact.EndMS, artifact.Confidence, dbutilstore.MarshalJSON(artifact.Metadata), artifact.CreatedAt, artifact.UpdatedAt, workspace); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *PostgresMediaRepository) ListPartArtifacts(ctx context.Context, userID, conversationID string) ([]domain.MessagePartArtifact, error) {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT artifact_id, part_id, artifact_type, status, extractor_kind, model_name, text_content,
		       page_index, start_ms, end_ms, confidence, metadata, created_at, updated_at
		FROM message_part_artifacts
		WHERE `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1 AND conversation_id = $2
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
		artifact.WorkspaceID = workspace
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
