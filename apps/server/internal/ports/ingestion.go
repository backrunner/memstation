package ports

import (
	"context"
	"time"

	"memstation/apps/server/internal/domain"
)

type IngestionRepository interface {
	OpenWindow(context.Context, domain.ConversationWindowInput, string) (domain.ConversationWindow, error)
	Window(context.Context, string) (domain.ConversationWindow, error)
	Observe(context.Context, domain.ObserveBatch, string, int) (domain.IngestionReceipt, error)
	Progress(context.Context, string) (domain.IngestionReceipt, error)
	Source(context.Context, string) (domain.SourceRecord, error)
	FinalizeWindow(context.Context, string, int64) error
	FinalizeIdle(context.Context, time.Time) error
}

type MemoryJobRecorder interface {
	RecordJob(context.Context, domain.MemoryJob) error
}

type RecallWatermarkReader interface {
	Watermarks(context.Context) ([]domain.StreamWatermark, bool, error)
}

type MemoryTaskAuthority interface {
	ValidateMemoryTask(context.Context, domain.MemoryTaskScope) error
}
