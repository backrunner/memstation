package memorygen

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

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	conversationservice "memstation/apps/server/internal/service/conversation"
)

const (
	defaultStructureVersion = 1
	defaultEmbeddingVersion = 1
)

type CreateRebuildInput struct {
	UserID                 string
	Scope                  string
	ConversationID         string
	ActivateOnSuccess      bool
	EmbeddingModelOverride string
}

type UserSourceStats struct {
	SnapshotCount        int        `json:"snapshot_count"`
	LatestSnapshotAt     *time.Time `json:"latest_snapshot_at,omitempty"`
	LatestSnapshotUserID string     `json:"latest_snapshot_user_id,omitempty"`
}

type Service struct {
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

func NewService(db runtimeconfig.DBHandle, defaultEmbeddingModel string) *Service {
	return &Service{
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

func (s *Service) currentDB() *sqlpkg.DB {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Current()
}

func CurrentGenerationID(userID string) string {
	return "gen:current:" + strings.TrimSpace(userID)
}

func (s *Service) EnsureActiveGeneration(ctx context.Context, userID string) (domain.MemoryGeneration, error) {
	if strings.TrimSpace(userID) == "" {
		return domain.MemoryGeneration{}, errors.New("user id is required")
	}
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if generationID := s.activeGenerations[userID]; generationID != "" {
			return s.generations[generationID], nil
		}
		now := time.Now().UTC()
		generation := domain.MemoryGeneration{
			GenerationID:      CurrentGenerationID(userID),
			UserID:            userID,
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
		s.activeGenerations[userID] = generation.GenerationID
		return generation, nil
	}

	if generation, ok, err := s.activeGenerationFromDB(ctx, userID); err != nil {
		return domain.MemoryGeneration{}, err
	} else if ok {
		return generation, nil
	}

	now := time.Now().UTC()
	_, err := db.ExecContext(ctx, `
        INSERT INTO memory_generations (
            generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
            embedding_version, embedding_model, activate_on_success, source_count, processed_count,
            embedding_count, failure_count, metadata, created_at, updated_at, activated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,0,0,0,$11,$12,$12,$12)
        ON CONFLICT (generation_id) DO NOTHING
    `,
		CurrentGenerationID(userID),
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
	return s.activeGenerationFromDBRequired(ctx, userID)
}

func (s *Service) ResolveGeneration(ctx context.Context, userID, generationID string) (domain.MemoryGeneration, error) {
	if strings.TrimSpace(generationID) != "" {
		return s.GetGeneration(ctx, userID, generationID)
	}
	return s.EnsureActiveGeneration(ctx, userID)
}

func (s *Service) GetActiveGeneration(ctx context.Context, userID string) (domain.MemoryGeneration, error) {
	return s.EnsureActiveGeneration(ctx, userID)
}

func (s *Service) GetInProgressGeneration(ctx context.Context, userID string) (domain.MemoryGeneration, bool, error) {
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		var items []domain.MemoryGeneration
		for _, generation := range s.generations {
			if generation.UserID != userID || generation.IsActive {
				continue
			}
			if generation.Status != domain.MemoryGenerationStatusQueued &&
				generation.Status != domain.MemoryGenerationStatusRunning &&
				generation.Status != domain.MemoryGenerationStatusCompleted {
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
        WHERE user_id = $1 AND is_active = FALSE AND status IN ('queued', 'running', 'completed')
        ORDER BY created_at DESC
        LIMIT 1
    `, userID)
	generation, err := scanGeneration(row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemoryGeneration{}, false, nil
		}
		return domain.MemoryGeneration{}, false, err
	}
	return generation, true, nil
}

func (s *Service) GetGeneration(ctx context.Context, userID, generationID string) (domain.MemoryGeneration, error) {
	if strings.TrimSpace(generationID) == "" {
		return s.EnsureActiveGeneration(ctx, userID)
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		generation, ok := s.generations[generationID]
		if !ok || generation.UserID != userID {
			return domain.MemoryGeneration{}, errors.New("memory generation not found")
		}
		return generation, nil
	}
	row := db.QueryRowContext(ctx, `
        SELECT generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
               embedding_version, embedding_model, activate_on_success, source_count, processed_count,
               embedding_count, failure_count, last_error, metadata, created_at, updated_at, activated_at
        FROM memory_generations
        WHERE generation_id = $1 AND user_id = $2
    `, generationID, userID)
	generation, err := scanGeneration(row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemoryGeneration{}, errors.New("memory generation not found")
		}
		return domain.MemoryGeneration{}, err
	}
	return generation, nil
}

func (s *Service) ListGenerations(ctx context.Context, userID string, limit int) ([]domain.MemoryGeneration, error) {
	if limit <= 0 {
		limit = 20
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		items := make([]domain.MemoryGeneration, 0)
		for _, generation := range s.generations {
			if generation.UserID == userID {
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
        WHERE user_id = $1
        ORDER BY created_at DESC
        LIMIT $2
    `, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.MemoryGeneration, 0)
	for rows.Next() {
		generation, err := scanGeneration(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, generation)
	}
	return items, rows.Err()
}

func (s *Service) ListGenerationSources(ctx context.Context, userID, generationID string) ([]domain.MemoryGenerationSource, error) {
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
			if item.UserID == userID {
				items = append(items, item)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
		return items, nil
	}
	rows, err := db.QueryContext(ctx, `
        SELECT id, generation_id, user_id, snapshot_id, conversation_id, source_hash, status, last_error, created_at, updated_at, processed_at
        FROM memory_generation_sources
        WHERE generation_id = $1 AND user_id = $2
        ORDER BY created_at ASC
    `, generationID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.MemoryGenerationSource, 0)
	for rows.Next() {
		item, err := scanGenerationSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) IsCurrentGenerationSource(ctx context.Context, userID, generationID, snapshotID string) (bool, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(generationID) == "" || strings.TrimSpace(snapshotID) == "" {
		return false, nil
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		var target domain.MemoryGenerationSource
		found := false
		for _, id := range s.sourcesByGeneration[generationID] {
			item := s.generationSources[id]
			if item.UserID == userID && item.SnapshotID == snapshotID {
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
			if item.UserID != userID || item.ConversationID != target.ConversationID {
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
            WHERE gs.generation_id = $1 AND gs.user_id = $2 AND gs.snapshot_id = $3
            LIMIT 1
        )
        SELECT gs.snapshot_id
        FROM memory_generation_sources gs
        JOIN memory_source_snapshots ss
          ON ss.snapshot_id = gs.snapshot_id
         AND ss.user_id = gs.user_id
        JOIN target t
          ON t.conversation_id = gs.conversation_id
        WHERE gs.generation_id = $1 AND gs.user_id = $2
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

func (s *Service) CaptureSourceSnapshot(ctx context.Context, conversation domain.ConversationRecord) (domain.MemorySourceSnapshot, bool, error) {
	if strings.TrimSpace(conversation.UserID) == "" || strings.TrimSpace(conversation.ConversationID) == "" {
		return domain.MemorySourceSnapshot{}, false, errors.New("conversation user id and conversation id are required")
	}
	snapshot := snapshotFromConversation(conversation)
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		key := sourceConversationKey(conversation.UserID, conversation.ConversationID)
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

	latest, latestFound, err := s.latestSnapshotHeaderFromDB(ctx, conversation.UserID, conversation.ConversationID)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	if latestFound && latest.SourceHash == snapshot.SourceHash {
		loaded, ok, err := s.GetSourceSnapshot(ctx, conversation.UserID, latest.SnapshotID)
		return loaded, ok, err
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

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	defer rollback(tx)

	_, err = tx.ExecContext(ctx, `
        INSERT INTO memory_source_snapshots (
            snapshot_id, user_id, conversation_id, snapshot_version, source_hash, request_format,
            upstream_provider, mode, finalize_status, metadata, usage, message_count, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
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
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
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
			marshalJSON(message.Metadata),
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

func (s *Service) GetSourceSnapshot(ctx context.Context, userID, snapshotID string) (domain.MemorySourceSnapshot, bool, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return domain.MemorySourceSnapshot{}, false, nil
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		snapshot, ok := s.sourceSnapshots[snapshotID]
		if !ok || snapshot.UserID != userID {
			return domain.MemorySourceSnapshot{}, false, nil
		}
		return snapshot, true, nil
	}
	row := db.QueryRowContext(ctx, `
        SELECT snapshot_id, user_id, conversation_id, snapshot_version, source_hash, request_format,
               upstream_provider, mode, finalize_status, metadata, usage, message_count, created_at, updated_at
        FROM memory_source_snapshots
        WHERE snapshot_id = $1 AND user_id = $2
    `, snapshotID, userID)
	snapshot, err := scanSnapshot(row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemorySourceSnapshot{}, false, nil
		}
		return domain.MemorySourceSnapshot{}, false, err
	}
	messages, err := s.loadSnapshotMessages(ctx, snapshot.SnapshotID)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	snapshot.Messages = messages
	return snapshot, true, nil
}

func (s *Service) GetLatestSourceSnapshot(ctx context.Context, userID, conversationID string) (domain.MemorySourceSnapshot, bool, error) {
	if s.currentDB() == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		ids := s.sourceByConversation[sourceConversationKey(userID, conversationID)]
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

func (s *Service) ListSourceSnapshots(ctx context.Context, userID, conversationID string, limit int) ([]domain.MemorySourceSnapshot, error) {
	if limit <= 0 {
		limit = 50
	}
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		ids := append([]string(nil), s.sourceByConversation[sourceConversationKey(userID, conversationID)]...)
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
               upstream_provider, mode, finalize_status, metadata, usage, message_count, created_at, updated_at
        FROM memory_source_snapshots
        WHERE user_id = $1 AND conversation_id = $2
        ORDER BY snapshot_version DESC
        LIMIT $3
    `, userID, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.MemorySourceSnapshot, 0)
	for rows.Next() {
		item, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) SourceStatsForUser(ctx context.Context, userID string) (UserSourceStats, error) {
	db := s.currentDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		stats := UserSourceStats{}
		for _, snapshot := range s.sourceSnapshots {
			if snapshot.UserID != userID {
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
        WHERE user_id = $1
    `, userID)
	var count int
	var latest sqlpkg.NullTime
	if err := row.Scan(&count, &latest); err != nil {
		return UserSourceStats{}, err
	}
	stats := UserSourceStats{SnapshotCount: count}
	if latest.Valid {
		stats.LatestSnapshotAt = &latest.Time
	}
	return stats, nil
}

func (s *Service) CreateRebuildGeneration(ctx context.Context, input CreateRebuildInput) (domain.MemoryGeneration, []domain.MemoryGenerationSource, error) {
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
	if input.ActivateOnSuccess == false {
		// keep explicit false
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
	if len(snapshots) == 0 {
		return domain.MemoryGeneration{}, nil, errors.New("no retained source snapshots are available for rebuild")
	}

	now := time.Now().UTC()
	generation := domain.MemoryGeneration{
		GenerationID:      uuid.NewString(),
		UserID:            input.UserID,
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
	_, err = tx.ExecContext(ctx, `
        INSERT INTO memory_generations (
            generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
            embedding_version, embedding_model, activate_on_success, source_count, processed_count,
            embedding_count, failure_count, last_error, metadata, created_at, updated_at, activated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,0,0,0,'',$12,$13,$13,NULL)
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
	for _, item := range sourceItems {
		_, err = tx.ExecContext(ctx, `
            INSERT INTO memory_generation_sources (
                id, generation_id, user_id, snapshot_id, conversation_id, source_hash, status,
                last_error, created_at, updated_at, processed_at
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,NULL)
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
	}
	if err := tx.Commit(); err != nil {
		return domain.MemoryGeneration{}, nil, err
	}
	return generation, sourceItems, nil
}

func (s *Service) AddSnapshotToGeneration(ctx context.Context, generationID string, snapshot domain.MemorySourceSnapshot) (domain.MemoryGenerationSource, bool, error) {
	if strings.TrimSpace(generationID) == "" || strings.TrimSpace(snapshot.SnapshotID) == "" {
		return domain.MemoryGenerationSource{}, false, nil
	}
	generation, err := s.GetGeneration(ctx, snapshot.UserID, generationID)
	if err != nil {
		return domain.MemoryGenerationSource{}, false, err
	}
	if generation.IsActive || (generation.Status != domain.MemoryGenerationStatusQueued &&
		generation.Status != domain.MemoryGenerationStatusRunning &&
		generation.Status != domain.MemoryGenerationStatusCompleted) {
		return domain.MemoryGenerationSource{}, false, nil
	}

	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, id := range s.sourcesByGeneration[generationID] {
			item := s.generationSources[id]
			if item.SnapshotID == snapshot.SnapshotID {
				return item, false, nil
			}
		}
		now := time.Now().UTC()
		item := domain.MemoryGenerationSource{
			ID:             uuid.NewString(),
			GenerationID:   generationID,
			UserID:         snapshot.UserID,
			SnapshotID:     snapshot.SnapshotID,
			ConversationID: snapshot.ConversationID,
			SourceHash:     snapshot.SourceHash,
			Status:         domain.MemoryGenerationSourceStatusQueued,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		s.generationSources[item.ID] = item
		s.sourcesByGeneration[generationID] = append(s.sourcesByGeneration[generationID], item.ID)
		generation.SourceCount++
		if generation.Status == domain.MemoryGenerationStatusCompleted {
			generation.Status = domain.MemoryGenerationStatusRunning
		}
		generation.UpdatedAt = now
		s.generations[generationID] = generation
		return item, true, nil
	}

	existing := db.QueryRowContext(ctx, `
        SELECT id, generation_id, user_id, snapshot_id, conversation_id, source_hash, status, last_error, created_at, updated_at, processed_at
        FROM memory_generation_sources
        WHERE generation_id = $1 AND snapshot_id = $2
    `, generationID, snapshot.SnapshotID)
	item, err := scanGenerationSource(existing)
	if err == nil {
		return item, false, nil
	}
	if !errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.MemoryGenerationSource{}, false, err
	}

	now := time.Now().UTC()
	item = domain.MemoryGenerationSource{
		ID:             uuid.NewString(),
		GenerationID:   generationID,
		UserID:         snapshot.UserID,
		SnapshotID:     snapshot.SnapshotID,
		ConversationID: snapshot.ConversationID,
		SourceHash:     snapshot.SourceHash,
		Status:         domain.MemoryGenerationSourceStatusQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryGenerationSource{}, false, err
	}
	defer rollback(tx)
	_, err = tx.ExecContext(ctx, `
        INSERT INTO memory_generation_sources (
            id, generation_id, user_id, snapshot_id, conversation_id, source_hash, status,
            last_error, created_at, updated_at, processed_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,'',$8,$8,NULL)
    `,
		item.ID,
		item.GenerationID,
		item.UserID,
		item.SnapshotID,
		item.ConversationID,
		item.SourceHash,
		string(item.Status),
		now,
	)
	if err != nil {
		return domain.MemoryGenerationSource{}, false, err
	}
	_, err = tx.ExecContext(ctx, `
        UPDATE memory_generations
        SET source_count = source_count + 1,
            status = CASE WHEN status = 'completed' THEN 'running' ELSE status END,
            updated_at = $1
        WHERE generation_id = $2
    `, now, generationID)
	if err != nil {
		return domain.MemoryGenerationSource{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MemoryGenerationSource{}, false, err
	}
	return item, true, nil
}

func (s *Service) MarkGenerationSourceRunning(ctx context.Context, userID, generationID, snapshotID string) error {
	if strings.TrimSpace(generationID) == "" || strings.TrimSpace(snapshotID) == "" {
		return nil
	}
	now := time.Now().UTC()
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for id, item := range s.generationSources {
			if item.GenerationID == generationID && item.SnapshotID == snapshotID && item.UserID == userID {
				item.Status = domain.MemoryGenerationSourceStatusRunning
				item.UpdatedAt = now
				s.generationSources[id] = item
			}
		}
		generation := s.generations[generationID]
		if generation.Status == domain.MemoryGenerationStatusQueued || generation.Status == domain.MemoryGenerationStatusCompleted {
			generation.Status = domain.MemoryGenerationStatusRunning
			generation.UpdatedAt = now
			s.generations[generationID] = generation
		}
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.ExecContext(ctx, `
        UPDATE memory_generation_sources
        SET status = $1, updated_at = $2
        WHERE generation_id = $3 AND snapshot_id = $4 AND user_id = $5
    `, string(domain.MemoryGenerationSourceStatusRunning), now, generationID, snapshotID, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
        UPDATE memory_generations
        SET status = CASE WHEN status IN ('queued', 'completed') THEN 'running' ELSE status END, updated_at = $1
        WHERE generation_id = $2 AND user_id = $3
    `, now, generationID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) MarkGenerationSourceCompleted(ctx context.Context, userID, generationID, snapshotID string, embeddingCount int) (domain.MemoryGeneration, bool, error) {
	now := time.Now().UTC()
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for id, item := range s.generationSources {
			if item.GenerationID == generationID && item.SnapshotID == snapshotID && item.UserID == userID {
				if item.Status != domain.MemoryGenerationSourceStatusCompleted {
					item.Status = domain.MemoryGenerationSourceStatusCompleted
					item.ProcessedAt = timePtr(now)
					item.UpdatedAt = now
					s.generationSources[id] = item
					generation := s.generations[generationID]
					generation.ProcessedCount++
					generation.EmbeddingCount += max(embeddingCount, 0)
					generation.UpdatedAt = now
					if generation.ProcessedCount >= generation.SourceCount && generation.FailureCount == 0 {
						generation.Status = domain.MemoryGenerationStatusCompleted
					}
					s.generations[generationID] = generation
					return generation, generation.ProcessedCount >= generation.SourceCount && generation.FailureCount == 0 && generation.ActivateOnSuccess, nil
				}
				return s.generations[generationID], false, nil
			}
		}
		return domain.MemoryGeneration{}, false, errors.New("generation source not found")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryGeneration{}, false, err
	}
	defer rollback(tx)
	result, err := tx.ExecContext(ctx, `
        UPDATE memory_generation_sources
        SET status = $1, last_error = '', processed_at = $2, updated_at = $2
        WHERE generation_id = $3 AND snapshot_id = $4 AND user_id = $5 AND status <> $1
    `, string(domain.MemoryGenerationSourceStatusCompleted), now, generationID, snapshotID, userID)
	if err != nil {
		return domain.MemoryGeneration{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return domain.MemoryGeneration{}, false, err
	}
	if rows > 0 {
		if _, err = tx.ExecContext(ctx, `
            UPDATE memory_generations
            SET processed_count = processed_count + 1,
                embedding_count = embedding_count + $1,
                status = CASE
                    WHEN failure_count = 0 AND processed_count + 1 >= source_count THEN $2
                    WHEN status = 'queued' THEN 'running'
                    ELSE status
                END,
                updated_at = $3
            WHERE generation_id = $4 AND user_id = $5
        `, max(embeddingCount, 0), string(domain.MemoryGenerationStatusCompleted), now, generationID, userID); err != nil {
			return domain.MemoryGeneration{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.MemoryGeneration{}, false, err
	}
	generation, err := s.GetGeneration(ctx, userID, generationID)
	if err != nil {
		return domain.MemoryGeneration{}, false, err
	}
	ready := generation.ProcessedCount >= generation.SourceCount && generation.FailureCount == 0 && generation.ActivateOnSuccess
	return generation, ready, nil
}

func (s *Service) MarkGenerationSourceFailed(ctx context.Context, userID, generationID, snapshotID, lastError string) (domain.MemoryGeneration, error) {
	now := time.Now().UTC()
	lastError = strings.TrimSpace(lastError)
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for id, item := range s.generationSources {
			if item.GenerationID == generationID && item.SnapshotID == snapshotID && item.UserID == userID {
				item.Status = domain.MemoryGenerationSourceStatusFailed
				item.LastError = lastError
				item.UpdatedAt = now
				s.generationSources[id] = item
			}
		}
		generation := s.generations[generationID]
		generation.Status = domain.MemoryGenerationStatusFailed
		generation.FailureCount++
		generation.LastError = lastError
		generation.UpdatedAt = now
		s.generations[generationID] = generation
		return generation, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	defer rollback(tx)
	if _, err = tx.ExecContext(ctx, `
        UPDATE memory_generation_sources
        SET status = $1, last_error = $2, updated_at = $3
        WHERE generation_id = $4 AND snapshot_id = $5 AND user_id = $6
    `, string(domain.MemoryGenerationSourceStatusFailed), lastError, now, generationID, snapshotID, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if _, err = tx.ExecContext(ctx, `
        UPDATE memory_generations
        SET status = $1, failure_count = failure_count + 1, last_error = $2, updated_at = $3
        WHERE generation_id = $4 AND user_id = $5
    `, string(domain.MemoryGenerationStatusFailed), lastError, now, generationID, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MemoryGeneration{}, err
	}
	return s.GetGeneration(ctx, userID, generationID)
}

func (s *Service) ActivateGeneration(ctx context.Context, userID, generationID string) (domain.MemoryGeneration, error) {
	if strings.TrimSpace(generationID) == "" {
		return domain.MemoryGeneration{}, errors.New("generation id is required")
	}
	generation, err := s.GetGeneration(ctx, userID, generationID)
	if err != nil {
		return domain.MemoryGeneration{}, err
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
		if !ok || generation.UserID != userID {
			return domain.MemoryGeneration{}, errors.New("memory generation not found")
		}
		for id, item := range s.generations {
			if item.UserID != userID {
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
		s.activeGenerations[userID] = generationID
		return s.generations[generationID], nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	defer rollback(tx)
	if _, err = tx.ExecContext(ctx, `
        UPDATE memory_generations
        SET is_active = FALSE, updated_at = $1
        WHERE user_id = $2 AND is_active = TRUE
    `, now, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if _, err = tx.ExecContext(ctx, `
        UPDATE memory_generations
        SET is_active = TRUE, status = $1, activated_at = $2, updated_at = $2
        WHERE generation_id = $3 AND user_id = $4
    `, string(domain.MemoryGenerationStatusActivated), now, generationID, userID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	conversationMetadata := string(marshalJSON(map[string]any{"memory_generation_id": generationID}))
	if generation.GenerationID == CurrentGenerationID(userID) || generation.SourceCount == 0 {
		if _, err = tx.ExecContext(ctx, `
        UPDATE conversations
        SET metadata = COALESCE(metadata, '{}'::jsonb) || $1::jsonb,
            updated_at = $2
        WHERE user_id = $3
    `, conversationMetadata, now, userID); err != nil {
			return domain.MemoryGeneration{}, err
		}
	} else if _, err = tx.ExecContext(ctx, `
        UPDATE conversations
        SET metadata = COALESCE(metadata, '{}'::jsonb) || $1::jsonb,
            updated_at = $2
        WHERE user_id = $3
          AND id IN (
              SELECT DISTINCT conversation_id
              FROM memory_generation_sources
              WHERE generation_id = $4 AND user_id = $3
          )
    `, conversationMetadata, now, userID, generationID); err != nil {
		return domain.MemoryGeneration{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MemoryGeneration{}, err
	}
	return s.GetGeneration(ctx, userID, generationID)
}

func (s *Service) latestSnapshotsForScope(ctx context.Context, userID, scope, conversationID string) ([]domain.MemorySourceSnapshot, error) {
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
			for key, ids := range s.sourceByConversation {
				if !strings.HasPrefix(key, userID+"::") || len(ids) == 0 {
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
                upstream_provider, mode, finalize_status, metadata, usage, message_count, created_at, updated_at
            FROM memory_source_snapshots
            WHERE user_id = $1
            ORDER BY conversation_id, snapshot_version DESC
        `, userID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := make([]domain.MemorySourceSnapshot, 0)
		for rows.Next() {
			item, err := scanSnapshot(rows)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
		return items, rows.Err()
	}
}

func (s *Service) activeGenerationFromDB(ctx context.Context, userID string) (domain.MemoryGeneration, bool, error) {
	db := s.currentDB()
	if db == nil {
		return domain.MemoryGeneration{}, false, nil
	}
	row := db.QueryRowContext(ctx, `
        SELECT generation_id, user_id, status, scope, conversation_id, is_active, structure_version,
               embedding_version, embedding_model, activate_on_success, source_count, processed_count,
               embedding_count, failure_count, last_error, metadata, created_at, updated_at, activated_at
        FROM memory_generations
        WHERE user_id = $1 AND is_active = TRUE
        LIMIT 1
    `, userID)
	generation, err := scanGeneration(row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemoryGeneration{}, false, nil
		}
		return domain.MemoryGeneration{}, false, err
	}
	return generation, true, nil
}

func (s *Service) activeGenerationFromDBRequired(ctx context.Context, userID string) (domain.MemoryGeneration, error) {
	generation, ok, err := s.activeGenerationFromDB(ctx, userID)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	if !ok {
		return domain.MemoryGeneration{}, errors.New("active memory generation not found")
	}
	return generation, nil
}

func (s *Service) latestSnapshotHeaderFromDB(ctx context.Context, userID, conversationID string) (domain.MemorySourceSnapshot, bool, error) {
	db := s.currentDB()
	if db == nil {
		return domain.MemorySourceSnapshot{}, false, nil
	}
	row := db.QueryRowContext(ctx, `
        SELECT snapshot_id, user_id, conversation_id, snapshot_version, source_hash, request_format,
               upstream_provider, mode, finalize_status, metadata, usage, message_count, created_at, updated_at
        FROM memory_source_snapshots
        WHERE user_id = $1 AND conversation_id = $2
        ORDER BY snapshot_version DESC
        LIMIT 1
    `, userID, conversationID)
	snapshot, err := scanSnapshot(row)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.MemorySourceSnapshot{}, false, nil
		}
		return domain.MemorySourceSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *Service) loadSnapshotMessages(ctx context.Context, snapshotID string) ([]domain.UnifiedMessage, error) {
	db := s.currentDB()
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
        SELECT role, text_content, content_blocks, tool_calls, tool_result, provider_payload
        FROM memory_source_messages
        WHERE snapshot_id = $1
        ORDER BY sequence_num ASC
    `, snapshotID)
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
		items = append(items, message)
	}
	return items, rows.Err()
}

func snapshotFromConversation(conversation domain.ConversationRecord) domain.MemorySourceSnapshot {
	now := time.Now().UTC()
	metadata := cloneMap(conversation.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["captured_from_conversation"] = conversation.ConversationID
	metadata["captured_at"] = now.Format(time.RFC3339)
	messages := conversationservice.PrepareMessagesForStorage(conversation.Messages)
	return domain.MemorySourceSnapshot{
		UserID:         conversation.UserID,
		ConversationID: conversation.ConversationID,
		SourceHash: sourceHash(map[string]any{
			"request_format": conversation.RequestFormat,
			"provider":       conversation.Provider,
			"mode":           conversation.Mode,
			"messages":       messagesWithoutDerivedPromptText(messages),
		}),
		RequestFormat:  conversation.RequestFormat,
		Provider:       conversation.Provider,
		Mode:           conversation.Mode,
		FinalizeStatus: conversation.FinalizeStatus,
		Metadata:       metadata,
		Usage:          conversation.Usage,
		MessageCount:   len(messages),
		Messages:       messages,
	}
}

func messagesWithoutDerivedPromptText(messages []domain.UnifiedMessage) []domain.UnifiedMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]domain.UnifiedMessage, 0, len(messages))
	for _, message := range messages {
		cloned := message
		if len(message.Metadata) > 0 {
			cloned.Metadata = cloneMap(message.Metadata)
			delete(cloned.Metadata, conversationservice.MemoryPromptTextMetadataKey)
			if len(cloned.Metadata) == 0 {
				cloned.Metadata = nil
			}
		}
		out = append(out, cloned)
	}
	return out
}

func scanGeneration(scanner interface{ Scan(dest ...any) error }) (domain.MemoryGeneration, error) {
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
	return generation, nil
}

func scanSnapshot(scanner interface{ Scan(dest ...any) error }) (domain.MemorySourceSnapshot, error) {
	var snapshot domain.MemorySourceSnapshot
	var provider string
	var mode string
	var finalizeStatus string
	var metadataBytes []byte
	var usageBytes []byte
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
	return snapshot, nil
}

func scanGenerationSource(scanner interface{ Scan(dest ...any) error }) (domain.MemoryGenerationSource, error) {
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
	return item, nil
}

func sourceConversationKey(userID, conversationID string) string {
	return strings.TrimSpace(userID) + "::" + strings.TrimSpace(conversationID)
}

func sourceHash(value any) string {
	raw, _ := json.Marshal(value)
	return uuid.NewSHA1(uuid.NameSpaceOID, raw).String()
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
	if len(value) == 0 {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func canActivateGeneration(generation domain.MemoryGeneration) bool {
	if generation.IsActive || generation.Status == domain.MemoryGenerationStatusActivated {
		return true
	}
	if generation.FailureCount > 0 {
		return false
	}
	return generation.SourceCount > 0 && generation.ProcessedCount >= generation.SourceCount
}

func stringValue(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
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
