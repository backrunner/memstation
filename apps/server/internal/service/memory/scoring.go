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

func scoreDocument(doc domain.MemoryDocument, baseSemanticScore, keywordScore, relationExpansion, supersessionBoost, conversationBoost float64, cfg searchConfig) ScoreComponents {
	components := ScoreComponents{
		BaseSemanticScore: baseSemanticScore,
		KeywordScore:      keywordScore,
		RelationExpansion: relationExpansion,
		SupersessionBoost: supersessionBoost,
		ConversationBoost: conversationBoost,
		SemanticScore:     combineDirectSignal(baseSemanticScore, keywordScore) + relationExpansion + supersessionBoost + conversationBoost,
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
		components.TimeDecay = domain.TimeDecay(doc.Type, doc.ActivityAt, doc.DecayFactor, doc.Importance, time.Now().UTC())
		components.EffectiveScore *= components.TimeDecay
	}
	components.FreshnessPenalty = domain.StalePenalty(doc, time.Now().UTC())
	components.EffectiveScore *= components.FreshnessPenalty
	return components
}

func ExplainDocumentScore(doc domain.MemoryDocument, baseSemanticScore, keywordScore, relationExpansion, supersessionBoost, conversationBoost float64, variant SearchVariant) ScoreComponents {
	return scoreDocument(doc, baseSemanticScore, keywordScore, relationExpansion, supersessionBoost, conversationBoost, configForVariant(variant))
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
