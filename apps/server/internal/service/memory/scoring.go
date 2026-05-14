package memory

import (
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
	BaseSemanticScore float64 `json:"base_semantic_score"`
	KeywordScore      float64 `json:"keyword_score"`
	DirectScore       float64 `json:"direct_score"`
	RerankScore       float64 `json:"rerank_score"`
	RerankWeight      float64 `json:"rerank_weight"`
	RelationExpansion float64 `json:"relation_expansion"`
	SupersessionBoost float64 `json:"supersession_boost"`
	ConversationBoost float64 `json:"conversation_boost"`
	SemanticScore     float64 `json:"semantic_score"`
	ImportanceWeight  float64 `json:"importance_weight"`
	TimeDecay         float64 `json:"time_decay"`
	FreshnessPenalty  float64 `json:"freshness_penalty"`
	EffectiveScore    float64 `json:"effective_score"`
}

type searchConfig struct {
	useRelations  bool
	useImportance bool
	useTimeDecay  bool
}

func configForVariant(variant SearchVariant) searchConfig {
	switch variant {
	case SearchVariantSemanticOnly:
		return searchConfig{useRelations: false}
	case SearchVariantSemanticImportance:
		return searchConfig{useImportance: true}
	case SearchVariantFullNoRelations:
		return searchConfig{useImportance: true, useTimeDecay: true}
	case SearchVariantFull:
		fallthrough
	default:
		return searchConfig{useRelations: true, useImportance: true, useTimeDecay: true}
	}
}

func scoreDocument(doc domain.MemoryDocument, baseSemanticScore, keywordScore, relationExpansion, supersessionBoost, conversationBoost, rerankScore, rerankWeight float64, cfg searchConfig) ScoreComponents {
	return scoreDocumentAt(doc, baseSemanticScore, keywordScore, relationExpansion, supersessionBoost, conversationBoost, rerankScore, rerankWeight, time.Now().UTC(), cfg)
}

func scoreDocumentAt(doc domain.MemoryDocument, baseSemanticScore, keywordScore, relationExpansion, supersessionBoost, conversationBoost, rerankScore, rerankWeight float64, now time.Time, cfg searchConfig) ScoreComponents {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	directScore := combineDirectSignal(baseSemanticScore, keywordScore)
	if rerankWeight > 1 {
		rerankWeight = 1
	}
	if rerankWeight < 0 {
		rerankWeight = 0
	}
	components := ScoreComponents{
		BaseSemanticScore: baseSemanticScore,
		KeywordScore:      keywordScore,
		DirectScore:       directScore,
		RerankScore:       rerankScore,
		RerankWeight:      rerankWeight,
		RelationExpansion: relationExpansion,
		SupersessionBoost: supersessionBoost,
		ConversationBoost: conversationBoost,
		SemanticScore:     blendDirectScore(directScore, rerankScore, rerankWeight) + relationExpansion + supersessionBoost + conversationBoost,
		ImportanceWeight:  1,
		TimeDecay:         1,
		FreshnessPenalty:  1,
	}
	components.EffectiveScore = components.SemanticScore
	if cfg.useImportance {
		components.ImportanceWeight = domain.ClampImportance(doc.Importance)
		components.EffectiveScore *= components.ImportanceWeight
	}
	if cfg.useTimeDecay {
		components.TimeDecay = domain.TimeDecay(doc.Type, doc.ActivityAt, doc.DecayFactor, doc.Importance, now)
		components.EffectiveScore *= components.TimeDecay
	}
	components.FreshnessPenalty = domain.StalePenalty(doc, now)
	components.EffectiveScore *= components.FreshnessPenalty
	return components
}

func ExplainDocumentScore(doc domain.MemoryDocument, baseSemanticScore, keywordScore, relationExpansion, supersessionBoost, conversationBoost, rerankScore, rerankWeight float64, variant SearchVariant) ScoreComponents {
	return scoreDocument(doc, baseSemanticScore, keywordScore, relationExpansion, supersessionBoost, conversationBoost, rerankScore, rerankWeight, configForVariant(variant))
}

func keywordSignalBoost(keywordScore float64) float64 {
	if keywordScore <= 0 {
		return 0
	}
	return keywordScore * 0.35
}

func combineDirectSignal(baseSemanticScore, keywordScore float64) float64 {
	switch {
	case baseSemanticScore <= 0:
		return keywordScore
	case keywordScore <= 0:
		return baseSemanticScore
	default:
		return baseSemanticScore + keywordSignalBoost(keywordScore)
	}
}

func blendDirectScore(directScore, rerankScore, rerankWeight float64) float64 {
	if rerankWeight <= 0 {
		return directScore
	}
	return ((1 - rerankWeight) * directScore) + (rerankWeight * rerankScore)
}
