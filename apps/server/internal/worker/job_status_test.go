package worker

import (
	"context"
	"errors"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"

	"github.com/hibiken/asynq"
)

type jobFixture struct {
	jobs         []domain.MemoryJob
	authorityErr error
}

func (f *jobFixture) ValidateMemoryTask(context.Context, domain.MemoryTaskScope) error {
	return f.authorityErr
}

func (f *jobFixture) RecordJob(ctx context.Context, j domain.MemoryJob) error {
	if _, err := security.DataScope(ctx, j.UserID, j.WorkspaceID); err != nil {
		return err
	}
	f.jobs = append(f.jobs, j)
	return nil
}

func TestObsoleteJobTerminatesWithoutRetry(t *testing.T) {
	for _, during := range []bool{false, true} {
		f := &jobFixture{}
		if !during {
			f.authorityErr = domain.ErrMemoryTaskObsolete
		}
		called := false
		h := trackMemoryJob(Services{TaskAuthority: f, Jobs: f})(asynq.HandlerFunc(func(context.Context, *asynq.Task) error { called = true; return domain.ErrMemoryTaskObsolete }))
		task := asynq.NewTask("embedding.generate", []byte(`{"schema_version":2,"user_id":"owner","workspace_id":"group","node_id":"node"}`))
		if err := h.ProcessTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		if called != during || len(f.jobs) == 0 || f.jobs[len(f.jobs)-1].Status != "skipped" || f.jobs[len(f.jobs)-1].NextRetryAt != nil {
			t.Fatalf("obsolete ledger: %+v called=%v", f.jobs, called)
		}
		f.jobs = nil
		f.authorityErr = domain.ErrAccessDenied
		if err := h.ProcessTask(context.Background(), task); !errors.Is(err, domain.ErrAccessDenied) || len(f.jobs) != 0 {
			t.Fatal("unverified task wrote ledger", err)
		}
	}
}
