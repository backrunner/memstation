package ports

import (
	"context"

	"memstation/apps/server/internal/domain"
)

type MemoryLifecycleRepository interface {
	Inspect(context.Context, string) (domain.MemoryDocument, error)
	Propose(context.Context, domain.MemoryCandidate) (domain.MemoryCandidate, error)
	Candidates(context.Context, string, ...string) ([]domain.MemoryCandidate, error)
	Decide(context.Context, domain.CandidateDecision) (domain.MemoryCandidate, error)
	Forget(context.Context, domain.ForgetSelector, string) (domain.MemoryOperation, error)
	Operation(context.Context, string) (domain.MemoryOperation, error)
	Export(context.Context, string) (domain.MemoryOperation, error)
	Publish(context.Context, domain.PublicationInput) (domain.MemoryPublication, error)
	Publications(context.Context, ...string) ([]domain.MemoryPublication, error)
	RetractPublication(context.Context, string, int64) (domain.MemoryPublication, error)
}

type DeletionJournal interface {
	Initialize(context.Context, string) (string, error)
	Entries(context.Context) (string, []domain.DeletionEntry, error)
	Append(context.Context, domain.DeletionIntent) (domain.DeletionEntry, error)
}
