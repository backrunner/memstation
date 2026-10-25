package aicall

import (
	"context"
	"sync"

	"memstation/apps/server/internal/domain"
)

type (
	traceKey struct{}
	Trace    struct {
		mu          sync.Mutex
		calls       []domain.RecallProviderCall
		failureCode string
	}
)

func WithTrace(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{}
	return context.WithValue(ctx, traceKey{}, t), t
}

// RecordFailure distinguishes usable inference from a billable, completed
// upstream call whose response fails local schema or index validation.
func RecordFailure(ctx context.Context, code string) {
	if t, ok := ctx.Value(traceKey{}).(*Trace); ok {
		t.mu.Lock()
		if t.failureCode == "" {
			t.failureCode = code
		}
		t.mu.Unlock()
	}
}

func (t *Trace) FailureCode() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failureCode
}

func Record(ctx context.Context, c domain.AICall) {
	if t, ok := ctx.Value(traceKey{}).(*Trace); ok {
		t.mu.Lock()
		t.calls = append(t.calls, domain.RecallProviderCall{CallID: c.ID, Role: c.Role, ProviderRequestID: c.ProviderRequestID, Status: c.Status})
		t.mu.Unlock()
	}
}

func (t *Trace) Calls() []domain.RecallProviderCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]domain.RecallProviderCall{}, t.calls...)
}
