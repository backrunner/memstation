package aicall

import "context"

type (
	metadataKey struct{}
	Metadata    struct{ Stage, Strategy string }
)

func WithMetadata(ctx context.Context, stage, strategy string) context.Context {
	return context.WithValue(ctx, metadataKey{}, Metadata{Stage: stage, Strategy: strategy})
}

func MetadataFromContext(ctx context.Context) Metadata {
	m, _ := ctx.Value(metadataKey{}).(Metadata)
	return m
}

// WithDefaults preserves the caller's phase while supplying deployment metadata.
func WithDefaults(ctx context.Context, stage, strategy string) context.Context {
	meta := MetadataFromContext(ctx)
	if meta.Stage == "" {
		meta.Stage = stage
	}
	if meta.Strategy == "" {
		meta.Strategy = strategy
	}
	return WithMetadata(ctx, meta.Stage, meta.Strategy)
}
