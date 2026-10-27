package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
)

// MemoryGate verifies a restored/switched database before any data request or
// worker can read it. Readiness alone is not an access control boundary.
type MemoryGate struct {
	mu       sync.Mutex
	repo     *MemoryLifecycleRepository
	verified *sql.DB
}

func NewMemoryGate(repo *MemoryLifecycleRepository) *MemoryGate { return &MemoryGate{repo: repo} }

func (g *MemoryGate) Check(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	db, err := g.repo.database()
	if err != nil {
		return err
	}
	if db == g.verified {
		return nil
	}
	if err = g.repo.EnsureDeletionJournal(ctx); err != nil {
		return errors.New("memory recovery barrier has not completed")
	}
	if dbutilstore.CurrentDB(g.repo.db) != db {
		return errors.New("memory database changed during recovery")
	}
	g.verified = db
	return nil
}

func (g *MemoryGate) Verify(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	db, err := g.repo.database()
	if err != nil {
		return err
	}
	if err = g.repo.EnsureDeletionJournal(ctx); err != nil {
		g.verified = nil
		return errors.New("memory deletion history unavailable or incomplete")
	}
	g.verified = db
	return nil
}
