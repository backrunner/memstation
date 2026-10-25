package sql

import (
	"context"
	"errors"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
)

// Unlike the legacy development stores, durable ingestion never acknowledges a
// write into process memory when PostgreSQL is absent or changes connections.
type DurableIngestionRepository struct {
	db      runtimeconfig.DBHandle
	journal ports.DeletionJournal
}

func NewDurableIngestionRepository(db runtimeconfig.DBHandle) *DurableIngestionRepository {
	return &DurableIngestionRepository{db: db}
}

func (r *DurableIngestionRepository) current() (*PostgresIngestionRepository, error) {
	db := currentDB(r.db)
	if db == nil {
		return nil, errors.New("durable ingestion database unavailable")
	}
	r2 := NewPostgresIngestionRepository(db)
	r2.SetDeletionJournal(r.journal)
	return r2, nil
}

func (r *DurableIngestionRepository) Observe(ctx context.Context, b domain.ObserveBatch, key string, budget int) (domain.IngestionReceipt, error) {
	repo, err := r.current()
	if err != nil {
		return domain.IngestionReceipt{}, err
	}
	return repo.Observe(ctx, b, key, budget)
}

func (r *DurableIngestionRepository) Progress(ctx context.Context, id string) (domain.IngestionReceipt, error) {
	repo, err := r.current()
	if err != nil {
		return domain.IngestionReceipt{}, err
	}
	return repo.Progress(ctx, id)
}

func (r *DurableIngestionRepository) Source(ctx context.Context, id string) (domain.SourceRecord, error) {
	repo, err := r.current()
	if err != nil {
		return domain.SourceRecord{}, err
	}
	return repo.Source(ctx, id)
}

func (r *DurableIngestionRepository) FinalizeWindow(ctx context.Context, id string, revision int64) error {
	repo, err := r.current()
	if err != nil {
		return err
	}
	return repo.FinalizeWindow(ctx, id, revision)
}

func (r *DurableIngestionRepository) FinalizeIdle(ctx context.Context, before time.Time) error {
	repo, err := r.current()
	if err != nil {
		return err
	}
	return repo.FinalizeIdle(ctx, before)
}

func (r *DurableIngestionRepository) RecordJob(ctx context.Context, j domain.MemoryJob) error {
	repo, err := r.current()
	if err != nil {
		return err
	}
	return repo.RecordJob(ctx, j)
}

func (r *DurableIngestionRepository) ValidateMemoryTask(ctx context.Context, s domain.MemoryTaskScope) error {
	repo, err := r.current()
	if err != nil {
		return err
	}
	return repo.ValidateMemoryTask(ctx, s)
}

func (r *DurableIngestionRepository) SetDeletionJournal(j ports.DeletionJournal) { r.journal = j }

func (r *DurableIngestionRepository) AcknowledgeReceipt(ctx context.Context, id string) error {
	repo, err := r.current()
	if err != nil {
		return err
	}
	_, err = repo.db.ExecContext(ctx, `UPDATE ingestion_receipts SET processing_started_at=COALESCE(processing_started_at,now()) WHERE id=$1`, id)
	return err
}

func (r *DurableIngestionRepository) OpenWindow(ctx context.Context, in domain.ConversationWindowInput, key string) (domain.ConversationWindow, error) {
	repo, err := r.current()
	if err != nil {
		return domain.ConversationWindow{}, err
	}
	return repo.OpenWindow(ctx, in, key)
}

func (r *DurableIngestionRepository) Window(ctx context.Context, id string) (domain.ConversationWindow, error) {
	repo, err := r.current()
	if err != nil {
		return domain.ConversationWindow{}, err
	}
	return repo.Window(ctx, id)
}
