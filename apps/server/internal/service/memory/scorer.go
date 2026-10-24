package memory

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/service/memoryllm"
	settingsservice "memstation/apps/server/internal/service/settings"
)

type MemoryScore struct {
	Importance         float64   `json:"importance"`
	DecayFactor        float64   `json:"decay_factor"`
	ActivityAt         time.Time `json:"activity_at"`
	KeyMemoryCandidate bool      `json:"key_memory_candidate"`
	Reason             string    `json:"reason"`
}

type ScoreInput struct {
	UserID         string
	MemoryType     domain.MemoryType
	Content        string
	ConversationID string
	Metadata       map[string]any
	ReferenceTime  time.Time
}

type Scorer struct{ generator memoryllm.Generator }

func NewScorer(providers *provider.Registry, settings *settingsservice.Service) *Scorer {
	return &Scorer{generator: memoryllm.NewClient(providers, settings, 90*time.Second)}
}

func (s *Scorer) Score(ctx context.Context, input ScoreInput) (MemoryScore, error) {
	if s == nil || s.generator == nil {
		return MemoryScore{}, errors.New("memory scoring LLM is not configured")
	}
	role := "analysis_model"
	if input.MemoryType == domain.MemoryTypeSummary || input.MemoryType == domain.MemoryTypeSegment {
		role = "summary_model"
	}
	reference := domain.DeriveActivityAt(input.ReferenceTime, time.Time{}, time.Now().UTC())
	raw, err := s.generator.Generate(ctx, memoryllm.Request{
		UserID: input.UserID, Role: role, Task: "memory.score", MaxOutputTokens: 2048,
		Instruction: `Score the supplied memory's durability. Return {"importance":number,"decay_factor":number,"activity_at":"RFC3339","key_memory_candidate":boolean,"reason":string}. Importance 0.5..2, decay_factor 0.5..3. Do not infer new facts. Reuse reference_time if the source provides no reliable activity time.`,
		Input:       map[string]any{"memory_type": input.MemoryType, "content": input.Content, "reference_time": reference.UTC().Format(time.RFC3339)},
	})
	if err != nil {
		return MemoryScore{}, err
	}
	var score MemoryScore
	if err := memoryllm.Decode(raw, &score); err != nil {
		return MemoryScore{}, err
	}
	if math.IsNaN(score.Importance) || math.IsInf(score.Importance, 0) || score.Importance < .5 || score.Importance > 2 || math.IsNaN(score.DecayFactor) || math.IsInf(score.DecayFactor, 0) || score.DecayFactor < .5 || score.DecayFactor > 3 || score.ActivityAt.IsZero() || strings.TrimSpace(score.Reason) == "" {
		return MemoryScore{}, errors.New("invalid LLM memory score")
	}
	return score, nil
}
