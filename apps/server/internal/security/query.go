package security

import (
	"context"
	"slices"
	"time"
)

// MemoryFilter narrows an authorized scope. It never grants access.
type MemoryFilter struct {
	SubjectActorIDs []string
	From, To        *time.Time
}
type memoryFilterKey struct{}

func WithMemoryFilter(ctx context.Context, f MemoryFilter) context.Context {
	f.SubjectActorIDs = slices.Clone(f.SubjectActorIDs)
	if f.From != nil {
		v := *f.From
		f.From = &v
	}
	if f.To != nil {
		v := *f.To
		f.To = &v
	}
	return context.WithValue(ctx, memoryFilterKey{}, f)
}

func MemoryFilterFromContext(ctx context.Context) MemoryFilter {
	f, _ := ctx.Value(memoryFilterKey{}).(MemoryFilter)
	f.SubjectActorIDs = slices.Clone(f.SubjectActorIDs)
	return f
}

func MatchesMemoryFilter(ctx context.Context, actors []string, at time.Time) bool {
	f := MemoryFilterFromContext(ctx)
	if len(f.SubjectActorIDs) > 0 {
		match := false
		for _, actor := range actors {
			if slices.Contains(f.SubjectActorIDs, actor) {
				match = true
			}
		}
		if !match {
			return false
		}
	}
	return (f.From == nil || !at.Before(*f.From)) && (f.To == nil || !at.After(*f.To))
}
