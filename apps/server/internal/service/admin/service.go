package admin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/security"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/ports"
	analysisservice "memstation/apps/server/internal/service/analysis"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	settingssvc "memstation/apps/server/internal/service/settings"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
)

type Service struct {
	conversations ports.ConversationRepository
	media         ports.MediaRepository
	memories      ports.MemoryRepository
	settings      *settingssvc.Service
	audit         *auditsvc.Service
	models        *modelcap.Registry
	users         *userservice.Service
	apiKeys       *apikeyservice.Service
	usage         *usageservice.Service
	processor     *analysisservice.Service
	autoKeys      []string
	generations   *memorygenservice.Service
}

type ConversationListItem = domain.ConversationListItem

type ConversationMemory struct {
	ConversationID string                  `json:"conversation_id"`
	Documents      []domain.MemoryDocument `json:"documents"`
	KeyMemories    []domain.KeyMemory      `json:"key_memories"`
}

type MemoryPreview struct {
	ConversationID string           `json:"conversation_id,omitempty"`
	SummaryStatus  string           `json:"summary_status"`
	AnalysisStatus string           `json:"analysis_status"`
	Keywords       []string         `json:"keywords,omitempty"`
	Summary        map[string]any   `json:"summary,omitempty"`
	Fact           map[string]any   `json:"fact,omitempty"`
	Facts          []map[string]any `json:"facts,omitempty"`
	Impression     map[string]any   `json:"impression,omitempty"`
	GenericItems   []map[string]any `json:"generic_items,omitempty"`
	KeyMemories    []map[string]any `json:"key_memories,omitempty"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
}

type UserSummary struct {
	UserID            string     `json:"user_id"`
	Name              string     `json:"name"`
	IsAdmin           bool       `json:"is_admin"`
	APIKeyCount       int        `json:"api_key_count"`
	ConversationCount int        `json:"conversation_count"`
	KeyMemoryCount    int        `json:"key_memory_count"`
	TotalTokens       int        `json:"total_tokens"`
	RecentActiveAt    *time.Time `json:"recent_active_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

type UserDetail struct {
	Agents        []domain.Agent         `json:"agents,omitempty"`
	User          userservice.Record     `json:"user"`
	Settings      map[string]any         `json:"settings"`
	APIKeys       []apikeyservice.Record `json:"api_keys"`
	Conversations []ConversationListItem `json:"conversations"`
	KeyMemories   []domain.KeyMemory     `json:"key_memories"`
	TokenUsage    map[string]int         `json:"token_usage"`
	Stats         map[string]any         `json:"stats"`
}

func NewService(conversations ports.ConversationRepository, media ports.MediaRepository, memories ports.MemoryRepository, settings *settingssvc.Service, audit *auditsvc.Service, models *modelcap.Registry) *Service {
	return &Service{conversations: conversations, media: media, memories: memories, settings: settings, audit: audit, models: models}
}

func (s *Service) AttachUserRuntime(users *userservice.Service, apiKeys *apikeyservice.Service, usage *usageservice.Service) {
	s.users = users
	s.apiKeys = apiKeys
	s.usage = usage
}

func (s *Service) SetGenerationService(service *memorygenservice.Service) {
	s.generations = service
}

func (s *Service) Overview(ctx context.Context, userID string) (map[string]any, error) {
	conversations, err := s.conversations.ListConversations(ctx, userID)
	if err != nil {
		return nil, err
	}
	keyMemoryCount, err := countKeyMemories(ctx, s.memories, userID)
	if err != nil {
		return nil, err
	}
	activeConversations := 0
	for _, item := range conversations {
		if item.FinalizeStatus == "active" || item.FinalizeStatus == "" {
			activeConversations++
		}
	}
	recentAuditEvents := 0
	if s.audit != nil {
		recentAuditEvents = len(s.audit.List(ctx, 10))
	}
	config := map[string]any{}
	if s.settings != nil {
		config, err = s.settings.SnapshotForUser(ctx, userID)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"active_conversations": activeConversations,
		"conversation_count":   len(conversations),
		"memory_count":         countDocumentsByType(ctx, s.memories, s.generations, userID, nil),
		"key_memory_count":     keyMemoryCount,
		"models":               s.models.List(),
		"config":               config,
		"recent_audit_events":  recentAuditEvents,
	}, nil
}

func (s *Service) ListConversations(ctx context.Context, userID string, limit int) ([]ConversationListItem, error) {
	records, err := s.conversations.ListConversations(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := make([]ConversationListItem, 0, len(records))
	for _, record := range records {
		detail, err := s.conversations.GetConversation(ctx, userID, record.ConversationID)
		if err != nil {
			detail = record
		}
		items = append(items, ConversationListItem{
			ConversationID: detail.ConversationID,
			UserID:         detail.UserID,
			Provider:       string(detail.Provider),
			Protocol:       detail.RequestFormat,
			Mode:           string(detail.Mode),
			FinalizeStatus: string(detail.FinalizeStatus),
			SummaryStatus:  string(detail.SummaryStatus),
			AnalysisStatus: string(detail.AnalysisStatus),
			MessageCount:   len(detail.Messages),
			TotalTokens:    detail.Usage.TotalTokens,
			UpdatedAt:      detail.UpdatedAt,
			Metadata:       detail.Metadata,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Service) ConversationDetail(ctx context.Context, userID, conversationID, generationID string) (domain.ConversationRecord, error) {
	record, err := s.conversations.GetConversationInGeneration(ctx, userID, conversationID, generationID)
	if err != nil {
		return domain.ConversationRecord{}, err
	}
	if s.media != nil {
		if parts, partsErr := s.media.ListMessageParts(ctx, userID, conversationID); partsErr == nil {
			record.Parts = parts
		}
		if artifacts, artifactsErr := s.media.ListPartArtifacts(ctx, userID, conversationID); artifactsErr == nil {
			record.Artifacts = artifacts
		}
	}
	return record, nil
}

func (s *Service) ConversationMemory(ctx context.Context, userID, conversationID string) (ConversationMemory, error) {
	generationID, err := s.activeGenerationID(ctx, userID)
	if err != nil {
		return ConversationMemory{}, err
	}
	documents, err := s.memories.ListDocumentsByConversationInGeneration(ctx, userID, generationID, conversationID)
	if err != nil {
		return ConversationMemory{}, err
	}
	keyMemories, err := s.memories.ListKeyMemories(ctx, userID, 200)
	if err != nil {
		return ConversationMemory{}, err
	}
	filteredKeyMemories := make([]domain.KeyMemory, 0)
	for _, item := range keyMemories {
		if item.Value == conversationID || anyString(item.Metadata["conversation_id"]) == conversationID {
			filteredKeyMemories = append(filteredKeyMemories, item)
		}
	}
	return ConversationMemory{
		ConversationID: conversationID,
		Documents:      documents,
		KeyMemories:    filteredKeyMemories,
	}, nil
}

func (s *Service) SetMemoryProcessor(processor *analysisservice.Service, autoKeys []string) {
	s.processor = processor
	s.autoKeys = append([]string{}, autoKeys...)
}

func (s *Service) PreviewMemory(ctx context.Context, userID, conversationID string, messages []domain.UnifiedMessage) (MemoryPreview, error) {
	if s.processor == nil {
		return MemoryPreview{}, fmt.Errorf("memory LLM is not configured")
	}
	if conversationID == "" {
		conversationID = "preview"
	}
	now := time.Now().UTC()
	input := analysisservice.Input{UserID: userID, ConversationID: conversationID, Conversation: domain.ConversationRecord{UserID: userID, ConversationID: conversationID, Messages: messages}}
	generated, err := s.processor.SummarizeConversation(ctx, input)
	if err != nil {
		return MemoryPreview{}, err
	}
	input.Summary = domain.MemoryDocument{ID: "preview:summary:" + conversationID, NodeID: "preview:node:summary:" + conversationID, UserID: userID, ConversationID: conversationID, Type: domain.MemoryTypeSummary, Content: generated.Summary}
	input.ExistingFacts, err = s.memories.ListDocuments(ctx, userID, []domain.MemoryType{domain.MemoryTypeFact}, 64)
	if err != nil {
		return MemoryPreview{}, err
	}
	result, err := s.processor.AnalyzeConversation(ctx, input)
	if err != nil {
		return MemoryPreview{}, err
	}
	current := []domain.KeyMemory{}
	for _, key := range s.autoKeys {
		item, found, err := s.memories.GetKeyMemory(ctx, userID, key)
		if err != nil {
			return MemoryPreview{}, err
		}
		if found {
			current = append(current, item)
		}
	}
	candidates, err := s.processor.ReconcileKeyMemories(ctx, input, result.Facts, current, s.autoKeys)
	if err != nil {
		return MemoryPreview{}, err
	}
	preview := MemoryPreview{
		ConversationID: conversationID, SummaryStatus: "preview", AnalysisStatus: "preview", Keywords: generated.Keywords,
		Summary: map[string]any{"id": input.Summary.ID, "type": "summary", "content": generated.Summary, "topic_label": generated.TopicLabel, "importance": generated.Importance, "decay_factor": generated.DecayFactor, "activity_at": now, "time_tier": domain.ClassifyTimeTier(now, now), "extraction_method": "llm"},
		Facts:   []map[string]any{}, GenericItems: []map[string]any{}, KeyMemories: []map[string]any{}, Metadata: map[string]any{"message_count": len(messages), "extraction_method": "llm"},
	}
	for _, fact := range result.Facts {
		preview.Facts = append(preview.Facts, previewFactMap(fact, now))
	}
	if result.Fact.ID != "" {
		preview.Fact = previewFactMap(result.Fact, now)
	}
	if result.Impression.ID != "" {
		doc := result.Impression
		preview.Impression = map[string]any{"id": doc.ID, "type": doc.Type, "content": doc.Content, "metadata": doc.Metadata, "importance": doc.Importance, "decay_factor": doc.DecayFactor, "activity_at": now, "time_tier": domain.ClassifyTimeTier(now, now)}
	}
	for _, item := range result.GenericItems {
		preview.GenericItems = append(preview.GenericItems, previewGenericMemoryItemMap(item, now))
	}
	for _, candidate := range candidates {
		preview.KeyMemories = append(preview.KeyMemories, map[string]any{"key": candidate.Key, "value": candidate.Value, "category": candidate.Category, "source": "llm", "status": "candidate", "importance": candidate.Importance, "decay_factor": candidate.DecayFactor, "source_document_ids": candidate.SourceDocumentIDs})
	}
	return preview, nil
}

func previewGenericMemoryItemMap(item domain.MemoryDocument, now time.Time) map[string]any {
	return map[string]any{
		"id":              item.ID,
		"node_id":         item.NodeID,
		"type":            item.Type,
		"content":         item.Content,
		"metadata":        item.Metadata,
		"importance":      item.Importance,
		"decay_factor":    item.DecayFactor,
		"activity_at":     now,
		"time_tier":       domain.ClassifyTimeTier(now, now),
		"effective_score": item.Importance * domain.TimeDecay(item.Type, now, item.DecayFactor, item.Importance, now),
	}
}

func previewFactMap(fact domain.MemoryDocument, now time.Time) map[string]any {
	return map[string]any{
		"id":              fact.ID,
		"type":            fact.Type,
		"content":         fact.Content,
		"metadata":        fact.Metadata,
		"importance":      fact.Importance,
		"decay_factor":    fact.DecayFactor,
		"activity_at":     now,
		"time_tier":       domain.ClassifyTimeTier(now, now),
		"effective_score": fact.Importance * domain.TimeDecay(domain.MemoryTypeFact, now, fact.DecayFactor, fact.Importance, now),
	}
}

func (s *Service) ListUsers(ctx context.Context) ([]UserSummary, error) {
	if s.users == nil {
		return nil, errors.New("user runtime is not configured")
	}

	users, err := s.users.List(ctx)
	if err != nil {
		return nil, err
	}

	keysByUser := map[string]int{}
	if s.apiKeys != nil {
		apiKeys, err := s.apiKeys.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, apiKey := range apiKeys {
			if apiKey.RevokedAt == nil && (apiKey.ExpiresAt == nil || apiKey.ExpiresAt.After(time.Now())) {
				keysByUser[apiKey.UserID]++
			}
		}
	}

	summaries := make([]UserSummary, 0, len(users))
	for _, user := range users {
		conversations, err := s.ListConversations(ctx, user.ID, 0)
		if err != nil {
			return nil, err
		}
		keyMemories, err := countKeyMemories(ctx, s.memories, user.ID)
		if err != nil {
			return nil, err
		}
		totals, err := s.totalsForUser(ctx, user.ID, conversations)
		if err != nil {
			return nil, err
		}

		var recentActiveAt *time.Time
		if len(conversations) > 0 {
			ts := conversations[0].UpdatedAt
			recentActiveAt = &ts
		}

		summaries = append(summaries, UserSummary{
			UserID:            user.ID,
			Name:              user.Name,
			IsAdmin:           user.IsAdmin,
			APIKeyCount:       keysByUser[user.ID],
			ConversationCount: len(conversations),
			KeyMemoryCount:    keyMemories,
			TotalTokens:       totals["total_tokens"],
			RecentActiveAt:    recentActiveAt,
			CreatedAt:         user.CreatedAt,
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		left := summaries[i].RecentActiveAt
		right := summaries[j].RecentActiveAt
		switch {
		case left == nil && right == nil:
			return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
		case left == nil:
			return false
		case right == nil:
			return true
		default:
			return left.After(*right)
		}
	})

	return summaries, nil
}

func (s *Service) UserDetail(ctx context.Context, userID string) (UserDetail, error) {
	if s.users == nil {
		return UserDetail{}, errors.New("user runtime is not configured")
	}

	user, err := s.users.Get(ctx, userID)
	if err != nil {
		return UserDetail{}, err
	}

	settings := map[string]any{}
	if s.settings != nil {
		settings, err = s.settings.SnapshotForUser(ctx, userID)
		if err != nil {
			return UserDetail{}, err
		}
	}

	agents := []domain.Agent{}
	apiKeys := []apikeyservice.Record{}
	if s.apiKeys != nil {
		agentItems, err := s.apiKeys.Identities().List(ctx, userID, "", 100)
		if err != nil {
			return UserDetail{}, err
		}
		agents = agentItems
		records, err := s.apiKeys.List(ctx)
		if err != nil {
			return UserDetail{}, err
		}
		for _, record := range records {
			if record.UserID == userID {
				apiKeys = append(apiKeys, record)
			}
		}
		sort.Slice(apiKeys, func(i, j int) bool {
			return apiKeys[i].CreatedAt.After(apiKeys[j].CreatedAt)
		})
	}

	conversationCount := 0
	if s.conversations != nil {
		records, err := s.conversations.ListConversations(ctx, userID)
		if err != nil {
			return UserDetail{}, err
		}
		conversationCount = len(records)
	}

	conversations, err := s.ListConversations(ctx, userID, 12)
	if err != nil {
		return UserDetail{}, err
	}

	keyMemories := []domain.KeyMemory{}
	keyMemoryCount := 0
	if s.memories != nil {
		keyMemoryCount, err = countKeyMemories(ctx, s.memories, userID)
		if err != nil {
			return UserDetail{}, err
		}
		keyMemories, err = s.memories.ListKeyMemories(ctx, userID, 20)
		if err != nil {
			return UserDetail{}, err
		}
	}

	tokenUsage, err := s.totalsForUser(ctx, userID, conversations)
	if err != nil {
		return UserDetail{}, err
	}

	return UserDetail{
		Agents:        agents,
		User:          user,
		Settings:      settings,
		APIKeys:       apiKeys,
		Conversations: conversations,
		KeyMemories:   keyMemories,
		TokenUsage:    tokenUsage,
		Stats: map[string]any{
			"conversation_count": lenOrFallback(conversations, conversationCount),
			"key_memory_count":   keyMemoryCount,
			"api_key_count":      len(apiKeys),
		},
	}, nil
}

func countDocumentsByType(ctx context.Context, repo ports.MemoryRepository, generations *memorygenservice.Service, userID string, kinds []domain.MemoryType) int {
	if repo == nil {
		return 0
	}
	generationID := ""
	if generations != nil && userID != "" {
		generation, err := generations.GetActiveGeneration(ctx, userID)
		if err != nil {
			return 0
		}
		generationID = generation.GenerationID
	}
	if generationID == "" && userID != "" {
		generationID = memorygenservice.CurrentGenerationID(security.WorkspaceID(ctx, userID))
	}
	items, err := repo.ListDocumentsInGeneration(ctx, userID, generationID, kinds, 1000)
	if err != nil {
		return 0
	}
	return len(items)
}

func anyString(value any) string {
	typed, _ := value.(string)
	return typed
}

func countKeyMemories(ctx context.Context, repo ports.MemoryRepository, userID string) (int, error) {
	if repo == nil {
		return 0, nil
	}
	return repo.CountKeyMemories(ctx, userID)
}

func (s *Service) activeGenerationID(ctx context.Context, userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", nil
	}
	if s.generations == nil {
		return memorygenservice.CurrentGenerationID(security.WorkspaceID(ctx, userID)), nil
	}
	generation, err := s.generations.GetActiveGeneration(ctx, userID)
	if err != nil {
		return "", err
	}
	return generation.GenerationID, nil
}

func (s *Service) totalsForUser(ctx context.Context, userID string, conversations []ConversationListItem) (map[string]int, error) {
	if s.usage != nil {
		return s.usage.Totals(ctx, userID)
	}
	return totalsFromConversations(conversations), nil
}

func totalsFromConversations(conversations []ConversationListItem) map[string]int {
	totalTokens := 0
	for _, item := range conversations {
		totalTokens += item.TotalTokens
	}
	return map[string]int{
		"input_tokens":  0,
		"output_tokens": 0,
		"total_tokens":  totalTokens,
	}
}

func lenOrFallback(items []ConversationListItem, fallback int) int {
	if fallback > len(items) {
		return fallback
	}
	return len(items)
}
