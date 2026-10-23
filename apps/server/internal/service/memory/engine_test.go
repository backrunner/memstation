package memory

import (
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestExpandRelationScoresDoesNotPromoteZeroWeightEdges(t *testing.T) {
	scores, _ := expandRelationScores(map[string]float64{"seed": 1}, []domain.MemoryRelation{{SrcNodeID: "seed", DstNodeID: "disabled", RelationType: "related_to", Weight: 0}}, RelationRecallConfig{MaxHops: 2})
	if _, ok := scores["disabled"]; ok {
		t.Fatal("zero-weight edge promoted a neighbour")
	}
}

func TestExpandRelationScoresAppliesHopPenalty(t *testing.T) {
	t.Parallel()

	scores, _ := expandRelationScores(map[string]float64{
		"seed": 1,
	}, []domain.MemoryRelation{
		{SrcNodeID: "seed", DstNodeID: "hop-1", RelationType: "derived_from", Weight: 1},
		{SrcNodeID: "hop-1", DstNodeID: "hop-2", RelationType: "derived_from", Weight: 1},
		{SrcNodeID: "hop-2", DstNodeID: "hop-3", RelationType: "derived_from", Weight: 1},
	}, RelationRecallConfig{
		MaxHops:          3,
		HopDecayLambda:   0.35,
		HopDecayExponent: 1.25,
	})

	if scores["hop-1"] <= 0 || scores["hop-2"] <= 0 || scores["hop-3"] <= 0 {
		t.Fatalf("expected hop-expanded scores to remain positive, got %+v", scores)
	}
	if scores["hop-1"] <= scores["hop-2"] {
		t.Fatalf("expected 1-hop score to outrank 2-hop score, got hop1=%f hop2=%f", scores["hop-1"], scores["hop-2"])
	}
	if scores["hop-2"] <= scores["hop-3"] {
		t.Fatalf("expected 2-hop score to outrank 3-hop score, got hop2=%f hop3=%f", scores["hop-2"], scores["hop-3"])
	}
}

func TestRelationRecallConfigHopPenaltyGrowsWithDistance(t *testing.T) {
	t.Parallel()

	cfg := RelationRecallConfig{
		MaxHops:          4,
		HopDecayLambda:   0.35,
		HopDecayExponent: 1.25,
	}

	penalty1 := cfg.hopPenalty(0)
	penalty2 := cfg.hopPenalty(1)
	penalty3 := cfg.hopPenalty(2)

	if penalty1 <= 0 || penalty2 <= 0 || penalty3 <= 0 {
		t.Fatalf("expected positive hop penalties, got hop1=%f hop2=%f hop3=%f", penalty1, penalty2, penalty3)
	}
	if penalty1 != 1 {
		t.Fatalf("expected first-hop relation retention to preserve the seed score, got %f", penalty1)
	}
	if penalty1 <= penalty2 {
		t.Fatalf("expected first-hop relation retention to stay above second-hop retention, got hop1=%f hop2=%f", penalty1, penalty2)
	}
	if penalty2 <= penalty3 {
		t.Fatalf("expected second-hop relation retention to stay above third-hop retention, got hop2=%f hop3=%f", penalty2, penalty3)
	}
}

func TestExpandRelationScoresTraversesSemanticEdgesInBothDirections(t *testing.T) {
	t.Parallel()

	scores, _ := expandRelationScores(map[string]float64{"summary": 1}, []domain.MemoryRelation{
		{SrcNodeID: "segment", DstNodeID: "summary", RelationType: "summarized_into", Weight: 1},
	}, RelationRecallConfig{MaxHops: 1, HopDecayLambda: 0.35, HopDecayExponent: 1.25})
	if scores["segment"] <= 0 {
		t.Fatalf("expected summary seed to reach its supporting segment, got %+v", scores)
	}
}

func TestRelationPathsConfidenceDirectionAndConflict(t *testing.T) {
	edges := []domain.MemoryRelation{
		{ID: "dependency", SrcNodeID: "a", DstNodeID: "b", RelationType: domain.MemoryRelationDependsOn, Weight: 1, Metadata: map[string]any{"confidence": 0.5}},
		{ID: "conflict", SrcNodeID: "a", DstNodeID: "c", RelationType: domain.MemoryRelationContradicts, Weight: 1},
	}
	scores, _, paths := expandRelationPaths(map[string]float64{"a": 1}, edges, RelationRecallConfig{MaxHops: 2})
	if scores["b"] != 0.08 || scores["c"] != 0 {
		t.Fatalf("typed scoring: %v", scores)
	}
	if paths["b"].SeedNodeID != "a" || len(paths["b"].RelationIDs) != 1 || paths["b"].RelationIDs[0] != "dependency" {
		t.Fatalf("missing path: %+v", paths)
	}
	reverse, _ := expandRelationScores(map[string]float64{"b": 1}, edges, RelationRecallConfig{MaxHops: 2})
	if reverse["a"] != 0 {
		t.Fatal("dependency direction ignored")
	}
	conflicts, _ := expandRelationScores(map[string]float64{"a": 1}, edges, RelationRecallConfig{MaxHops: 2, Mode: "conflicts"})
	if conflicts["c"] <= 0 {
		t.Fatal("conflict query omitted contradictory evidence")
	}
}
