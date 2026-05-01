package analysis

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/observability"
	conversationservice "memstation/apps/server/internal/service/conversation"
)

type Service struct{}

type Input struct {
	UserID         string
	ConversationID string
	Conversation   domain.ConversationRecord
	Summary        domain.MemoryDocument
	ExistingFacts  []domain.MemoryDocument
}

type FallbackExtraction struct {
	FactContent        string
	FactMetadata       map[string]any
	ImpressionContent  string
	ImpressionMetadata map[string]any
}

type Result struct {
	Fact       domain.MemoryDocument
	Impression domain.MemoryDocument
	Relations  []domain.MemoryRelation
}

func NewService() *Service { return &Service{} }

func (s *Service) AnalyzeConversation(ctx context.Context, input Input) (Result, error) {
	ctx, span := observability.TraceSpan(ctx, "analysis.analyze_conversation",
		attribute.String("analysis.user_id", firstNonEmpty(input.UserID, input.Conversation.UserID)),
		attribute.String("analysis.conversation_id", firstNonEmpty(input.ConversationID, input.Conversation.ConversationID)),
	)
	defer span.End()

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	userID := firstNonEmpty(input.UserID, input.Conversation.UserID)
	conversationID := firstNonEmpty(input.ConversationID, input.Conversation.ConversationID)
	summary := strings.TrimSpace(input.Summary.Content)
	if summary == "" {
		summary = conversationservice.SummarizeSegments(input.Conversation.Segments)
	}
	if summary == "" {
		summary = summarizeConversation(input.Conversation.Messages)
	}

	extracted, err := s.RunFallbackExtraction(ctx, summary)
	if err != nil {
		return Result{}, err
	}

	fact := domain.MemoryDocument{
		ID:             FactDocumentID(conversationID),
		NodeID:         FactNodeID(conversationID),
		UserID:         userID,
		ConversationID: conversationID,
		Type:           domain.MemoryTypeFact,
		Content:        extracted.FactContent,
		Importance:     1.05,
		DecayFactor:    0.98,
		Metadata:       cloneMap(extracted.FactMetadata),
	}
	if supersededIDs := conflictingFactIDs(fact, input.ExistingFacts); len(supersededIDs) > 0 {
		fact.Metadata["supersedes_fact_ids"] = supersededIDs
	}
	impression := domain.MemoryDocument{
		ID:             ImpressionDocumentID(conversationID),
		NodeID:         ImpressionNodeID(conversationID),
		UserID:         userID,
		ConversationID: conversationID,
		Type:           domain.MemoryTypeImpression,
		Content:        extracted.ImpressionContent,
		Importance:     0.85,
		DecayFactor:    0.95,
		Metadata:       cloneMap(extracted.ImpressionMetadata),
	}

	relations := make([]domain.MemoryRelation, 0, 3)
	if input.Summary.NodeID != "" {
		relations = append(relations,
			domain.MemoryRelation{
				UserID:       userID,
				SrcNodeID:    input.Summary.NodeID,
				DstNodeID:    fact.NodeID,
				RelationType: "derived_from",
				Weight:       0.92,
				Source:       "analysis",
				Metadata: map[string]any{
					"conversation_id": conversationID,
				},
			},
			domain.MemoryRelation{
				UserID:       userID,
				SrcNodeID:    input.Summary.NodeID,
				DstNodeID:    impression.NodeID,
				RelationType: "derived_from",
				Weight:       0.78,
				Source:       "analysis",
				Metadata: map[string]any{
					"conversation_id": conversationID,
				},
			},
		)
	}
	relations = append(relations, domain.MemoryRelation{
		UserID:       userID,
		SrcNodeID:    fact.NodeID,
		DstNodeID:    impression.NodeID,
		RelationType: "derived_from",
		Weight:       0.8,
		Source:       "analysis",
		Metadata: map[string]any{
			"conversation_id": conversationID,
		},
	})

	return Result{
		Fact:       fact,
		Impression: impression,
		Relations:  relations,
	}, nil
}

func (s *Service) RunFallbackExtraction(ctx context.Context, summary string) (FallbackExtraction, error) {
	ctx, span := observability.TraceSpan(ctx, "analysis.run_fallback_extraction",
		attribute.Int("analysis.summary_length", len(strings.TrimSpace(summary))),
	)
	defer span.End()

	if err := ctx.Err(); err != nil {
		return FallbackExtraction{}, err
	}

	summary = normalizeWhitespace(summary)
	if summary == "" {
		summary = "No summary available."
	}

	keywords := extractKeywords(summary)
	topic := strings.Join(keywords[:min(len(keywords), 3)], ", ")
	factAnchor := leadingSentence(summary)
	if factAnchor == "" {
		factAnchor = summary
	}

	factContent := "Recent topic: " + truncate(cleanLead(factAnchor), 160)
	if topic != "" {
		factContent = fmt.Sprintf("Recent topic keywords: %s. %s", topic, factContent)
	}

	impressionContent, impressionMetadata := inferImpression(summary)

	return FallbackExtraction{
		FactContent: factContent,
		FactMetadata: map[string]any{
			"category":   classifyFactCategory(summary, keywords),
			"subject":    "user",
			"fact_key":   classifyFactKey(summary, keywords),
			"confidence": inferConfidence(summary),
		},
		ImpressionContent:  impressionContent,
		ImpressionMetadata: impressionMetadata,
	}, nil
}

// classifyFactCategory maps summary text to the planned category taxonomy.
// Uses keyword heuristics as a pragmatic v1.0 fallback; a richer LLM-driven
// classifier can swap in later without changing downstream consumers.
func classifyFactCategory(summary string, keywords []string) string {
	haystack := strings.ToLower(summary)
	for _, kw := range keywords {
		haystack += " " + strings.ToLower(kw)
	}
	switch {
	case containsAny(haystack, "mother", "father", "wife", "husband", "sister", "brother", "child", "son", "daughter", "parent", "family"):
		return "family"
	case containsAny(haystack, "friend", "colleague", "teammate", "partner", "boss", "manager", "neighbor", "relationship"):
		return "relationship"
	case containsAny(haystack, "anxious", "anxiety", "depression", "feel ", "feeling", "mood", "stress", "therapy", "psych"):
		return "psychology"
	case containsAny(haystack, "salary", "stock", "income", "budget", "invest", "mortgage", "loan", "tax", "finance"):
		return "finance"
	case containsAny(haystack, "doctor", "clinic", "hospital", "medication", "diet", "exercise", "workout", "health", "sleep"):
		return "health"
	case containsAny(haystack, "office", "manager", "deadline", "standup", "meeting", "project", "work", "oncall", "release"):
		return "work"
	case containsAny(haystack, "exam", "course", "university", "college", "thesis", "research", "student", "study"):
		return "study"
	case containsAny(haystack, "shanghai", "beijing", "tokyo", "new york", "london", "city", "country", "timezone", "location", "commute", "address"):
		return "location"
	default:
		return "other"
	}
}

func classifyFactKey(summary string, keywords []string) string {
	haystack := strings.ToLower(summary)
	for _, kw := range keywords {
		haystack += " " + strings.ToLower(kw)
	}
	switch classifyFactCategory(summary, keywords) {
	case "location":
		if containsAny(haystack, "live", "lives", "living", "home", "moved", "relocat", "city", "address") {
			return "home_location"
		}
		return "location"
	case "work":
		if containsAny(haystack, "approver", "owner", "responsible", "manager", "boss") {
			return "work_owner"
		}
		if containsAny(haystack, "deadline", "due", "release", "oncall", "rotation") {
			return "work_schedule"
		}
	case "family":
		if containsAny(haystack, "mother", "father", "wife", "husband", "sister", "brother", "child", "son", "daughter", "parent") {
			return "family_relationship"
		}
	case "health":
		if containsAny(haystack, "doctor", "clinic", "hospital", "medication", "sleep", "diet", "exercise", "workout") {
			return "health_status"
		}
	}
	return ""
}

func conflictingFactIDs(candidate domain.MemoryDocument, existing []domain.MemoryDocument) []string {
	category := metadataString(candidate.Metadata, "category")
	subject := metadataString(candidate.Metadata, "subject")
	factKey := factKeyFor(candidate)
	if category == "" || subject == "" || factKey == "" {
		return nil
	}

	out := make([]string, 0)
	for _, item := range existing {
		if item.ID == "" || item.ID == candidate.ID || item.Type != domain.MemoryTypeFact {
			continue
		}
		if item.UserID != "" && candidate.UserID != "" && item.UserID != candidate.UserID {
			continue
		}
		if metadataString(item.Metadata, "superseded_by_id") != "" {
			continue
		}
		if metadataString(item.Metadata, "category") != category || metadataString(item.Metadata, "subject") != subject {
			continue
		}
		if factKeyFor(item) != factKey {
			continue
		}
		out = append(out, item.ID)
	}
	return out
}

func factKeyFor(doc domain.MemoryDocument) string {
	if key := metadataString(doc.Metadata, "fact_key"); key != "" {
		return key
	}
	return classifyFactKey(doc.Content, extractKeywords(doc.Content))
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

func FactDocumentID(conversationID string) string {
	return "fact:" + conversationID + ":recent-topic"
}

func FactNodeID(conversationID string) string {
	return "node:fact:" + conversationID + ":recent-topic"
}

func ImpressionDocumentID(conversationID string) string {
	return "impression:" + conversationID + ":tone"
}

func ImpressionNodeID(conversationID string) string {
	return "node:impression:" + conversationID + ":tone"
}

func summarizeConversation(messages []domain.UnifiedMessage) string {
	return conversationservice.BuildMemoryPromptInput(messages)
}

func inferImpression(summary string) (string, map[string]any) {
	lowered := strings.ToLower(summary)
	description := "Conversation tone appears collaborative and planning-oriented."
	metadata := map[string]any{
		"subject":      "conversation",
		"subject_type": "conversation",
		"evaluation":   "collaborative",
		"sentiment":    "positive",
		"familiarity":  "returning",
	}

	switch {
	case containsAny(lowered, "incident", "error", "bug", "failure", "retry", "timeout"):
		description = "Conversation tone appears operational and problem-solving oriented."
		metadata["evaluation"] = "problem_solving"
	case containsAny(lowered, "plan", "roadmap", "implement", "build", "design", "architecture"):
		description = "Conversation tone appears collaborative and planning-oriented."
		metadata["evaluation"] = "planning"
	case containsAny(lowered, "remember", "memory", "profile", "preference"):
		description = "Conversation tone appears reflective and memory-oriented."
		metadata["evaluation"] = "reflective"
	case containsAny(lowered, "why", "how", "what", "?"):
		description = "Conversation tone appears exploratory and question-driven."
		metadata["evaluation"] = "exploratory"
	}

	return description, metadata
}

func inferConfidence(summary string) float64 {
	length := len(strings.Fields(summary))
	switch {
	case length >= 30:
		return 0.86
	case length >= 12:
		return 0.8
	default:
		return 0.72
	}
}

func leadingSentence(value string) string {
	value = normalizeWhitespace(value)
	if value == "" {
		return ""
	}
	for _, separator := range []string{". ", "! ", "? ", " | "} {
		parts := strings.SplitN(value, separator, 2)
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return strings.TrimSpace(parts[0])
		}
	}
	return value
}

func cleanLead(value string) string {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"user:", "assistant:", "system:"} {
		if strings.HasPrefix(strings.ToLower(value), prefix) {
			return strings.TrimSpace(value[len(prefix):])
		}
	}
	return value
}

func extractKeywords(summary string) []string {
	seen := map[string]struct{}{}
	words := strings.Fields(strings.ToLower(summary))
	out := make([]string, 0, 5)
	for _, word := range words {
		word = strings.Trim(word, ".,;:!?()[]{}\"'")
		if len(word) < 4 {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		out = append(out, word)
		if len(out) == 5 {
			break
		}
	}
	return out
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func cloneMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
