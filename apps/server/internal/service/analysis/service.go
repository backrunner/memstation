package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	Facts              []ExtractedFact
	ImpressionContent  string
	ImpressionMetadata map[string]any
}

type ExtractedFact struct {
	Content  string
	Metadata map[string]any
}

type Result struct {
	Fact       domain.MemoryDocument
	Facts      []domain.MemoryDocument
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

	extractedFacts := extracted.Facts
	if len(extractedFacts) == 0 {
		extractedFacts = []ExtractedFact{{
			Content:  extracted.FactContent,
			Metadata: extracted.FactMetadata,
		}}
	}
	facts := make([]domain.MemoryDocument, 0, len(extractedFacts))
	for _, extractedFact := range extractedFacts {
		content := strings.TrimSpace(extractedFact.Content)
		if content == "" {
			continue
		}
		fact := domain.MemoryDocument{
			UserID:         userID,
			ConversationID: conversationID,
			Type:           domain.MemoryTypeFact,
			Content:        content,
			Importance:     1.05,
			DecayFactor:    0.98,
			Metadata:       cloneMap(extractedFact.Metadata),
		}
		fact = applyFactLineageMetadata(fact, userID, conversationID)
		fact.ID = FactRevisionDocumentID(conversationID, metadataString(fact.Metadata, "fact_key"), metadataString(fact.Metadata, "lineage_revision_key"))
		fact.NodeID = FactRevisionNodeID(conversationID, metadataString(fact.Metadata, "fact_key"), metadataString(fact.Metadata, "lineage_revision_key"))
		if supersededIDs := conflictingFactIDs(fact, input.ExistingFacts); len(supersededIDs) > 0 {
			fact.Metadata["supersedes_fact_ids"] = supersededIDs
		}
		facts = append(facts, fact)
	}
	if len(facts) == 0 {
		return Result{}, fmt.Errorf("analysis produced no facts")
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

	relations := make([]domain.MemoryRelation, 0, len(facts)*2+1)
	if input.Summary.NodeID != "" {
		for _, fact := range facts {
			relations = append(relations, domain.MemoryRelation{
				UserID:       userID,
				SrcNodeID:    input.Summary.NodeID,
				DstNodeID:    fact.NodeID,
				RelationType: "derived_from",
				Weight:       0.92,
				Source:       "analysis",
				Metadata: map[string]any{
					"conversation_id": conversationID,
				},
			})
		}
		relations = append(relations, domain.MemoryRelation{
			UserID:       userID,
			SrcNodeID:    input.Summary.NodeID,
			DstNodeID:    impression.NodeID,
			RelationType: "derived_from",
			Weight:       0.78,
			Source:       "analysis",
			Metadata: map[string]any{
				"conversation_id": conversationID,
			},
		})
	}
	for _, fact := range facts {
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
	}

	return Result{
		Fact:       facts[0],
		Facts:      facts,
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
	baseFact := ExtractedFact{
		Content: factContent,
		Metadata: map[string]any{
			"category":   classifyFactCategory(summary, keywords),
			"subject":    "user",
			"fact_key":   classifyFactKey(summary, keywords),
			"confidence": inferConfidence(summary),
			"fact_layer": "canonical_fact",
			"fact_kind":  "topic",
		},
	}
	facts := deriveStructuredFacts(summary, keywords)
	if len(facts) == 0 {
		facts = []ExtractedFact{baseFact}
	}
	facts = dedupeExtractedFacts(facts)
	primaryFact := facts[0]

	impressionContent, impressionMetadata := inferImpression(summary)

	return FallbackExtraction{
		FactContent:        primaryFact.Content,
		FactMetadata:       cloneMap(primaryFact.Metadata),
		Facts:              facts,
		ImpressionContent:  impressionContent,
		ImpressionMetadata: impressionMetadata,
	}, nil
}

func deriveStructuredFacts(summary string, keywords []string) []ExtractedFact {
	haystack := strings.ToLower(summary)
	confidence := inferConfidence(summary)
	facts := make([]ExtractedFact, 0, 4)
	add := func(content string, metadata map[string]any) {
		content = strings.TrimSpace(content)
		if content == "" {
			return
		}
		if metadata == nil {
			metadata = map[string]any{}
		}
		if _, ok := metadata["confidence"]; !ok {
			metadata["confidence"] = confidence
		}
		metadata["subject"] = firstNonEmpty(metadataString(metadata, "subject"), "user")
		metadata["fact_layer"] = firstNonEmpty(metadataString(metadata, "fact_layer"), "canonical_fact")
		facts = append(facts, ExtractedFact{Content: content, Metadata: metadata})
	}

	if containsAny(haystack, "now lives", "currently lives", "moved to", "relocated to", "home address", "住在", "搬到") {
		add("Current home location: "+truncate(sentenceContainingAny(summary, "now lives", "currently lives", "moved to", "relocated to", "home address", "住在", "搬到"), 180), map[string]any{
			"category":  "location",
			"fact_key":  "home_location",
			"fact_kind": "entity",
		})
	}
	if containsAny(haystack, "prefer", "prefers", "preference", "likes", "favorite", "希望", "喜欢", "偏好") {
		add("User preference: "+truncate(sentenceContainingAny(summary, "prefer", "prefers", "preference", "likes", "favorite", "希望", "喜欢", "偏好"), 180), map[string]any{
			"category":  "preference",
			"fact_key":  preferenceFactKey(haystack),
			"fact_kind": "preference",
		})
	}
	if containsAny(haystack, "owner", "owns", "responsible", "approver", "manager", "负责人", "owner") {
		add("Work responsibility: "+truncate(sentenceContainingAny(summary, "owner", "owns", "responsible", "approver", "manager", "负责人"), 180), map[string]any{
			"category":  "work",
			"fact_key":  workOwnerFactKey(haystack, keywords),
			"fact_kind": "relationship",
		})
	}
	if containsAny(haystack, "mother", "father", "wife", "husband", "sister", "brother", "friend", "colleague", "teammate", "妈妈", "爸爸", "同事", "朋友") {
		add("Relationship fact: "+truncate(sentenceContainingAny(summary, "mother", "father", "wife", "husband", "sister", "brother", "friend", "colleague", "teammate", "妈妈", "爸爸", "同事", "朋友"), 180), map[string]any{
			"category":  classifyRelationshipCategory(haystack),
			"fact_key":  relationshipFactKey(haystack),
			"fact_kind": "relationship",
		})
	}
	if containsAny(haystack, "plan", "plans", "planned", "will ", "todo", "deadline", "due ", "next week", "next month", "计划", "准备", "下周", "下个月") {
		for _, sentence := range sentencesContainingAny(summary, "plan", "plans", "planned", "will ", "todo", "deadline", "due ", "next week", "next month", "计划", "准备", "下周", "下个月") {
			sentenceHaystack := strings.ToLower(sentence)
			add("User plan: "+truncate(sentence, 180), map[string]any{
				"category":  "plan",
				"fact_key":  planFactKey(sentenceHaystack, append(extractKeywords(sentence), keywords...)),
				"fact_kind": "plan",
			})
		}
	}
	return dedupeExtractedFacts(facts)
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
	case containsAny(haystack, "prefer", "prefers", "preference", "likes", "favorite", "theme", "language", "希望", "喜欢", "偏好"):
		return "preference"
	case containsAny(haystack, "office", "manager", "deadline", "standup", "meeting", "project", "work", "oncall", "release"):
		return "work"
	case containsAny(haystack, "exam", "course", "university", "college", "thesis", "research", "student", "study"):
		return "study"
	case containsAny(haystack, "shanghai", "beijing", "tokyo", "new york", "london", "city", "country", "timezone", "location", "commute", "address"):
		return "location"
	case containsAny(haystack, "plan", "plans", "planned", "will ", "todo", "next week", "next month", "计划", "准备", "下周", "下个月"):
		return "plan"
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
			return workOwnerFactKey(haystack, keywords)
		}
		if containsAny(haystack, "deadline", "due", "release", "oncall", "rotation") {
			return scopedFactKey("work_schedule", haystack, keywords)
		}
	case "family":
		if containsAny(haystack, "mother", "father", "wife", "husband", "sister", "brother", "child", "son", "daughter", "parent") {
			return "family_relationship"
		}
	case "health":
		if containsAny(haystack, "doctor", "clinic", "hospital", "medication", "sleep", "diet", "exercise", "workout") {
			return "health_status"
		}
	case "preference":
		return preferenceFactKey(haystack)
	case "plan":
		return planFactKey(haystack, keywords)
	}
	return ""
}

func preferenceFactKey(haystack string) string {
	switch {
	case containsAny(haystack, "theme", "dark", "light", "ui", "interface", "界面", "主题"):
		if containsAny(haystack, "layout", "compact", "dense", "sidebar", "table", "布局", "紧凑") {
			return "preference_ui_layout"
		}
		if containsAny(haystack, "font", "typography", "字体") {
			return "preference_ui_font"
		}
		return "preference_ui_theme"
	case containsAny(haystack, "language", "english", "chinese", "中文", "英文"):
		return "preference_language"
	case containsAny(haystack, "model", "gpt", "claude", "gemini", "模型"):
		return "preference_model"
	default:
		return scopedFactKey("user_preference", haystack, nil)
	}
}

func planFactKey(haystack string, keywords []string) string {
	switch {
	case containsAny(haystack, "move", "relocat", "address", "city", "搬", "迁"):
		return "plan_relocation"
	case containsAny(haystack, "deadline", "due ", "release", "launch", "oncall", "下周", "下个月"):
		return scopedFactKey("work_schedule", haystack, keywords)
	default:
		return scopedFactKey("user_plan", haystack, keywords)
	}
}

func workOwnerFactKey(haystack string, keywords []string) string {
	for _, marker := range []string{"owner of ", "owns ", "responsible for ", "approver for ", "manager for ", "负责人 "} {
		if suffix := suffixAfterMarker(haystack, marker); suffix != "" {
			return "work_owner_" + suffix
		}
	}
	return scopedFactKey("work_owner", haystack, keywords)
}

func scopedFactKey(prefix, haystack string, keywords []string) string {
	if suffix := lineageTopicSuffix(haystack, keywords); suffix != "" {
		return prefix + "_" + suffix
	}
	normalized := normalizeWhitespace(haystack)
	if normalized == "" {
		return prefix
	}
	sum := sha256.Sum256([]byte(normalized))
	return prefix + "_" + hex.EncodeToString(sum[:])[:8]
}

func lineageTopicSuffix(haystack string, keywords []string) string {
	for _, marker := range []string{"project ", "service ", "system ", "app ", "repo ", "repository ", "产品", "项目"} {
		if suffix := suffixAfterMarker(haystack, marker); suffix != "" {
			return suffix
		}
	}
	for _, keyword := range keywords {
		if suffix := candidateLineageSuffix(keyword); suffix != "" {
			return suffix
		}
	}
	for _, keyword := range extractKeywords(haystack) {
		if suffix := candidateLineageSuffix(keyword); suffix != "" {
			return suffix
		}
	}
	return ""
}

func suffixAfterMarker(haystack, marker string) string {
	idx := strings.Index(haystack, marker)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(haystack[idx+len(marker):])
	if rest == "" {
		return ""
	}
	fields := strings.Fields(rest)
	for _, field := range fields[:min(len(fields), 4)] {
		if suffix := candidateLineageSuffix(field); suffix != "" {
			return suffix
		}
	}
	return ""
}

func candidateLineageSuffix(value string) string {
	value = strings.Trim(value, ".,;:!?()[]{}\"'")
	if value == "" {
		return ""
	}
	key := sanitizeFactKey(value)
	if key == "" || key == "unknown" || len(key) < 3 || genericFactKeySuffix(key) {
		return ""
	}
	return key
}

func genericFactKeySuffix(value string) bool {
	switch value {
	case "user", "users", "their", "the", "and", "for", "with", "plan", "plans", "planned", "will", "todo", "deadline", "next", "week", "month",
		"preference", "prefers", "prefer", "likes", "favorite", "work", "owner", "owns", "responsible",
		"manager", "approver", "release", "launch", "service", "system", "project", "current", "recent",
		"renew", "refactor":
		return true
	default:
		return false
	}
}

func relationshipFactKey(haystack string) string {
	switch {
	case containsAny(haystack, "mother", "father", "wife", "husband", "sister", "brother", "child", "son", "daughter", "parent", "妈妈", "爸爸"):
		return "family_relationship"
	default:
		return "relationship"
	}
}

func classifyRelationshipCategory(haystack string) string {
	if relationshipFactKey(haystack) == "family_relationship" {
		return "family"
	}
	return "relationship"
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

func applyFactLineageMetadata(doc domain.MemoryDocument, userID, conversationID string) domain.MemoryDocument {
	if doc.Type != domain.MemoryTypeFact {
		return doc
	}
	if doc.Metadata == nil {
		doc.Metadata = map[string]any{}
	}
	factKey := factKeyFor(doc)
	if factKey == "" {
		factKey = sanitizeFactKey(doc.ID)
	}
	doc.Metadata["fact_key"] = factKey
	doc.Metadata["lineage_group_id"] = factLineageGroupID(userID, factKey)
	doc.Metadata["lineage_revision_key"] = factLineageRevisionKey(userID, conversationID, factKey, doc.Content, doc.Metadata)
	return doc
}

func factLineageGroupID(userID, factKey string) string {
	return fmt.Sprintf("fact-lineage:%s:%s", strings.TrimSpace(userID), sanitizeFactKey(factKey))
}

func factLineageRevisionKey(userID, conversationID, factKey, content string, metadata map[string]any) string {
	builder := strings.Builder{}
	builder.Grow(len(userID) + len(conversationID) + len(factKey) + len(content) + 32)
	builder.WriteString(sanitizeFactKey(userID))
	builder.WriteByte('|')
	builder.WriteString(sanitizeFactKey(conversationID))
	builder.WriteByte('|')
	builder.WriteString(sanitizeFactKey(factKey))
	builder.WriteByte('|')
	builder.WriteString(strings.TrimSpace(metadataString(metadata, "category")))
	builder.WriteByte('|')
	builder.WriteString(strings.TrimSpace(metadataString(metadata, "subject")))
	builder.WriteByte('|')
	builder.WriteString(normalizeWhitespace(content))
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

func sanitizeFactKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	b.Grow(len(value))
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastUnderscore = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	sanitized := strings.Trim(b.String(), "_")
	if sanitized == "" {
		return "unknown"
	}
	return sanitized
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

func FactRevisionDocumentID(conversationID, factKey, revisionKey string) string {
	return "fact:" + sanitizeFactKey(conversationID) + ":" + sanitizeFactKey(factKey) + ":" + lineageSuffix(revisionKey)
}

func FactRevisionNodeID(conversationID, factKey, revisionKey string) string {
	return "node:fact:" + sanitizeFactKey(conversationID) + ":" + sanitizeFactKey(factKey) + ":" + lineageSuffix(revisionKey)
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

func sentenceContainingAny(value string, needles ...string) string {
	value = normalizeWhitespace(value)
	if value == "" {
		return ""
	}
	for _, sentence := range splitFactSentences(value) {
		if containsAny(strings.ToLower(sentence), needles...) {
			return cleanLead(sentence)
		}
	}
	return cleanLead(leadingSentence(value))
}

func sentencesContainingAny(value string, needles ...string) []string {
	value = normalizeWhitespace(value)
	if value == "" {
		return nil
	}
	out := make([]string, 0)
	for _, sentence := range splitFactSentences(value) {
		if containsAny(strings.ToLower(sentence), needles...) {
			if cleaned := cleanLead(sentence); cleaned != "" {
				out = append(out, cleaned)
			}
		}
	}
	if len(out) == 0 {
		if fallback := cleanLead(leadingSentence(value)); fallback != "" {
			out = append(out, fallback)
		}
	}
	return out
}

func splitFactSentences(value string) []string {
	replacer := strings.NewReplacer("! ", ". ", "? ", ". ", " | ", ". ", "\n", ". ")
	parts := strings.Split(replacer.Replace(value), ". ")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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

func dedupeExtractedFacts(facts []ExtractedFact) []ExtractedFact {
	seen := map[string]struct{}{}
	out := make([]ExtractedFact, 0, len(facts))
	for _, fact := range facts {
		content := normalizeWhitespace(fact.Content)
		if content == "" {
			continue
		}
		fact.Content = content
		key := metadataString(fact.Metadata, "fact_key")
		if key == "" {
			key = sanitizeFactKey(content)
		}
		dedupeKey := key + "\x1f" + strings.ToLower(content)
		if _, ok := seen[dedupeKey]; ok {
			continue
		}
		seen[dedupeKey] = struct{}{}
		fact.Metadata = cloneMap(fact.Metadata)
		out = append(out, fact)
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

func lineageSuffix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "head"
	}
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
