package memory

import (
	"testing"

	"memstation/apps/server/internal/domain"
)

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
	if penalty1 >= 1 {
		t.Fatalf("expected first-hop relation penalty to remain below direct semantic recall, got %f", penalty1)
	}
	if penalty1 <= penalty2 {
		t.Fatalf("expected first-hop relation retention to stay above second-hop retention, got hop1=%f hop2=%f", penalty1, penalty2)
	}
	if penalty2 <= penalty3 {
		t.Fatalf("expected second-hop relation retention to stay above third-hop retention, got hop2=%f hop3=%f", penalty2, penalty3)
	}
}
