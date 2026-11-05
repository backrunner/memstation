package grpctransport

import (
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestSearchProjectionPreservesEntityRecallDiagnostics(t *testing.T) {
	out := toProtoSearchResult(domain.SearchResult{EntityRecall: &domain.EntityRecallDiagnostics{Status: "complete", CacheHit: true, QueryEntities: 2, MatchedNodes: 3, LatencyMS: 17}})
	d := out.GetEntityRecall()
	if d == nil || d.GetStatus() != "complete" || !d.GetCacheHit() || d.GetQueryEntities() != 2 || d.GetMatchedNodes() != 3 || d.GetLatencyMs() != 17 {
		t.Fatalf("diagnostics omitted in gRPC: %+v", d)
	}
}
