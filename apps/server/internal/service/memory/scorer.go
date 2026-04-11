package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
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

type Scorer struct {
	providers *provider.Registry
	settings  *settingsservice.Service
}

func NewScorer(providers *provider.Registry, settings *settingsservice.Service) *Scorer {
	return &Scorer{providers: providers, settings: settings}
}

func (s *Scorer) Score(ctx context.Context, input ScoreInput) MemoryScore {
	fallback := fallbackScore(input)
	if s == nil || s.providers == nil || s.settings == nil {
		return fallback
	}
	snapshot, err := s.settings.SnapshotForUser(ctx, input.UserID)
	if err != nil {
		return fallback
	}
	modelRef := strings.TrimSpace(selectScoringModel(snapshot, input.MemoryType))
	if modelRef == "" {
		return fallback
	}
	providerName, modelName := splitModelReference(modelRef)
	adapter, err := s.providers.Resolve(providerName)
	if err != nil {
		return fallback
	}
	if modelName == "" {
		modelName = modelRef
	}

	referenceTime := domain.DeriveActivityAt(input.ReferenceTime, time.Time{}, time.Time{})
	request := domain.UnifiedRequest{
		UserID:     input.UserID,
		Protocol:   domain.ProtocolOpenAIChat,
		Provider:   providerName,
		Model:      modelName,
		Stream:     false,
		Mode:       domain.ModeTransparent,
		Messages:   scoringMessages(input, referenceTime),
		ReceivedAt: time.Now().UTC(),
	}
	response, err := adapter.Complete(ctx, request)
	if err != nil {
		return fallback
	}
	score, ok := parseScoringResponse(response.Message.Text, input, referenceTime)
	if !ok {
		return fallback
	}
	return score
}

func selectScoringModel(snapshot map[string]any, memoryType domain.MemoryType) string {
	switch memoryType {
	case domain.MemoryTypeSummary, domain.MemoryTypeSegment:
		if value := stringValueAny(snapshot["summary_model"]); value != "" {
			return value
		}
	case domain.MemoryTypeImpression, domain.MemoryTypeFact, domain.MemoryTypeKeyMemory:
		if value := stringValueAny(snapshot["analysis_model"]); value != "" {
			return value
		}
	}
	if value := stringValueAny(snapshot["analysis_model"]); value != "" {
		return value
	}
	return stringValueAny(snapshot["summary_model"])
}

func splitModelReference(value string) (domain.Provider, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	for _, separator := range []string{":", "/"} {
		parts := strings.SplitN(value, separator, 2)
		if len(parts) != 2 {
			continue
		}
		if providerName, ok := domain.ParseProvider(strings.TrimSpace(parts[0])); ok {
			return providerName, strings.TrimSpace(parts[1])
		}
	}
	return "", value
}

func scoringMessages(input ScoreInput, referenceTime time.Time) []domain.UnifiedMessage {
	payload := map[string]any{
		"memory_type":     input.MemoryType,
		"conversation_id": input.ConversationID,
		"content":         strings.TrimSpace(input.Content),
		"metadata":        input.Metadata,
		"reference_time":  referenceTime.UTC().Format(time.RFC3339),
	}
	raw, _ := json.Marshal(payload)
	return []domain.UnifiedMessage{
		{
			Role: domain.RoleSystem,
			Text: "You score memory durability for MemStation. Respond with JSON only: " +
				`{"importance":number,"decay_factor":number,"activity_at":"RFC3339","key_memory_candidate":boolean,"reason":"short string"}. ` +
				"importance must be 0.5-2.0. decay_factor must be 0.5-3.0. " +
				"Set higher importance and decay_factor for stable identity, preference, long-term project, responsibility, or operator-critical facts. " +
				"Set lower values for ephemeral chatter. If time is unclear, reuse reference_time.",
		},
		{
			Role: domain.RoleUser,
			Text: string(raw),
		},
	}
}

func parseScoringResponse(raw string, input ScoreInput, referenceTime time.Time) (MemoryScore, bool) {
	type scoringPayload struct {
		Importance         float64 `json:"importance"`
		DecayFactor        float64 `json:"decay_factor"`
		ActivityAt         string  `json:"activity_at"`
		KeyMemoryCandidate bool    `json:"key_memory_candidate"`
		Reason             string  `json:"reason"`
	}
	jsonText := extractJSONObject(raw)
	if jsonText == "" {
		return MemoryScore{}, false
	}
	var payload scoringPayload
	if err := json.Unmarshal([]byte(jsonText), &payload); err != nil {
		return MemoryScore{}, false
	}
	activityAt := referenceTime.UTC()
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.ActivityAt)); err == nil {
		activityAt = parsed.UTC()
	}
	score := MemoryScore{
		Importance:         domain.ClampImportance(payload.Importance),
		DecayFactor:        domain.ClampDecayFactor(payload.DecayFactor),
		ActivityAt:         domain.DeriveActivityAt(activityAt, referenceTime, referenceTime),
		KeyMemoryCandidate: payload.KeyMemoryCandidate,
		Reason:             strings.TrimSpace(payload.Reason),
	}
	if score.Importance == 0 {
		score.Importance = fallbackScore(input).Importance
	}
	if score.DecayFactor == 0 {
		score.DecayFactor = fallbackScore(input).DecayFactor
	}
	if score.ActivityAt.IsZero() {
		score.ActivityAt = domain.DeriveActivityAt(referenceTime, referenceTime, time.Now().UTC())
	}
	if score.Reason == "" {
		score.Reason = "llm_scored"
	}
	return score, true
}

func extractJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return ""
	}
	return raw[start : end+1]
}

func fallbackScore(input ScoreInput) MemoryScore {
	referenceTime := fallbackReferenceTime(input)
	content := strings.ToLower(strings.TrimSpace(input.Content))
	importance := 1.0
	decayFactor := 1.0
	keyMemoryCandidate := false

	switch input.MemoryType {
	case domain.MemoryTypeSummary:
		importance = 1.0
		decayFactor = 1.0
	case domain.MemoryTypeSegment:
		importance = 0.9
		decayFactor = 0.85
	case domain.MemoryTypeFact:
		importance = 1.15
		decayFactor = 1.2
	case domain.MemoryTypeImpression:
		importance = 0.85
		decayFactor = 0.9
	case domain.MemoryTypeKeyMemory:
		importance = 1.4
		decayFactor = 2.2
		keyMemoryCandidate = true
	}

	if containsAny(content, "prefer", "preference", "always", "usually", "favorite", "owner", "responsible", "project", "roadmap", "deadline", "family", "profile") {
		importance += 0.2
		decayFactor += 0.35
		keyMemoryCandidate = true
	}
	if containsAny(content, "today", "tomorrow", "this week", "later", "temporary", "ephemeral", "draft") {
		importance -= 0.15
		decayFactor -= 0.2
	}
	if metadataSource(input.Metadata) == "admin_rest" || metadataSource(input.Metadata) == "worker" {
		importance += 0.1
	}

	return MemoryScore{
		Importance:         domain.ClampImportance(importance),
		DecayFactor:        domain.ClampDecayFactor(decayFactor),
		ActivityAt:         referenceTime,
		KeyMemoryCandidate: keyMemoryCandidate,
		Reason:             "fallback_rule",
	}
}

func fallbackReferenceTime(input ScoreInput) time.Time {
	if activityAt := metadataTime(input.Metadata, "activity_at"); !activityAt.IsZero() {
		return activityAt
	}
	if confirmedAt := metadataTime(input.Metadata, "last_confirmed_at"); !confirmedAt.IsZero() {
		return confirmedAt
	}
	return domain.DeriveActivityAt(input.ReferenceTime, time.Time{}, time.Now().UTC())
}

func metadataSource(metadata map[string]any) string {
	value, _ := metadata["source"].(string)
	return strings.TrimSpace(strings.ToLower(value))
}

func containsAny(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func stringValueAny(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}

func metadataTime(metadata map[string]any, key string) time.Time {
	value, ok := metadata[key]
	if !ok || value == nil {
		return time.Time{}
	}
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC()
	case *time.Time:
		if typed != nil {
			return typed.UTC()
		}
	case string:
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed)); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
