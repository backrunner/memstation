package memory

import (
	"math"
	"time"

	"memstation/apps/server/internal/domain"
)

type SearchVariant string

const (
	SearchVariantFull               SearchVariant = "full"
	SearchVariantFullNoRelations    SearchVariant = "full_no_relations"
	SearchVariantSemanticOnly       SearchVariant = "semantic_only"
	SearchVariantSemanticImportance SearchVariant = "semantic_importance"
)

type ScoreComponents struct {
	ScorePolicy       string  `json:"score_policy"`
	BaseSemanticScore float64 `json:"base_semantic_score"`
	KeywordScore      float64 `json:"keyword_score"`
	DirectScore       float64 `json:"direct_score"`
	RerankScore       float64 `json:"rerank_score"`
	RerankApplied     bool    `json:"rerank_applied"`
	RelationExpansion float64 `json:"relation_expansion"`
	SupersessionBoost float64 `json:"supersession_boost"`
	ConversationBoost float64 `json:"conversation_boost"`
	SemanticScore     float64 `json:"semantic_score"`
	ImportanceWeight  float64 `json:"importance_weight"`
	TimeDecay         float64 `json:"time_decay"`
	FreshnessPenalty  float64 `json:"freshness_penalty"`
	EffectiveScore    float64 `json:"effective_score"`
}

type searchConfig struct{ useRelations, useImportance, useTimeDecay bool }

func configForVariant(v SearchVariant) searchConfig {
	switch v {
	case SearchVariantSemanticOnly:
		return searchConfig{}
	case SearchVariantSemanticImportance:
		return searchConfig{useImportance: true}
	case SearchVariantFullNoRelations:
		return searchConfig{useImportance: true, useTimeDecay: true}
	default:
		return searchConfig{useRelations: true, useImportance: true, useTimeDecay: true}
	}
}

func scoreDocument(doc domain.MemoryDocument, base, keyword, relation, supersession, conversation, rerank float64, rerankApplied bool, cfg searchConfig) ScoreComponents {
	return scoreDocumentAt(doc, base, keyword, relation, supersession, conversation, rerank, rerankApplied, time.Now().UTC(), cfg)
}

func scoreDocumentAt(doc domain.MemoryDocument, base, keyword, relation, supersession, conversation, rerank float64, rerankApplied bool, now time.Time, cfg searchConfig) ScoreComponents {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	// Candidate admission is separate from final query relevance.
	// Without a validated reranker batch, use bounded direct/graph relevance;
	// there is no alternate scoring policy or late conversation bonus.
	direct := combineDirectSignal(base, keyword)
	c := ScoreComponents{ScorePolicy: "hybrid_v1", BaseSemanticScore: base, KeywordScore: keyword, DirectScore: direct, RerankScore: rerank, RerankApplied: rerankApplied, RelationExpansion: relation, SupersessionBoost: supersession, ConversationBoost: conversation, SemanticScore: clamp(math.Max(direct, relation), 0, 1), ImportanceWeight: 1, TimeDecay: 1, FreshnessPenalty: domain.StalePenalty(doc, now)}
	if rerankApplied {
		c.ScorePolicy = "relevance_v1"
		c.SemanticScore = rerank
	}
	if cfg.useImportance {
		c.ImportanceWeight = clamp(domain.ClampImportance(doc.Importance), .9, 1.1)
	}
	if cfg.useTimeDecay {
		c.TimeDecay = .9 + .1*domain.TimeDecay(doc.Type, doc.ActivityAt, doc.DecayFactor, doc.Importance, now)
	}
	c.EffectiveScore = c.SemanticScore * c.ImportanceWeight * c.TimeDecay * c.FreshnessPenalty
	return c
}

func ExplainDocumentScore(doc domain.MemoryDocument, base, keyword, relation, supersession, conversation, rerank float64, rerankApplied bool, variant SearchVariant) ScoreComponents {
	return scoreDocument(doc, base, keyword, relation, supersession, conversation, rerank, rerankApplied, configForVariant(variant))
}

func ExplainDocumentScoreAt(doc domain.MemoryDocument, signals ScoreComponents, now time.Time, variant SearchVariant, rerankApplied bool) ScoreComponents {
	return scoreDocumentAt(doc, signals.BaseSemanticScore, signals.KeywordScore, signals.RelationExpansion, signals.SupersessionBoost, signals.ConversationBoost, signals.RerankScore, rerankApplied, now, configForVariant(variant))
}

func combineDirectSignal(base, keyword float64) float64 { return clamp(math.Max(base, keyword), 0, 1) }
