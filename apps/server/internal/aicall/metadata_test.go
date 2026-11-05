package aicall

import (
	"context"
	"testing"
)

func TestMetadataDefaultsPreserveActualWorkerStrategy(t *testing.T) {
	ctx := WithDefaults(context.Background(), "embedding.query", "lightweight")
	if got := MetadataFromContext(ctx); got.Stage != "embedding.query" || got.Strategy != "lightweight" {
		t.Fatalf("query policy lost: %+v", got)
	}
	ctx = WithDefaults(WithMetadata(context.Background(), "embedding.generate", "hybrid"), "embedding.query", "generative")
	if got := MetadataFromContext(ctx); got.Stage != "embedding.generate" || got.Strategy != "hybrid" {
		t.Fatalf("worker attribution overwritten: %+v", got)
	}
}
