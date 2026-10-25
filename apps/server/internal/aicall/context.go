package aicall

import "context"

type Job struct {
	ID      string
	Attempt int
}
type jobKey struct{}

func WithJob(ctx context.Context, id string, attempt int) context.Context {
	return context.WithValue(ctx, jobKey{}, Job{ID: id, Attempt: attempt})
}
func JobFromContext(ctx context.Context) Job { j, _ := ctx.Value(jobKey{}).(Job); return j }
