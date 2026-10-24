package memorygeneration

import (
	"context"
	"errors"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"

	"github.com/google/uuid"
)

func (s *Repository) AddSnapshotToGeneration(ctx context.Context, generationID string, snapshot domain.MemorySourceSnapshot) (domain.MemoryGenerationSource, bool, error) {
	if _, err := security.PersonalScope(ctx, snapshot.UserID, snapshot.WorkspaceID); err != nil {
		return domain.MemoryGenerationSource{}, false, err
	}
	if generationID == "" || snapshot.SnapshotID == "" {
		return domain.MemoryGenerationSource{}, false, nil
	}
	stored, ok, err := s.GetSourceSnapshot(ctx, snapshot.UserID, snapshot.SnapshotID)
	if err != nil {
		return domain.MemoryGenerationSource{}, false, err
	}
	if !ok {
		return domain.MemoryGenerationSource{}, false, domain.ErrAccessDenied
	}
	snapshot = stored
	now := time.Now().UTC()
	item := domain.MemoryGenerationSource{ID: uuid.NewString(), UserID: snapshot.UserID, WorkspaceID: snapshot.WorkspaceID, GenerationID: generationID, SnapshotID: snapshot.SnapshotID, ConversationID: snapshot.ConversationID, SourceHash: snapshot.SourceHash, Status: domain.MemoryGenerationSourceStatusQueued, CreatedAt: now, UpdatedAt: now}
	if db := s.currentDB(); db != nil {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return item, false, err
		}
		defer rollback(tx)
		generation, err := lockGenerationTx(ctx, tx, snapshot.UserID, generationID)
		if err != nil {
			return item, false, err
		}
		if !acceptsSources(generation) {
			return domain.MemoryGenerationSource{}, false, nil
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO memory_generation_sources(id,generation_id,user_id,workspace_id,snapshot_id,conversation_id,source_hash,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'queued',$8,$8) ON CONFLICT(generation_id,snapshot_id) DO NOTHING`, item.ID, generationID, item.UserID, item.WorkspaceID, item.SnapshotID, item.ConversationID, item.SourceHash, now)
		if err != nil {
			return item, false, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return item, false, err
		}
		if n == 0 {
			old, err := scanGenerationSource(tx.QueryRowContext(ctx, `SELECT id,generation_id,user_id,snapshot_id,conversation_id,source_hash,status,last_error,created_at,updated_at,processed_at FROM memory_generation_sources WHERE generation_id=$1 AND snapshot_id=$2 AND user_id=$3 AND workspace_id=$4`, generationID, item.SnapshotID, item.UserID, item.WorkspaceID))
			return old, false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE memory_generations SET source_count=source_count+1,updated_at=$1 WHERE generation_id=$2 AND user_id=$3 AND workspace_id=$4`, now, generationID, item.UserID, item.WorkspaceID); err != nil {
			return item, false, err
		}
		return item, true, tx.Commit()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	generation, ok := s.generations[generationID]
	if !ok || !sameScope(snapshot.UserID, generation.UserID, generation.WorkspaceID) {
		return item, false, domain.ErrAccessDenied
	}
	if !acceptsSources(generation) {
		return domain.MemoryGenerationSource{}, false, nil
	}
	for _, id := range s.sourcesByGeneration[generationID] {
		old := s.generationSources[id]
		if old.SnapshotID == item.SnapshotID {
			return old, false, nil
		}
	}
	s.generationSources[item.ID] = item
	s.sourcesByGeneration[generationID] = append(s.sourcesByGeneration[generationID], item.ID)
	generation.SourceCount++
	generation.UpdatedAt = now
	s.generations[generationID] = generation
	return item, true, nil
}

func acceptsSources(g domain.MemoryGeneration) bool {
	return !g.IsActive && (g.Status == domain.MemoryGenerationStatusQueued || g.Status == domain.MemoryGenerationStatusRunning)
}

func (s *Repository) MarkGenerationSourceRunning(ctx context.Context, userID, generationID, snapshotID string) error {
	_, _, err := s.transitionSource(ctx, userID, generationID, snapshotID, domain.MemoryGenerationSourceStatusRunning, "", 0)
	return err
}

func (s *Repository) MarkGenerationSourceCompleted(ctx context.Context, userID, generationID, snapshotID string, embeddingCount int) (domain.MemoryGeneration, bool, error) {
	g, _, err := s.transitionSource(ctx, userID, generationID, snapshotID, domain.MemoryGenerationSourceStatusCompleted, "", embeddingCount)
	// Completion can be retried after the activation task failed to publish.
	// Counters stay idempotent, while readiness remains true until activation.
	return g, err == nil && g.Status == domain.MemoryGenerationStatusCompleted && g.ActivateOnSuccess, err
}

func (s *Repository) MarkGenerationSourceFailed(ctx context.Context, userID, generationID, snapshotID, lastError string) (domain.MemoryGeneration, error) {
	g, _, err := s.transitionSource(ctx, userID, generationID, snapshotID, domain.MemoryGenerationSourceStatusFailed, lastError, 0)
	return g, err
}

func transition(g domain.MemoryGeneration, source domain.MemoryGenerationSource, status domain.MemoryGenerationSourceStatus, lastError string, count int) (domain.MemoryGeneration, domain.MemoryGenerationSource, bool) {
	if g.Status == domain.MemoryGenerationStatusDiscarded || g.IsActive || source.Status == domain.MemoryGenerationSourceStatusCompleted || source.Status == status {
		return g, source, false
	}
	now := time.Now().UTC()
	if source.Status == domain.MemoryGenerationSourceStatusFailed && status != domain.MemoryGenerationSourceStatusFailed {
		g.FailureCount = max(g.FailureCount-1, 0)
	}
	source.Status = status
	source.LastError = lastError
	source.UpdatedAt = now
	g.UpdatedAt = now
	switch status {
	case domain.MemoryGenerationSourceStatusCompleted:
		source.ProcessedAt = timePtr(now)
		g.ProcessedCount++
		g.EmbeddingCount += max(count, 0)
	case domain.MemoryGenerationSourceStatusFailed:
		g.FailureCount++
		g.LastError = lastError
	}
	if g.FailureCount > 0 {
		g.Status = domain.MemoryGenerationStatusFailed
	} else if g.ProcessedCount >= g.SourceCount && g.SourceCount > 0 {
		g.Status = domain.MemoryGenerationStatusCompleted
		g.LastError = ""
	} else {
		g.Status = domain.MemoryGenerationStatusRunning
		g.LastError = ""
	}
	return g, source, true
}

func (s *Repository) transitionSource(ctx context.Context, userID, generationID, snapshotID string, status domain.MemoryGenerationSourceStatus, lastError string, count int) (domain.MemoryGeneration, bool, error) {
	if _, err := security.PersonalScope(ctx, userID, ""); err != nil {
		return domain.MemoryGeneration{}, false, err
	}
	if db := s.currentDB(); db != nil {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return domain.MemoryGeneration{}, false, err
		}
		defer rollback(tx)
		g, err := lockGenerationTx(ctx, tx, userID, generationID)
		if err != nil {
			return g, false, err
		}
		if g.Status == domain.MemoryGenerationStatusDiscarded || g.IsActive {
			return g, false, nil
		}
		item, err := scanGenerationSource(tx.QueryRowContext(ctx, `SELECT id,generation_id,user_id,snapshot_id,conversation_id,source_hash,status,last_error,created_at,updated_at,processed_at FROM memory_generation_sources WHERE user_id=$1 AND workspace_id=$2 AND generation_id=$3 AND snapshot_id=$4 FOR UPDATE`, userID, g.WorkspaceID, generationID, snapshotID))
		if err != nil {
			return g, false, err
		}
		g, item, changed := transition(g, item, status, lastError, count)
		if !changed {
			return g, false, nil
		}
		if _, err = tx.ExecContext(ctx, `UPDATE memory_generation_sources SET status=$1,last_error=$2,updated_at=$3,processed_at=$4 WHERE id=$5 AND user_id=$6 AND workspace_id=$7`, item.Status, item.LastError, item.UpdatedAt, item.ProcessedAt, item.ID, userID, g.WorkspaceID); err != nil {
			return g, false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE memory_generations SET status=$1,processed_count=$2,embedding_count=$3,failure_count=$4,last_error=$5,updated_at=$6 WHERE generation_id=$7 AND user_id=$8 AND workspace_id=$9`, g.Status, g.ProcessedCount, g.EmbeddingCount, g.FailureCount, g.LastError, g.UpdatedAt, generationID, userID, g.WorkspaceID); err != nil {
			return g, false, err
		}
		return g, true, tx.Commit()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.generations[generationID]
	if !ok || !sameScope(userID, g.UserID, g.WorkspaceID) {
		return domain.MemoryGeneration{}, false, domain.ErrAccessDenied
	}
	if g.Status == domain.MemoryGenerationStatusDiscarded || g.IsActive {
		return g, false, nil
	}
	for _, id := range s.sourcesByGeneration[generationID] {
		item := s.generationSources[id]
		if item.SnapshotID == snapshotID && sameScope(userID, item.UserID, item.WorkspaceID) {
			g, item, changed := transition(g, item, status, lastError, count)
			if changed {
				s.generations[generationID] = g
				s.generationSources[id] = item
			}
			return g, changed, nil
		}
	}
	return g, false, errors.New("generation source not found")
}
