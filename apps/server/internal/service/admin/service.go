package admin

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/ports"
	analysisservice "memstation/apps/server/internal/service/analysis"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	settingssvc "memstation/apps/server/internal/service/settings"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
)

type Service struct {
	conversations ports.ConversationRepository
	memories      ports.MemoryRepository
	settings      *settingssvc.Service
	audit         *auditsvc.Service
	models        *modelcap.Registry
	users         *userservice.Service
	apiKeys       *apikeyservice.Service
	usage         *usageservice.Service
}

type ConversationListItem struct {
	ConversationID string         `json:"conversation_id"`
	UserID         string         `json:"user_id"`
	Provider       string         `json:"provider"`
	Protocol       string         `json:"protocol"`
	Mode           string         `json:"mode"`
	FinalizeStatus string         `json:"finalize_status"`
	SummaryStatus  string         `json:"summary_status"`
	AnalysisStatus string         `json:"analysis_status"`
	MessageCount   int            `json:"message_count"`
	TotalTokens    int            `json:"total_tokens"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

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
	Impression     map[string]any   `json:"impression,omitempty"`
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
	User          userservice.Record     `json:"user"`
	Settings      map[string]any         `json:"settings"`
	APIKeys       []apikeyservice.Record `json:"api_keys"`
	Conversations []ConversationListItem `json:"conversations"`
	KeyMemories   []domain.KeyMemory     `json:"key_memories"`
	TokenUsage    map[string]int         `json:"token_usage"`
	Stats         map[string]any         `json:"stats"`
}

func NewService(conversations ports.ConversationRepository, memories ports.MemoryRepository, settings *settingssvc.Service, audit *auditsvc.Service, models *modelcap.Registry) *Service {
	return &Service{conversations: conversations, memories: memories, settings: settings, audit: audit, models: models}
}

func (s *Service) AttachUserRuntime(users *userservice.Service, apiKeys *apikeyservice.Service, usage *usageservice.Service) {
	s.users = users
	s.apiKeys = apiKeys
	s.usage = usage
}

func (s *Service) Overview(ctx context.Context, userID string) (map[string]any, error) {
	conversations, err := s.conversations.ListConversations(ctx, userID)
	if err != nil {
		return nil, err
	}
	keyMemories, err := s.memories.ListKeyMemories(ctx, userID, 50)
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
		"memory_count":         countDocumentsByType(ctx, s.memories, userID, nil),
		"key_memory_count":     len(keyMemories),
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

func (s *Service) ConversationDetail(ctx context.Context, userID, conversationID string) (domain.ConversationRecord, error) {
	return s.conversations.GetConversation(ctx, userID, conversationID)
}

func (s *Service) ConversationMemory(ctx context.Context, userID, conversationID string) (ConversationMemory, error) {
	documents, err := s.memories.ListDocumentsByConversation(ctx, userID, conversationID)
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

func (s *Service) PreviewMemory(ctx context.Context, userID, conversationID string, messages []domain.UnifiedMessage) (MemoryPreview, error) {
	summaryText := previewSummary(messages)
	keywords := previewKeywords(summaryText)
	now := time.Now().UTC()
	analysis := analysisservice.NewService()
	result, err := analysis.AnalyzeConversation(ctx, analysisservice.Input{
		UserID:         userID,
		ConversationID: conversationID,
		Conversation: domain.ConversationRecord{
			ConversationID: conversationID,
			UserID:         userID,
			Messages:       messages,
		},
		Summary: domain.MemoryDocument{
			ID:             "preview:summary:" + conversationID,
			NodeID:         "preview:node:summary:" + conversationID,
			UserID:         userID,
			ConversationID: conversationID,
			Type:           domain.MemoryTypeSummary,
			Content:        summaryText,
			Metadata: map[string]any{
				"topic_label": previewTopicLabel(keywords),
				"keywords":    keywords,
			},
		},
	})
	if err != nil {
		return MemoryPreview{}, err
	}

	keyMemoryCandidates := []map[string]any{}
	if strings.TrimSpace(conversationID) != "" {
		keyMemoryCandidates = append(keyMemoryCandidates, map[string]any{
			"key":             "recent_conversation",
			"value":           conversationID,
			"source":          "preview",
			"category":        "system",
			"status":          "candidate",
			"importance":      1.4,
			"decay_factor":    2.2,
			"activity_at":     now,
			"time_tier":       domain.ClassifyTimeTier(now, now),
			"effective_score": 1.4 * domain.TimeDecay(domain.MemoryTypeKeyMemory, now, 2.2, 1.4, now),
		})
	}

	return MemoryPreview{
		ConversationID: conversationID,
		SummaryStatus:  "preview",
		AnalysisStatus: "preview",
		Keywords:       keywords,
		Summary: map[string]any{
			"id":              "preview:summary:" + conversationID,
			"type":            "summary",
			"content":         summaryText,
			"topic_label":     previewTopicLabel(keywords),
			"importance":      1.0,
			"decay_factor":    1.0,
			"activity_at":     now,
			"time_tier":       domain.ClassifyTimeTier(now, now),
			"effective_score": 1.0,
		},
		Fact: map[string]any{
			"id":              result.Fact.ID,
			"type":            result.Fact.Type,
			"content":         result.Fact.Content,
			"metadata":        result.Fact.Metadata,
			"importance":      result.Fact.Importance,
			"decay_factor":    result.Fact.DecayFactor,
			"activity_at":     now,
			"time_tier":       domain.ClassifyTimeTier(now, now),
			"effective_score": result.Fact.Importance * domain.TimeDecay(domain.MemoryTypeFact, now, result.Fact.DecayFactor, result.Fact.Importance, now),
		},
		Impression: map[string]any{
			"id":              result.Impression.ID,
			"type":            result.Impression.Type,
			"content":         result.Impression.Content,
			"metadata":        result.Impression.Metadata,
			"importance":      result.Impression.Importance,
			"decay_factor":    result.Impression.DecayFactor,
			"activity_at":     now,
			"time_tier":       domain.ClassifyTimeTier(now, now),
			"effective_score": result.Impression.Importance * domain.TimeDecay(domain.MemoryTypeImpression, now, result.Impression.DecayFactor, result.Impression.Importance, now),
		},
		KeyMemories: keyMemoryCandidates,
		Metadata: map[string]any{
			"message_count": len(messages),
		},
	}, nil
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
			if apiKey.RevokedAt == nil {
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
		keyMemories := countKeyMemories(ctx, s.memories, user.ID)
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

	apiKeys := []apikeyservice.Record{}
	if s.apiKeys != nil {
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
	if s.memories != nil {
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
		User:          user,
		Settings:      settings,
		APIKeys:       apiKeys,
		Conversations: conversations,
		KeyMemories:   keyMemories,
		TokenUsage:    tokenUsage,
		Stats: map[string]any{
			"conversation_count": lenOrFallback(conversations, conversationCount),
			"key_memory_count":   len(keyMemories),
			"api_key_count":      len(apiKeys),
		},
	}, nil
}

func countDocumentsByType(ctx context.Context, repo ports.MemoryRepository, userID string, kinds []domain.MemoryType) int {
	if repo == nil {
		return 0
	}
	items, err := repo.ListDocuments(ctx, userID, kinds, 1000)
	if err != nil {
		return 0
	}
	return len(items)
}

func previewSummary(messages []domain.UnifiedMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Role != domain.RoleUser && message.Role != domain.RoleAssistant {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" && len(message.Blocks) > 0 {
			text = strings.TrimSpace(message.Blocks[0].Text)
		}
		if text == "" {
			continue
		}
		parts = append(parts, string(message.Role)+": "+text)
	}
	summary := strings.Join(parts, " | ")
	if len(summary) > 400 {
		return summary[:400] + "..."
	}
	if summary == "" {
		return "No summary content available."
	}
	return summary
}

func previewKeywords(summary string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 5)
	for _, part := range strings.Fields(strings.ToLower(summary)) {
		part = strings.Trim(part, ".,;:!?()[]{}\"'")
		if len(part) < 4 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
		if len(out) == 5 {
			break
		}
	}
	sort.Strings(out)
	return out
}

func previewTopicLabel(keywords []string) string {
	if len(keywords) == 0 {
		return "general"
	}
	if len(keywords) > 2 {
		keywords = keywords[:2]
	}
	return strings.Join(keywords, ", ")
}

func anyString(value any) string {
	typed, _ := value.(string)
	return typed
}

func countKeyMemories(ctx context.Context, repo ports.MemoryRepository, userID string) int {
	if repo == nil {
		return 0
	}
	items, err := repo.ListKeyMemories(ctx, userID, 1000)
	if err != nil {
		return 0
	}
	return len(items)
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
