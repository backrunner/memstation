package memorygeneration

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	storeaccess "memstation/apps/server/internal/store/access"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
)

const (
	defaultStructureVersion             = 1
	defaultEmbeddingVersion             = 1
	defaultRetainedInactiveGenerations  = 1
	defaultRetainedSnapshotsPerDialogue = 3
)

type Repository struct {
	db                    runtimeconfig.DBHandle
	defaultEmbeddingModel string

	mu                   sync.RWMutex
	generations          map[string]domain.MemoryGeneration
	activeGenerations    map[string]string
	sourceSnapshots      map[string]domain.MemorySourceSnapshot
	sourceByConversation map[string][]string
	generationSources    map[string]domain.MemoryGenerationSource
	sourcesByGeneration  map[string][]string
}

func NewRepository(db runtimeconfig.DBHandle, defaultEmbeddingModel string) *Repository {
	return &Repository{
		db:                    db,
		defaultEmbeddingModel: strings.TrimSpace(defaultEmbeddingModel),
		generations:           map[string]domain.MemoryGeneration{},
		activeGenerations:     map[string]string{},
		sourceSnapshots:       map[string]domain.MemorySourceSnapshot{},
		sourceByConversation:  map[string][]string{},
		generationSources:     map[string]domain.MemoryGenerationSource{},
		sourcesByGeneration:   map[string][]string{},
	}
}

func (s *Repository) currentDB() *sqlpkg.DB {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Current()
}

func CurrentGenerationID(userID string) string {
	return "gen:current:" + userID
}

func (s *Repository) DurableTasks() bool { return s.currentDB() != nil }

func (s *Repository) EnsureActiveGeneration(ctx context.Context, userID string) (domain.MemoryGeneration, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if strings.TrimSpace(userID) == "" {
		return domain.MemoryGeneration{}, errors.New("user id is required")
	}
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if generationID := s.activeGenerations[security.WorkspaceID(ctx, userID)]; generationID != "" {
			return s.generations[generationID], nil
		}
		now := time.Now().UTC()
		generation := domain.MemoryGeneration{
			GenerationID:      CurrentGenerationID(security.WorkspaceIdentity(ctx, userID)),
			UserID:            userID,
			WorkspaceID:       security.WorkspaceID(ctx, userID),
			Status:            domain.MemoryGenerationStatusActivated,
			Scope:             "current",
			IsActive:          true,
			StructureVersion:  defaultStructureVersion,
			EmbeddingVersion:  defaultEmbeddingVersion,
			EmbeddingModel:    s.defaultEmbeddingModel,
			ActivateOnSuccess: true,
			CreatedAt:         now,
			UpdatedAt:         now,
			ActivatedAt:       timePtr(now),
		}
		s.generations[generation.GenerationID] = generation
		s.activeGenerations[security.WorkspaceID(ctx, userID)] = generation.GenerationID
		return generation, nil
	}

	if generation, ok, err := s.activeGenerationFromDB(ctx, userID); err != nil {
		return domain.MemoryGeneration{}, err
	} else if ok {
		return generation, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	defer rollback(tx)
	if err = lockWorkspaceTx(ctx, tx, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_generations WHERE user_id=$1 AND workspace_id=$2 AND is_active)`, userID, security.WorkspaceID(ctx, userID)).Scan(&exists); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if exists {
		if err = tx.Commit(); err != nil {
			return domain.MemoryGeneration{}, err
		}
		return s.activeGenerationFromDBRequired(ctx, userID)
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
        INSERT INTO memory_generations (
            generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
            embedding_version, embedding_model, activate_on_success, source_count, processed_count,
            embedding_count, failure_count, metadata, created_at, updated_at, activated_at
        , workspace_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,0,0,0,$11,$12,$12,$12, `+storeaccess.WorkspaceSQL(ctx, "$2::text")+`)
        ON CONFLICT (generation_id) DO NOTHING
    `,
		CurrentGenerationID(security.WorkspaceIdentity(ctx, userID)),
		userID,
		string(domain.MemoryGenerationStatusActivated),
		"current",
		nil,
		true,
		defaultStructureVersion,
		defaultEmbeddingVersion,
		s.defaultEmbeddingModel,
		true,
		marshalJSON(map[string]any{"lazy_created": true}),
		now,
	)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.MemoryGeneration{}, err
	}
	return s.activeGenerationFromDBRequired(ctx, userID)
}

func (s *Repository) ResolveGeneration(ctx context.Context, userID, generationID string) (domain.MemoryGeneration, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if strings.TrimSpace(generationID) != "" {
		return s.GetGeneration(ctx, userID, generationID)
	}
	return s.EnsureActiveGeneration(ctx, userID)
}

func (s *Repository) GetActiveGeneration(ctx context.Context, userID string) (domain.MemoryGeneration, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryGeneration{}, err
	}
	return s.EnsureActiveGeneration(ctx, userID)
}

func (s *Repository) GetInProgressGeneration(ctx context.Context, userID string) (domain.MemoryGeneration, bool, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryGeneration{}, false, err
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		var items []domain.MemoryGeneration
		for _, generation := range s.generations {
			if !sameScope(ctx, userID, generation.UserID, generation.WorkspaceID) || generation.IsActive {
				continue
			}
			if generation.Status != domain.MemoryGenerationStatusQueued &&
				generation.Status != domain.MemoryGenerationStatusRunning {
				continue
			}
			items = append(items, generation)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
		if len(items) == 0 {
			return domain.MemoryGeneration{}, false, nil
		}
		return items[0], true, nil
	}
	row := db.QueryRowContext(ctx, `
        SELECT generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
               embedding_version, embedding_model, activate_on_success, source_count, processed_count,
               embedding_count, failure_count, last_error, metadata, created_at, updated_at, activated_at
        FROM memory_generations
	        WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1 AND is_active = FALSE AND status IN ('queued', 'running')
        ORDER BY created_at DESC
        LIMIT 1
    `, userID)
	generation, err := scanGeneration(ctx, row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemoryGeneration{}, false, nil
		}
		return domain.MemoryGeneration{}, false, err
	}
	return generation, true, nil
}

func (s *Repository) GetGeneration(ctx context.Context, userID, generationID string) (domain.MemoryGeneration, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if strings.TrimSpace(generationID) == "" {
		return s.EnsureActiveGeneration(ctx, userID)
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		generation, ok := s.generations[generationID]
		if !ok || !sameScope(ctx, userID, generation.UserID, generation.WorkspaceID) {
			return domain.MemoryGeneration{}, errors.New("memory generation not found")
		}
		return generation, nil
	}
	row := db.QueryRowContext(ctx, `
        SELECT generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
               embedding_version, embedding_model, activate_on_success, source_count, processed_count,
               embedding_count, failure_count, last_error, metadata, created_at, updated_at, activated_at
        FROM memory_generations
        WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND generation_id = $1 AND user_id = $2
    `, generationID, userID)
	generation, err := scanGeneration(ctx, row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemoryGeneration{}, errors.New("memory generation not found")
		}
		return domain.MemoryGeneration{}, err
	}
	return generation, nil
}

func (s *Repository) ListGenerations(ctx context.Context, userID string, limit int) ([]domain.MemoryGeneration, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		items := make([]domain.MemoryGeneration, 0)
		for _, generation := range s.generations {
			if sameScope(ctx, userID, generation.UserID, generation.WorkspaceID) {
				items = append(items, generation)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
		if len(items) > limit {
			items = items[:limit]
		}
		return items, nil
	}
	rows, err := db.QueryContext(ctx, `
        SELECT generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
               embedding_version, embedding_model, activate_on_success, source_count, processed_count,
               embedding_count, failure_count, last_error, metadata, created_at, updated_at, activated_at
        FROM memory_generations
        WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1
        ORDER BY created_at DESC
        LIMIT $2
    `, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.MemoryGeneration, 0)
	for rows.Next() {
		generation, err := scanGeneration(ctx, rows)
		if err != nil {
			return nil, err
		}
		items = append(items, generation)
	}
	return items, rows.Err()
}

func (s *Repository) ListGenerationSources(ctx context.Context, userID, generationID string) ([]domain.MemoryGenerationSource, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	if strings.TrimSpace(generationID) == "" {
		return nil, nil
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		items := make([]domain.MemoryGenerationSource, 0)
		for _, id := range s.sourcesByGeneration[generationID] {
			item := s.generationSources[id]
			if sameScope(ctx, userID, item.UserID, item.WorkspaceID) {
				items = append(items, item)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
		return items, nil
	}
	rows, err := db.QueryContext(ctx, `
        SELECT id, generation_id, user_id, snapshot_id, conversation_id, source_hash, status, last_error, created_at, updated_at, processed_at
        FROM memory_generation_sources
        WHERE `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND generation_id = $1 AND user_id = $2
        ORDER BY created_at ASC
    `, generationID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.MemoryGenerationSource, 0)
	for rows.Next() {
		item, err := scanGenerationSource(ctx, rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Repository) IsCurrentGenerationSource(ctx context.Context, userID, generationID, snapshotID string) (bool, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return false, err
	}
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(generationID) == "" || strings.TrimSpace(snapshotID) == "" {
		return false, nil
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		generation, ok := s.generations[generationID]
		if !ok || !sameScope(ctx, userID, generation.UserID, generation.WorkspaceID) || generation.Status == domain.MemoryGenerationStatusDiscarded {
			return false, nil
		}
		var target domain.MemoryGenerationSource
		found := false
		for _, id := range s.sourcesByGeneration[generationID] {
			item := s.generationSources[id]
			if sameScope(ctx, userID, item.UserID, item.WorkspaceID) && item.SnapshotID == snapshotID {
				target = item
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
		latest := target
		targetVersion := s.sourceSnapshots[target.SnapshotID].SnapshotVersion
		for _, id := range s.sourcesByGeneration[generationID] {
			item := s.generationSources[id]
			if !sameScope(ctx, userID, item.UserID, item.WorkspaceID) || item.ConversationID != target.ConversationID {
				continue
			}
			version := s.sourceSnapshots[item.SnapshotID].SnapshotVersion
			if version > targetVersion || (version == targetVersion && item.UpdatedAt.After(latest.UpdatedAt)) {
				latest = item
				targetVersion = version
			}
		}
		return latest.SnapshotID == snapshotID, nil
	}

	row := db.QueryRowContext(ctx, `
        WITH target AS (
            SELECT gs.conversation_id
            FROM memory_generation_sources gs
            WHERE `+storeaccess.PartitionSQL(ctx, "gs.partition_id")+` AND gs.workspace_id = `+storeaccess.WorkspaceSQL(ctx, "gs.user_id")+` AND gs.generation_id = $1 AND gs.user_id = $2 AND gs.snapshot_id = $3
            LIMIT 1
        )
	        SELECT gs.snapshot_id
	        FROM memory_generation_sources gs
	        JOIN memory_generations generation
	          ON generation.generation_id = gs.generation_id
	         AND generation.user_id = gs.user_id
	        JOIN memory_source_snapshots ss
          ON ss.snapshot_id = gs.snapshot_id
         AND ss.user_id = gs.user_id
        JOIN target t
          ON t.conversation_id = gs.conversation_id
	        WHERE `+storeaccess.PartitionSQL(ctx, "gs.partition_id")+` AND gs.workspace_id = `+storeaccess.WorkspaceSQL(ctx, "gs.user_id")+` AND gs.generation_id = $1 AND gs.user_id = $2 AND generation.status <> 'discarded'
        ORDER BY ss.snapshot_version DESC, gs.updated_at DESC, gs.created_at DESC
        LIMIT 1
    `, generationID, userID, snapshotID)
	var latestSnapshotID string
	if err := row.Scan(&latestSnapshotID); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return latestSnapshotID == snapshotID, nil
}

func (s *Repository) CaptureSourceSnapshot(ctx context.Context, snapshot domain.MemorySourceSnapshot) (domain.MemorySourceSnapshot, bool, error) {
	if _, err := security.DataScope(ctx, snapshot.UserID, snapshot.WorkspaceID); err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	snapshot.WorkspaceID = security.WorkspaceID(ctx, snapshot.UserID)
	var normalizeErr error
	snapshot.Parts, normalizeErr = domain.NormalizeMessageParts(snapshot.UserID, snapshot.WorkspaceID, snapshot.ConversationID, snapshot.Parts)
	if normalizeErr != nil {
		return domain.MemorySourceSnapshot{}, false, normalizeErr
	}
	snapshot.Artifacts, normalizeErr = domain.NormalizePartArtifacts(snapshot.UserID, snapshot.WorkspaceID, snapshot.ConversationID, snapshot.Artifacts)
	if normalizeErr != nil {
		return domain.MemorySourceSnapshot{}, false, normalizeErr
	}
	if strings.TrimSpace(snapshot.UserID) == "" || strings.TrimSpace(snapshot.ConversationID) == "" {
		return domain.MemorySourceSnapshot{}, false, errors.New("conversation user id and conversation id are required")
	}
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		key := sourceConversationKey(ctx, snapshot.UserID, snapshot.ConversationID)
		ids := append([]string(nil), s.sourceByConversation[key]...)
		if len(ids) > 0 {
			latest := s.sourceSnapshots[ids[len(ids)-1]]
			if latest.SourceHash == snapshot.SourceHash {
				return latest, false, nil
			}
			snapshot.SnapshotVersion = latest.SnapshotVersion + 1
		} else {
			snapshot.SnapshotVersion = 1
		}
		now := time.Now().UTC()
		snapshot.SnapshotID = uuid.NewString()
		snapshot.CreatedAt = now
		snapshot.UpdatedAt = now
		s.sourceSnapshots[snapshot.SnapshotID] = snapshot
		s.sourceByConversation[key] = append(s.sourceByConversation[key], snapshot.SnapshotID)
		return snapshot, true, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	defer rollback(tx)
	if err = lockWorkspaceTx(ctx, tx, snapshot.UserID); err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, sourceConversationKey(ctx, snapshot.UserID, snapshot.ConversationID)); err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	var owner, workspace, partition, state, contextMode string
	err = tx.QueryRowContext(ctx, `SELECT user_id,workspace_id,partition_id,finalize_status,context_mode FROM conversations WHERE id=$1 FOR SHARE`, snapshot.ConversationID).Scan(&owner, &workspace, &partition, &state, &contextMode)
	if err != nil && !errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.MemorySourceSnapshot{}, false, err
	}
	if err == nil && (!sameScope(ctx, snapshot.UserID, owner, workspace) || !security.CanReadPartition(ctx, partition) || state == "invalidated") {
		return domain.MemorySourceSnapshot{}, false, domain.ErrAccessDenied
	}
	if snapshot.Metadata == nil {
		snapshot.Metadata = map[string]any{}
	}
	snapshot.Metadata["trusted_context_mode"] = contextMode
	snapshot.Metadata["trusted_partition_id"] = partition
	// A retained source must not race blob cleanup or refer to another scope.
	for _, part := range snapshot.Parts {
		if part.BlobID != "" {
			var id string
			err = tx.QueryRowContext(ctx, `SELECT blob_id FROM blob_objects WHERE blob_id=$1 AND user_id=$2 AND workspace_id=$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND deleted_at IS NULL FOR SHARE`, part.BlobID, snapshot.UserID, snapshot.WorkspaceID).Scan(&id)
			if err != nil {
				return domain.MemorySourceSnapshot{}, false, domain.ErrAccessDenied
			}
		}
	}
	latest, latestFound, err := latestSnapshotHeader(ctx, tx, snapshot.UserID, snapshot.ConversationID)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	if latestFound && latest.SourceHash == snapshot.SourceHash {
		if err = tx.Commit(); err != nil {
			return domain.MemorySourceSnapshot{}, false, err
		}
		loaded, _, err := s.GetSourceSnapshot(ctx, snapshot.UserID, latest.SnapshotID)
		return loaded, false, err
	}
	if latestFound {
		snapshot.SnapshotVersion = latest.SnapshotVersion + 1
	} else {
		snapshot.SnapshotVersion = 1
	}
	snapshot.SnapshotID = uuid.NewString()
	now := time.Now().UTC()
	snapshot.CreatedAt = now
	snapshot.UpdatedAt = now

	_, err = tx.ExecContext(ctx, `
        INSERT INTO memory_source_snapshots (
            snapshot_id, user_id, conversation_id, snapshot_version, source_hash, request_format,
            upstream_provider, mode, finalize_status, metadata, usage, message_count,
            message_parts, part_artifacts, created_at, updated_at
        , workspace_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15, `+storeaccess.WorkspaceSQL(ctx, "$2::text")+`)
    `,
		snapshot.SnapshotID,
		snapshot.UserID,
		snapshot.ConversationID,
		snapshot.SnapshotVersion,
		snapshot.SourceHash,
		snapshot.RequestFormat,
		string(snapshot.Provider),
		string(snapshot.Mode),
		string(snapshot.FinalizeStatus),
		marshalJSON(snapshot.Metadata),
		marshalJSON(snapshot.Usage),
		snapshot.MessageCount,
		marshalJSON(snapshot.Parts),
		marshalJSON(snapshot.Artifacts),
		now,
	)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	for idx, message := range snapshot.Messages {
		_, err = tx.ExecContext(ctx, `
            INSERT INTO memory_source_messages (
                id, snapshot_id, user_id, conversation_id, snapshot_version, sequence_num, role,
                content_blocks, text_content, tool_calls, tool_result, provider_payload, created_at, updated_at
            , workspace_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13, `+storeaccess.WorkspaceSQL(ctx, "$3::text")+`)
        `,
			fmt.Sprintf("%s:%03d", snapshot.SnapshotID, idx),
			snapshot.SnapshotID,
			snapshot.UserID,
			snapshot.ConversationID,
			snapshot.SnapshotVersion,
			idx,
			string(message.Role),
			marshalJSON(message.Blocks),
			message.Text,
			marshalJSON(message.ToolCalls),
			marshalJSON(domain.StoredToolResultPayload(message.ToolResults)),
			marshalJSON(snapshotMessageMetadata(message)),
			now,
		)
		if err != nil {
			return domain.MemorySourceSnapshot{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *Repository) GetSourceSnapshot(ctx context.Context, userID, snapshotID string) (domain.MemorySourceSnapshot, bool, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	if strings.TrimSpace(snapshotID) == "" {
		return domain.MemorySourceSnapshot{}, false, nil
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		snapshot, ok := s.sourceSnapshots[snapshotID]
		if !ok || !sameScope(ctx, userID, snapshot.UserID, snapshot.WorkspaceID) {
			return domain.MemorySourceSnapshot{}, false, nil
		}
		return snapshot, true, nil
	}
	row := db.QueryRowContext(ctx, `
        SELECT snapshot_id, user_id, conversation_id, snapshot_version, source_hash, request_format,
               upstream_provider, mode, finalize_status, metadata, usage, message_count,
               message_parts, part_artifacts, created_at, updated_at
        FROM memory_source_snapshots
        WHERE content_status='active' AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND snapshot_id = $1 AND user_id = $2
    `, snapshotID, userID)
	snapshot, err := scanSnapshot(ctx, row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemorySourceSnapshot{}, false, nil
		}
		return domain.MemorySourceSnapshot{}, false, err
	}
	messages, err := s.loadSnapshotMessages(ctx, userID, snapshot.SnapshotID)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	snapshot.Messages = messages
	return snapshot, true, nil
}

func (s *Repository) GetLatestSourceSnapshot(ctx context.Context, userID, conversationID string) (domain.MemorySourceSnapshot, bool, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	if s.currentDB() == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		ids := s.sourceByConversation[sourceConversationKey(ctx, userID, conversationID)]
		if len(ids) == 0 {
			return domain.MemorySourceSnapshot{}, false, nil
		}
		return s.sourceSnapshots[ids[len(ids)-1]], true, nil
	}
	header, ok, err := s.latestSnapshotHeaderFromDB(ctx, userID, conversationID)
	if err != nil || !ok {
		return domain.MemorySourceSnapshot{}, ok, err
	}
	return s.GetSourceSnapshot(ctx, userID, header.SnapshotID)
}

func (s *Repository) ListSourceSnapshots(ctx context.Context, userID, conversationID string, limit int) ([]domain.MemorySourceSnapshot, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		ids := append([]string(nil), s.sourceByConversation[sourceConversationKey(ctx, userID, conversationID)]...)
		items := make([]domain.MemorySourceSnapshot, 0, len(ids))
		for i := len(ids) - 1; i >= 0; i-- {
			items = append(items, s.sourceSnapshots[ids[i]])
			if len(items) >= limit {
				break
			}
		}
		return items, nil
	}
	rows, err := db.QueryContext(ctx, `
        SELECT snapshot_id, user_id, conversation_id, snapshot_version, source_hash, request_format,
               upstream_provider, mode, finalize_status, metadata, usage, message_count,
               message_parts, part_artifacts, created_at, updated_at
        FROM memory_source_snapshots
        WHERE content_status='active' AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1 AND conversation_id = $2
        ORDER BY snapshot_version DESC
        LIMIT $3
    `, userID, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.MemorySourceSnapshot, 0)
	for rows.Next() {
		item, err := scanSnapshot(ctx, rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Repository) SourceStatsForUser(ctx context.Context, userID string) (domain.MemorySourceStats, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemorySourceStats{}, err
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		stats := domain.MemorySourceStats{}
		for _, snapshot := range s.sourceSnapshots {
			if !sameScope(ctx, userID, snapshot.UserID, snapshot.WorkspaceID) {
				continue
			}
			stats.SnapshotCount++
			if stats.LatestSnapshotAt == nil || snapshot.CreatedAt.After(*stats.LatestSnapshotAt) {
				ts := snapshot.CreatedAt
				stats.LatestSnapshotAt = &ts
			}
		}
		return stats, nil
	}
	row := db.QueryRowContext(ctx, `
        SELECT COUNT(*), MAX(created_at)
        FROM memory_source_snapshots
        WHERE content_status='active' AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1
    `, userID)
	var count int
	var latest sqlpkg.NullTime
	if err := row.Scan(&count, &latest); err != nil {
		return domain.MemorySourceStats{}, err
	}
	stats := domain.MemorySourceStats{SnapshotCount: count}
	if latest.Valid {
		stats.LatestSnapshotAt = &latest.Time
	}
	return stats, nil
}

func (s *Repository) CreateRebuildGeneration(ctx context.Context, input domain.CreateMemoryRebuildInput) (domain.MemoryGeneration, []domain.MemoryGenerationSource, error) {
	if _, err := security.DataScope(ctx, input.UserID, input.WorkspaceID); err != nil {
		return domain.MemoryGeneration{}, nil, err
	}
	input.WorkspaceID = security.WorkspaceID(ctx, input.UserID)
	if strings.TrimSpace(input.UserID) == "" {
		return domain.MemoryGeneration{}, nil, errors.New("user id is required")
	}
	if input.Scope == "" {
		if strings.TrimSpace(input.ConversationID) != "" {
			input.Scope = "conversation"
		} else {
			input.Scope = "user"
		}
	}
	if input.Scope != "user" && input.Scope != "conversation" {
		return domain.MemoryGeneration{}, nil, fmt.Errorf("unsupported rebuild scope: %s", input.Scope)
	}
	if input.Scope == "conversation" && strings.TrimSpace(input.ConversationID) == "" {
		return domain.MemoryGeneration{}, nil, errors.New("conversation id is required for conversation-scoped rebuild")
	}
	if input.Scope == "conversation" && input.ActivateOnSuccess {
		return domain.MemoryGeneration{}, nil, errors.New("conversation-scoped rebuilds cannot activate a user-wide generation; use user scope rebuild to switch active memory")
	}
	if existing, ok, err := s.GetInProgressGeneration(ctx, input.UserID); err != nil {
		return domain.MemoryGeneration{}, nil, err
	} else if ok {
		return domain.MemoryGeneration{}, nil, fmt.Errorf("generation %s is already in progress", existing.GenerationID)
	}

	active, err := s.EnsureActiveGeneration(ctx, input.UserID)
	if err != nil {
		return domain.MemoryGeneration{}, nil, err
	}

	snapshots, err := s.latestSnapshotsForScope(ctx, input.UserID, input.Scope, input.ConversationID)
	if err != nil {
		return domain.MemoryGeneration{}, nil, err
	}
	if len(snapshots) == 0 && s.currentDB() == nil {
		return domain.MemoryGeneration{}, nil, errors.New("no retained source snapshots are available for rebuild")
	}

	now := time.Now().UTC()
	generation := domain.MemoryGeneration{
		GenerationID:      uuid.NewString(),
		UserID:            input.UserID,
		WorkspaceID:       security.WorkspaceID(ctx, input.UserID),
		Status:            domain.MemoryGenerationStatusQueued,
		Scope:             input.Scope,
		ConversationID:    strings.TrimSpace(input.ConversationID),
		IsActive:          false,
		StructureVersion:  active.StructureVersion,
		EmbeddingVersion:  max(active.EmbeddingVersion+1, defaultEmbeddingVersion),
		EmbeddingModel:    firstNonEmpty(strings.TrimSpace(input.EmbeddingModelOverride), active.EmbeddingModel, s.defaultEmbeddingModel),
		ActivateOnSuccess: true,
		SourceCount:       len(snapshots),
		Metadata: map[string]any{
			"active_generation_id": active.GenerationID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if !input.ActivateOnSuccess {
		generation.ActivateOnSuccess = false
	}

	sourceItems := make([]domain.MemoryGenerationSource, 0, len(snapshots))
	for _, snapshot := range snapshots {
		sourceItems = append(sourceItems, domain.MemoryGenerationSource{
			ID:             uuid.NewString(),
			GenerationID:   generation.GenerationID,
			UserID:         input.UserID,
			WorkspaceID:    security.WorkspaceID(ctx, input.UserID),
			SnapshotID:     snapshot.SnapshotID,
			ConversationID: snapshot.ConversationID,
			SourceHash:     snapshot.SourceHash,
			Status:         domain.MemoryGenerationSourceStatusQueued,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}

	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, existing := range s.generations {
			if sameScope(ctx, input.UserID, existing.UserID, existing.WorkspaceID) && !existing.IsActive && (existing.Status == domain.MemoryGenerationStatusQueued || existing.Status == domain.MemoryGenerationStatusRunning) {
				return domain.MemoryGeneration{}, nil, errors.New("memory rebuild already in progress")
			}
		}
		s.generations[generation.GenerationID] = generation
		for _, item := range sourceItems {
			s.generationSources[item.ID] = item
			s.sourcesByGeneration[generation.GenerationID] = append(s.sourcesByGeneration[generation.GenerationID], item.ID)
		}
		return generation, sourceItems, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryGeneration{}, nil, err
	}
	defer rollback(tx)
	if err = lockWorkspaceTx(ctx, tx, input.UserID); err != nil {
		return domain.MemoryGeneration{}, nil, err
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_generations WHERE user_id=$1 AND workspace_id=$2 AND NOT is_active AND status IN ('queued','running'))`, input.UserID, input.WorkspaceID).Scan(&pending); err != nil {
		return domain.MemoryGeneration{}, nil, err
	}
	if pending {
		return domain.MemoryGeneration{}, nil, errors.New("memory rebuild already in progress")
	}
	_, err = tx.ExecContext(ctx, `
        INSERT INTO memory_generations (
            generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
            embedding_version, embedding_model, activate_on_success, source_count, processed_count,
            embedding_count, failure_count, last_error, metadata, created_at, updated_at, activated_at
        , workspace_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,0,0,0,'',$12,$13,$13,NULL, `+storeaccess.WorkspaceSQL(ctx, "$2::text")+`)
    `,
		generation.GenerationID,
		generation.UserID,
		string(generation.Status),
		generation.Scope,
		nullString(generation.ConversationID),
		false,
		generation.StructureVersion,
		generation.EmbeddingVersion,
		generation.EmbeddingModel,
		generation.ActivateOnSuccess,
		generation.SourceCount,
		marshalJSON(generation.Metadata),
		now,
	)
	if err != nil {
		return domain.MemoryGeneration{}, nil, err
	}
	if err = prepareCarriesTx(ctx, tx, &generation, active.GenerationID); err != nil {
		return domain.MemoryGeneration{}, nil, err
	}
	if generation.SourceCount == 0 {
		return domain.MemoryGeneration{}, nil, errors.New("no retained source snapshots or authoritative records available")
	}
	for _, item := range sourceItems {
		_, err = tx.ExecContext(ctx, `
            INSERT INTO memory_generation_sources (
                id, generation_id, user_id, snapshot_id, conversation_id, source_hash, status,
                last_error, created_at, updated_at, processed_at
            , workspace_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,NULL, `+storeaccess.WorkspaceSQL(ctx, "$3::text")+`)
        `,
			item.ID,
			item.GenerationID,
			item.UserID,
			item.SnapshotID,
			item.ConversationID,
			item.SourceHash,
			string(item.Status),
			item.LastError,
			now,
		)
		if err != nil {
			return domain.MemoryGeneration{}, nil, err
		}
		if err = enqueueRebuildSourceTx(ctx, tx, item); err != nil {
			return domain.MemoryGeneration{}, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.MemoryGeneration{}, nil, err
	}
	return generation, sourceItems, nil
}

func (s *Repository) ActivateGeneration(ctx context.Context, userID, generationID string) (domain.MemoryGeneration, error) {
	return s.activateGeneration(ctx, userID, generationID, false)
}

// ActivateCompletedGeneration is the worker entry point: a replayed activation
// must not reactivate a historical generation after another one replaced it.
func (s *Repository) ActivateCompletedGeneration(ctx context.Context, userID, generationID string) (domain.MemoryGeneration, error) {
	return s.activateGeneration(ctx, userID, generationID, true)
}

func (s *Repository) activateGeneration(ctx context.Context, userID, generationID string, automatic bool) (domain.MemoryGeneration, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if strings.TrimSpace(generationID) == "" {
		return domain.MemoryGeneration{}, errors.New("generation id is required")
	}
	generation, err := s.GetGeneration(ctx, userID, generationID)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	if automatic && (generation.Status != domain.MemoryGenerationStatusCompleted || !generation.ActivateOnSuccess) {
		return generation, nil
	}
	if !canActivateGeneration(generation) {
		return domain.MemoryGeneration{}, errors.New("memory generation is not ready for activation")
	}
	if generation.Scope == "conversation" {
		return domain.MemoryGeneration{}, errors.New("conversation-scoped rebuilds cannot be activated as the user-wide active generation")
	}
	now := time.Now().UTC()
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		generation, ok := s.generations[generationID]
		if !ok || !sameScope(ctx, userID, generation.UserID, generation.WorkspaceID) {
			return domain.MemoryGeneration{}, errors.New("memory generation not found")
		}
		if automatic && (generation.Status != domain.MemoryGenerationStatusCompleted || !generation.ActivateOnSuccess) {
			return generation, nil
		}
		if !canActivateGeneration(generation) {
			return domain.MemoryGeneration{}, errors.New("memory generation is not ready for activation")
		}
		for id, item := range s.generations {
			if !sameScope(ctx, userID, item.UserID, item.WorkspaceID) {
				continue
			}
			item.IsActive = id == generationID
			if item.IsActive {
				item.Status = domain.MemoryGenerationStatusActivated
				item.ActivatedAt = timePtr(now)
			}
			item.UpdatedAt = now
			s.generations[id] = item
		}
		s.activeGenerations[security.WorkspaceID(ctx, userID)] = generationID
		s.pruneInMemoryHistoryLocked(ctx, userID, generationID, defaultRetainedInactiveGenerations, defaultRetainedSnapshotsPerDialogue)
		return s.generations[generationID], nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	defer rollback(tx)
	if err = lockWorkspaceTx(ctx, tx, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	generation, err = lockGenerationTx(ctx, tx, userID, generationID)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	if automatic && (generation.Status != domain.MemoryGenerationStatusCompleted || !generation.ActivateOnSuccess) {
		return generation, nil
	}
	if !canActivateGeneration(generation) {
		return domain.MemoryGeneration{}, errors.New("memory generation is not ready for activation")
	}
	// Serialize against writes to the current generation before validating coverage.
	rows, err := tx.QueryContext(ctx, `SELECT generation_id FROM memory_generations WHERE user_id=$1 AND workspace_id=$2 AND is_active AND generation_id<>$3 FOR UPDATE`, userID, generation.WorkspaceID, generationID)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	if err = rows.Close(); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if !generation.IsActive {
		if err = validateActivationTx(ctx, tx, generation); err != nil {
			return domain.MemoryGeneration{}, err
		}
	}
	if err = activateCarriesTx(ctx, tx, generation); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if _, err = tx.ExecContext(ctx, `
        UPDATE memory_generations
        SET is_active = FALSE, updated_at = $1
        WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $2 AND is_active = TRUE
    `, now, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if _, err = tx.ExecContext(ctx, `
        UPDATE memory_generations
        SET is_active = TRUE, status = $1, activated_at = $2, updated_at = $2
        WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND generation_id = $3 AND user_id = $4
    `, string(domain.MemoryGenerationStatusActivated), now, generationID, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	conversationMetadata := string(marshalJSON(map[string]any{"memory_generation_id": generationID}))
	if generation.GenerationID == CurrentGenerationID(security.WorkspaceIdentity(ctx, userID)) || generation.SourceCount == 0 {
		if _, err = tx.ExecContext(ctx, `
        UPDATE conversations
        SET metadata = COALESCE(metadata, '{}'::jsonb) || $1::jsonb,
            updated_at = $2
        WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $3
    `, conversationMetadata, now, userID); err != nil {
			return domain.MemoryGeneration{}, err
		}
	} else if _, err = tx.ExecContext(ctx, `
        UPDATE conversations
        SET metadata = COALESCE(metadata, '{}'::jsonb) || $1::jsonb,
            updated_at = $2
        WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $3
          AND id IN (
              SELECT DISTINCT conversation_id
              FROM memory_generation_sources
              WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND generation_id = $4 AND user_id = $3
          )
	`, conversationMetadata, now, userID, generationID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if err = pruneGenerationHistoryTx(ctx, tx, userID, generationID, defaultRetainedInactiveGenerations, now); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if err = pruneSourceSnapshotsTx(ctx, tx, userID, defaultRetainedSnapshotsPerDialogue); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MemoryGeneration{}, err
	}
	return s.GetGeneration(ctx, userID, generationID)
}

func (s *Repository) DiscardGeneration(ctx context.Context, userID, generationID string) (domain.MemoryGeneration, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryGeneration{}, err
	}
	generationID = strings.TrimSpace(generationID)
	if userID == "" || generationID == "" {
		return domain.MemoryGeneration{}, errors.New("user id and generation id are required")
	}
	if generationID == CurrentGenerationID(security.WorkspaceIdentity(ctx, userID)) {
		return domain.MemoryGeneration{}, errors.New("the bootstrap generation cannot be discarded")
	}
	generation, err := s.GetGeneration(ctx, userID, generationID)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	if generation.IsActive {
		return domain.MemoryGeneration{}, errors.New("the active generation cannot be discarded")
	}
	now := time.Now().UTC()
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		generation = s.generations[generationID]
		if generation.IsActive {
			return domain.MemoryGeneration{}, errors.New("the active generation cannot be discarded")
		}
		generation.Status = domain.MemoryGenerationStatusDiscarded
		generation.UpdatedAt = now
		generation.Metadata = cloneMap(generation.Metadata)
		generation.Metadata["discarded_at"] = now.Format(time.RFC3339)
		s.generations[generationID] = generation
		for _, sourceID := range s.sourcesByGeneration[generationID] {
			delete(s.generationSources, sourceID)
		}
		delete(s.sourcesByGeneration, generationID)
		return generation, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	defer rollback(tx)
	if err = lockWorkspaceTx(ctx, tx, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `
		SELECT is_active FROM memory_generations
		WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND generation_id = $1 AND user_id = $2
		FOR UPDATE
	`, generationID, userID).Scan(&active); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if active {
		return domain.MemoryGeneration{}, errors.New("the active generation cannot be discarded")
	}
	if err = deleteGenerationDataTx(ctx, tx, userID, generationID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM memory_generation_sources WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND generation_id = $1 AND user_id = $2`, generationID, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE memory_generations
		SET status = $1,
		    metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('discarded_at', $2::timestamptz),
		    updated_at = $2
		WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND generation_id = $3 AND user_id = $4
	`, string(domain.MemoryGenerationStatusDiscarded), now, generationID, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if err = pruneSourceSnapshotsTx(ctx, tx, userID, defaultRetainedSnapshotsPerDialogue); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.MemoryGeneration{}, err
	}
	return s.GetGeneration(ctx, userID, generationID)
}

func (s *Repository) pruneInMemoryHistoryLocked(ctx context.Context, userID, activeGenerationID string, retainGenerations, retainSnapshots int) {
	items := make([]domain.MemoryGeneration, 0)
	for _, generation := range s.generations {
		if sameScope(ctx, userID, generation.UserID, generation.WorkspaceID) && generation.GenerationID != activeGenerationID && !generation.IsActive && generation.Status == domain.MemoryGenerationStatusActivated {
			items = append(items, generation)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ActivatedAt == nil {
			return false
		}
		if items[j].ActivatedAt == nil {
			return true
		}
		return items[i].ActivatedAt.After(*items[j].ActivatedAt)
	})
	for idx := max(retainGenerations, 0); idx < len(items); idx++ {
		generation := items[idx]
		generation.Status = domain.MemoryGenerationStatusDiscarded
		generation.UpdatedAt = time.Now().UTC()
		generation.Metadata = cloneMap(generation.Metadata)
		generation.Metadata["discarded_at"] = generation.UpdatedAt.Format(time.RFC3339)
		s.generations[generation.GenerationID] = generation
		for _, sourceID := range s.sourcesByGeneration[generation.GenerationID] {
			delete(s.generationSources, sourceID)
		}
		delete(s.sourcesByGeneration, generation.GenerationID)
	}

	if retainSnapshots <= 0 {
		return
	}
	referenced := map[string]struct{}{}
	for _, source := range s.generationSources {
		referenced[source.SnapshotID] = struct{}{}
	}
	for key, ids := range s.sourceByConversation {
		if len(ids) <= retainSnapshots || !sameScope(ctx, userID, s.sourceSnapshots[ids[0]].UserID, s.sourceSnapshots[ids[0]].WorkspaceID) {
			continue
		}
		keepFrom := len(ids) - retainSnapshots
		kept := append([]string(nil), ids[keepFrom:]...)
		for _, snapshotID := range ids[:keepFrom] {
			if _, ok := referenced[snapshotID]; ok {
				kept = append([]string{snapshotID}, kept...)
				continue
			}
			delete(s.sourceSnapshots, snapshotID)
		}
		s.sourceByConversation[key] = kept
	}
}

func pruneGenerationHistoryTx(ctx context.Context, tx *sqlpkg.Tx, userID, activeGenerationID string, retain int, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT generation_id
		FROM memory_generations
		WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1 AND generation_id <> $2 AND is_active = FALSE AND status = 'activated'
		ORDER BY activated_at DESC NULLS LAST, created_at DESC
		OFFSET $3
	`, userID, activeGenerationID, max(retain, 0))
	if err != nil {
		return err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, generationID := range ids {
		if _, err = lockGenerationTx(ctx, tx, userID, generationID); err != nil {
			return err
		}
		if err = deleteGenerationDataTx(ctx, tx, userID, generationID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM memory_generation_sources WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND generation_id = $1 AND user_id = $2`, generationID, userID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE memory_generations
			SET status = $1,
			    metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('discarded_at', $2::timestamptz, 'discard_reason', 'retention'),
			    updated_at = $2
			WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND generation_id = $3 AND user_id = $4
		`, string(domain.MemoryGenerationStatusDiscarded), now, generationID, userID); err != nil {
			return err
		}
	}
	return nil
}

func deleteGenerationDataTx(ctx context.Context, tx *sqlpkg.Tx, userID, generationID string) error {
	nodeSet := `SELECT id FROM memory_nodes WHERE user_id=$1 AND generation_id=$2 AND workspace_id=` + storeaccess.WorkspaceSQL(ctx, "user_id") + ``
	queries := []string{
		`DELETE FROM memory_embeddings WHERE user_id=$1 AND generation_id=$2 AND workspace_id=` + storeaccess.WorkspaceSQL(ctx, "user_id"),
		`DELETE FROM memory_generation_carries WHERE user_id=$1 AND generation_id=$2 AND workspace_id=` + storeaccess.WorkspaceSQL(ctx, "user_id"),
		`DELETE FROM memory_keywords WHERE user_id=$1 AND summary_id IN (SELECT entity_id FROM memory_nodes WHERE id IN (` + nodeSet + `))`,
		`DELETE FROM memory_evidences WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`DELETE FROM memory_relations WHERE user_id=$1 AND (src_node_id IN (` + nodeSet + `) OR dst_node_id IN (` + nodeSet + `))`,
		`DELETE FROM memory_embeddings WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`DELETE FROM memory_items WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`DELETE FROM facts WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`DELETE FROM impressions WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`DELETE FROM memory_summaries WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`DELETE FROM conversation_segments WHERE user_id=$1 AND generation_id=$2 AND EXISTS (SELECT 1 FROM conversations c WHERE c.user_id=conversation_segments.user_id AND c.id=conversation_segments.conversation_id AND c.workspace_id = ` + storeaccess.WorkspaceSQL(ctx, "c.user_id") + `)`,
		`DELETE FROM memory_nodes WHERE id IN (` + nodeSet + `)`,
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query, userID, generationID); err != nil {
			return err
		}
	}
	return nil
}

func pruneSourceSnapshotsTx(ctx context.Context, tx *sqlpkg.Tx, userID string, retain int) error {
	if retain <= 0 {
		return nil
	}
	candidates := `
		SELECT snapshot_id FROM (
			SELECT snapshot_id,
			       ROW_NUMBER() OVER (PARTITION BY conversation_id ORDER BY snapshot_version DESC, updated_at DESC) AS snapshot_rank
			FROM memory_source_snapshots
			WHERE content_status='active' AND ` + storeaccess.PartitionSQL(ctx, "partition_id") + ` AND workspace_id=` + storeaccess.WorkspaceSQL(ctx, "user_id") + ` AND user_id = $1
		) ranked
		WHERE snapshot_rank > $2
		  AND NOT EXISTS (
			SELECT 1 FROM memory_generation_sources source
			WHERE source.workspace_id = ` + storeaccess.WorkspaceSQL(ctx, "source.user_id") + ` AND source.snapshot_id = ranked.snapshot_id AND source.user_id = $1
		  )`
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_source_messages WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND snapshot_id IN (`+candidates+`)`, userID, retain); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM memory_source_snapshots WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND snapshot_id IN (`+candidates+`)`, userID, retain)
	return err
}

func (s *Repository) latestSnapshotsForScope(ctx context.Context, userID, scope, conversationID string) ([]domain.MemorySourceSnapshot, error) {
	switch scope {
	case "conversation":
		snapshot, ok, err := s.GetLatestSourceSnapshot(ctx, userID, conversationID)
		if err != nil || !ok {
			return nil, err
		}
		return []domain.MemorySourceSnapshot{snapshot}, nil
	default:
		db := s.currentDB()
		if db == nil {
			s.mu.RLock()
			defer s.mu.RUnlock()
			items := make([]domain.MemorySourceSnapshot, 0)
			for _, ids := range s.sourceByConversation {
				if len(ids) == 0 || !sameScope(ctx, userID, s.sourceSnapshots[ids[0]].UserID, s.sourceSnapshots[ids[0]].WorkspaceID) {
					continue
				}
				items = append(items, s.sourceSnapshots[ids[len(ids)-1]])
			}
			sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
			return items, nil
		}
		rows, err := db.QueryContext(ctx, `
            SELECT DISTINCT ON (conversation_id)
                snapshot_id, user_id, conversation_id, snapshot_version, source_hash, request_format,
                upstream_provider, mode, finalize_status, metadata, usage, message_count,
                message_parts, part_artifacts, created_at, updated_at
            FROM memory_source_snapshots
            WHERE content_status='active' AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1
            ORDER BY conversation_id, snapshot_version DESC
        `, userID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := make([]domain.MemorySourceSnapshot, 0)
		for rows.Next() {
			item, err := scanSnapshot(ctx, rows)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
		return items, rows.Err()
	}
}

func (s *Repository) activeGenerationFromDB(ctx context.Context, userID string) (domain.MemoryGeneration, bool, error) {
	db := s.currentDB()
	if db == nil {
		return domain.MemoryGeneration{}, false, nil
	}
	row := db.QueryRowContext(ctx, `
        SELECT generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
               embedding_version, embedding_model, activate_on_success, source_count, processed_count,
               embedding_count, failure_count, last_error, metadata, created_at, updated_at, activated_at
        FROM memory_generations
        WHERE workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1 AND is_active = TRUE
        LIMIT 1
    `, userID)
	generation, err := scanGeneration(ctx, row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemoryGeneration{}, false, nil
		}
		return domain.MemoryGeneration{}, false, err
	}
	return generation, true, nil
}

func (s *Repository) activeGenerationFromDBRequired(ctx context.Context, userID string) (domain.MemoryGeneration, error) {
	generation, ok, err := s.activeGenerationFromDB(ctx, userID)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	if !ok {
		return domain.MemoryGeneration{}, errors.New("active memory generation not found")
	}
	return generation, nil
}

func (s *Repository) latestSnapshotHeaderFromDB(ctx context.Context, userID, conversationID string) (domain.MemorySourceSnapshot, bool, error) {
	return latestSnapshotHeader(ctx, s.currentDB(), userID, conversationID)
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sqlpkg.Row
}

func latestSnapshotHeader(ctx context.Context, db rowQueryer, userID, conversationID string) (domain.MemorySourceSnapshot, bool, error) {
	row := db.QueryRowContext(ctx, `
        SELECT snapshot_id, user_id, conversation_id, snapshot_version, source_hash, request_format,
               upstream_provider, mode, finalize_status, metadata, usage, message_count,
               message_parts, part_artifacts, created_at, updated_at
        FROM memory_source_snapshots
        WHERE content_status='active' AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND user_id = $1 AND conversation_id = $2
        ORDER BY snapshot_version DESC
        LIMIT 1
    `, userID, conversationID)
	snapshot, err := scanSnapshot(ctx, row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemorySourceSnapshot{}, false, nil
		}
		return domain.MemorySourceSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *Repository) loadSnapshotMessages(ctx context.Context, userID, snapshotID string) ([]domain.UnifiedMessage, error) {
	db := s.currentDB()
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
        SELECT role, text_content, content_blocks, tool_calls, tool_result, provider_payload
        FROM memory_source_messages
        WHERE `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+` AND snapshot_id = $1 AND user_id=$2 AND EXISTS(SELECT 1 FROM memory_source_snapshots parent WHERE parent.snapshot_id=$1 AND parent.user_id=$2 AND parent.content_status='active')
        ORDER BY sequence_num ASC
    `, snapshotID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.UnifiedMessage, 0)
	for rows.Next() {
		var role string
		var text string
		var blocksBytes []byte
		var toolCallsBytes []byte
		var toolResultBytes []byte
		var metadataBytes []byte
		if err := rows.Scan(&role, &text, &blocksBytes, &toolCallsBytes, &toolResultBytes, &metadataBytes); err != nil {
			return nil, err
		}
		message := domain.UnifiedMessage{Role: domain.MessageRole(role), Text: text}
		_ = json.Unmarshal(blocksBytes, &message.Blocks)
		_ = json.Unmarshal(toolCallsBytes, &message.ToolCalls)
		if toolResult, ok := domain.DecodeStoredToolResult(toolResultBytes); ok {
			message.ToolResults = []domain.ToolResult{toolResult}
		}
		_ = json.Unmarshal(metadataBytes, &message.Metadata)
		if raw, ok := message.Metadata["trusted_source"]; ok {
			payload, err := json.Marshal(raw)
			if err != nil {
				return nil, err
			}
			var source domain.MessageSource
			if err = json.Unmarshal(payload, &source); err != nil {
				return nil, err
			}
			if source.WorkspaceID != security.WorkspaceID(ctx, userID) {
				return nil, domain.ErrAccessDenied
			}
			message.Source = &source
			delete(message.Metadata, "trusted_source")
		}

		items = append(items, message)
	}
	return items, rows.Err()
}

func scanGeneration(ctx context.Context, scanner interface{ Scan(dest ...any) error }) (domain.MemoryGeneration, error) {
	var generation domain.MemoryGeneration
	var conversationID sqlpkg.NullString
	var lastError sqlpkg.NullString
	var metadataBytes []byte
	var activatedAt sqlpkg.NullTime
	err := scanner.Scan(
		&generation.GenerationID,
		&generation.UserID,
		&generation.Status,
		&generation.Scope,
		&conversationID,
		&generation.IsActive,
		&generation.StructureVersion,
		&generation.EmbeddingVersion,
		&generation.EmbeddingModel,
		&generation.ActivateOnSuccess,
		&generation.SourceCount,
		&generation.ProcessedCount,
		&generation.EmbeddingCount,
		&generation.FailureCount,
		&lastError,
		&metadataBytes,
		&generation.CreatedAt,
		&generation.UpdatedAt,
		&activatedAt,
	)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	if conversationID.Valid {
		generation.ConversationID = conversationID.String
	}
	if lastError.Valid {
		generation.LastError = lastError.String
	}
	_ = json.Unmarshal(metadataBytes, &generation.Metadata)
	if activatedAt.Valid {
		generation.ActivatedAt = &activatedAt.Time
	}
	generation.WorkspaceID = security.WorkspaceID(ctx, generation.UserID)
	return generation, nil
}

func scanSnapshot(ctx context.Context, scanner interface{ Scan(dest ...any) error }) (domain.MemorySourceSnapshot, error) {
	var snapshot domain.MemorySourceSnapshot
	var provider string
	var mode string
	var finalizeStatus string
	var metadataBytes []byte
	var usageBytes []byte
	var partsBytes []byte
	var artifactsBytes []byte
	err := scanner.Scan(
		&snapshot.SnapshotID,
		&snapshot.UserID,
		&snapshot.ConversationID,
		&snapshot.SnapshotVersion,
		&snapshot.SourceHash,
		&snapshot.RequestFormat,
		&provider,
		&mode,
		&finalizeStatus,
		&metadataBytes,
		&usageBytes,
		&snapshot.MessageCount,
		&partsBytes,
		&artifactsBytes,
		&snapshot.CreatedAt,
		&snapshot.UpdatedAt,
	)
	if err != nil {
		return domain.MemorySourceSnapshot{}, err
	}
	snapshot.Provider = domain.Provider(provider)
	snapshot.Mode = domain.Mode(mode)
	snapshot.FinalizeStatus = domain.FinalizeStatus(finalizeStatus)
	_ = json.Unmarshal(metadataBytes, &snapshot.Metadata)
	_ = json.Unmarshal(usageBytes, &snapshot.Usage)
	if err = json.Unmarshal(partsBytes, &snapshot.Parts); err != nil {
		return domain.MemorySourceSnapshot{}, err
	}
	if err = json.Unmarshal(artifactsBytes, &snapshot.Artifacts); err != nil {
		return domain.MemorySourceSnapshot{}, err
	}
	snapshot.WorkspaceID = security.WorkspaceID(ctx, snapshot.UserID)
	snapshot.Parts, err = domain.NormalizeMessageParts(snapshot.UserID, snapshot.WorkspaceID, snapshot.ConversationID, snapshot.Parts)
	if err != nil {
		return domain.MemorySourceSnapshot{}, err
	}
	snapshot.Artifacts, err = domain.NormalizePartArtifacts(snapshot.UserID, snapshot.WorkspaceID, snapshot.ConversationID, snapshot.Artifacts)
	if err != nil {
		return domain.MemorySourceSnapshot{}, err
	}
	return snapshot, nil
}

func scanGenerationSource(ctx context.Context, scanner interface{ Scan(dest ...any) error }) (domain.MemoryGenerationSource, error) {
	var item domain.MemoryGenerationSource
	var lastError sqlpkg.NullString
	var processedAt sqlpkg.NullTime
	err := scanner.Scan(
		&item.ID,
		&item.GenerationID,
		&item.UserID,
		&item.SnapshotID,
		&item.ConversationID,
		&item.SourceHash,
		&item.Status,
		&lastError,
		&item.CreatedAt,
		&item.UpdatedAt,
		&processedAt,
	)
	if err != nil {
		return domain.MemoryGenerationSource{}, err
	}
	if lastError.Valid {
		item.LastError = lastError.String
	}
	if processedAt.Valid {
		item.ProcessedAt = &processedAt.Time
	}
	item.WorkspaceID = security.WorkspaceID(ctx, item.UserID)
	return item, nil
}

func sourceConversationKey(ctx context.Context, userID, conversationID string) string {
	return string(marshalJSON([]string{userID, security.WorkspaceID(ctx, userID), conversationID}))
}

func marshalJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func rollback(tx *sqlpkg.Tx) {
	_ = tx.Rollback()
}

func cloneMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func canActivateGeneration(generation domain.MemoryGeneration) bool {
	if generation.Status == domain.MemoryGenerationStatusDiscarded {
		return false
	}
	if generation.IsActive || generation.Status == domain.MemoryGenerationStatusActivated {
		return true
	}
	if generation.FailureCount > 0 {
		return false
	}
	return generation.SourceCount > 0 && generation.ProcessedCount >= generation.SourceCount
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func sameScope(ctx context.Context, user, owner, workspace string) bool {
	return owner == user && workspace == security.WorkspaceID(ctx, user)
}

func ensureWorkspaceTx(ctx context.Context, tx *sqlpkg.Tx, user string) error {
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil {
		return err
	}
	if workspace != domain.PersonalWorkspaceID(user) {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE user_id=$1 AND id=$2 AND status='active' FOR SHARE`, user, workspace).Scan(&id)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ErrAccessDenied
		}
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,capabilities) VALUES($1,$2,'Personal','["gateway.invoke","memory.read","memory.edit","files.read","files.write"]') ON CONFLICT DO NOTHING`, security.WorkspaceID(ctx, user), user)
	return err
}

func lockWorkspaceTx(ctx context.Context, tx *sqlpkg.Tx, user string) error {
	if err := ensureWorkspaceTx(ctx, tx, user); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "generation:"+security.WorkspaceID(ctx, user))
	return err
}

const generationColumns = `generation_id,user_id,status,scope,conversation_id,is_active,structure_version,embedding_version,embedding_model,activate_on_success,source_count,processed_count,embedding_count,failure_count,last_error,metadata,created_at,updated_at,activated_at`

func lockGenerationTx(ctx context.Context, tx *sqlpkg.Tx, user, id string) (domain.MemoryGeneration, error) {
	return scanGeneration(ctx, tx.QueryRowContext(ctx, `SELECT `+generationColumns+` FROM memory_generations WHERE user_id=$1 AND workspace_id=$2 AND generation_id=$3 FOR UPDATE`, user, security.WorkspaceID(ctx, user), id))
}
