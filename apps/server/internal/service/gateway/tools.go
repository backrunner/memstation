package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	auditsvc "memstation/apps/server/internal/service/audit"
	memoryservice "memstation/apps/server/internal/service/memory"
)

const defaultToolRounds = 3

type ToolRuntime struct {
	memory           *memoryservice.Service
	audit            *auditsvc.Service
	allowedWriteKeys map[string]struct{}
	maxRounds        int
}

func NewToolRuntime(memory *memoryservice.Service, audit *auditsvc.Service, allowedWriteKeys map[string]struct{}, maxRounds int) *ToolRuntime {
	copiedKeys := make(map[string]struct{}, len(allowedWriteKeys))
	for key := range allowedWriteKeys {
		copiedKeys[key] = struct{}{}
	}
	if maxRounds <= 0 {
		maxRounds = defaultToolRounds
	}
	return &ToolRuntime{
		memory:           memory,
		audit:            audit,
		allowedWriteKeys: copiedKeys,
		maxRounds:        maxRounds,
	}
}

func (r *ToolRuntime) MaxRounds() int {
	if r == nil || r.maxRounds <= 0 {
		return defaultToolRounds
	}
	return r.maxRounds
}

func (r *ToolRuntime) ShouldManageRequest(req domain.UnifiedRequest) bool {
	if r == nil || len(req.Tools) == 0 {
		return false
	}
	for _, tool := range req.Tools {
		if !r.CanHandleTool(tool.Name) {
			return false
		}
	}
	return true
}

func (r *ToolRuntime) CanHandle(toolCalls []domain.ToolCall) bool {
	if r == nil || len(toolCalls) == 0 {
		return false
	}
	for _, call := range toolCalls {
		if !r.CanHandleTool(call.Name) {
			return false
		}
	}
	return true
}

func (r *ToolRuntime) CanHandleTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "recall_memory", "get_key_memory", "write_key_memory":
		return true
	default:
		return false
	}
}

func (r *ToolRuntime) Execute(ctx context.Context, req domain.UnifiedRequest, assistant domain.UnifiedMessage) ([]domain.UnifiedMessage, error) {
	if r == nil || len(assistant.ToolCalls) == 0 {
		return nil, nil
	}
	results := make([]domain.UnifiedMessage, 0, len(assistant.ToolCalls))
	for _, call := range assistant.ToolCalls {
		output, err := r.executeCall(ctx, req, call)
		if err != nil {
			output = map[string]any{
				"ok":    false,
				"error": err.Error(),
			}
		}
		result := domain.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output:     output,
		}
		results = append(results, domain.UnifiedMessage{
			Role:        domain.RoleTool,
			Text:        stringifyToolOutput(output),
			Blocks:      []domain.ContentBlock{{Type: domain.ContentBlockJSON, Raw: stringifyToolOutput(output)}},
			ToolResults: []domain.ToolResult{result},
			Metadata: map[string]any{
				"tool_name": call.Name,
			},
		})
	}
	return results, nil
}

func (r *ToolRuntime) executeCall(ctx context.Context, req domain.UnifiedRequest, call domain.ToolCall) (map[string]any, error) {
	capability := domain.CapabilityMemoryRead
	if call.Name == "write_key_memory" {
		capability = domain.CapabilityMemoryEdit
	}
	if err := security.RequireCapability(ctx, capability); err != nil {
		return nil, err
	}
	arguments := objectValue(call.Arguments)
	switch call.Name {
	case "recall_memory":
		return r.recallMemory(ctx, req, arguments)
	case "get_key_memory":
		return r.getKeyMemory(ctx, req, arguments)
	case "write_key_memory":
		return r.writeKeyMemory(ctx, req, arguments)
	default:
		return nil, fmt.Errorf("unsupported tool: %s", call.Name)
	}
}

func (r *ToolRuntime) recallMemory(ctx context.Context, req domain.UnifiedRequest, arguments map[string]any) (map[string]any, error) {
	if r.memory == nil {
		return nil, fmt.Errorf("memory service unavailable")
	}
	query := strings.TrimSpace(stringArg(arguments, "query"))
	if query == "" {
		query = lastUserText(req.Messages)
	}
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	for _, key := range []string{"as_of", "known_at"} {
		if value, exists := arguments[key]; exists && value != nil {
			raw, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be an RFC3339 timestamp", key)
			}
			if strings.TrimSpace(raw) != "" {
				if _, err := time.Parse(time.RFC3339Nano, raw); err != nil {
					return nil, fmt.Errorf("%s must be an RFC3339 timestamp", key)
				}
			}
		}
	}
	topN := clampInt(intArg(arguments, "top_n", 5), 1, 10)
	types := memoryTypesArg(arguments["types"])
	dateFrom := timeArg(arguments, "date_from")
	dateTo := timeArg(arguments, "date_to")
	timeTiers := timeTiersArg(arguments["time_tiers"])
	result, err := r.memory.Search(ctx, domain.SearchInput{
		AsOf: timeArg(arguments, "as_of"), KnownAt: timeArg(arguments, "known_at"), RelationMode: stringArg(arguments, "relation_mode"), EntityScope: stringArg(arguments, "entity_scope"),
		UserID:      req.UserID,
		Query:       query,
		TopN:        topN,
		MemoryTypes: types,
		TimeTiers:   timeTiers,
		DateFrom:    dateFrom,
		DateTo:      dateTo,
		MinScore:    0,
	})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(result.Memories))
	for _, item := range result.Memories {
		items = append(items, map[string]any{
			"id":              item.ID,
			"node_id":         item.NodeID,
			"type":            item.Type,
			"content":         item.Content,
			"score":           item.Score,
			"effective_score": item.EffectiveScore,
			"metadata":        item.Metadata,
			"importance":      item.Importance,
			"decay_factor":    item.DecayFactor,
			"activity_at":     item.ActivityAt,
			"time_tier":       item.TimeTier,
		})
	}
	return map[string]any{
		"ok":                     true,
		"query":                  query,
		"items":                  items,
		"total":                  result.Total,
		"query_embedding_tokens": result.QueryEmbeddingTokens,
	}, nil
}

func (r *ToolRuntime) getKeyMemory(ctx context.Context, req domain.UnifiedRequest, arguments map[string]any) (map[string]any, error) {
	if r.memory == nil {
		return nil, fmt.Errorf("memory service unavailable")
	}
	key := strings.TrimSpace(stringArg(arguments, "key"))
	limit := clampInt(intArg(arguments, "limit", 10), 1, 20)
	query := strings.TrimSpace(stringArg(arguments, "query"))
	dateFrom := timeArg(arguments, "date_from")
	dateTo := timeArg(arguments, "date_to")
	timeTiers := timeTiersArg(arguments["time_tiers"])
	memories, err := r.memory.SearchKeyMemories(ctx, domain.KeyMemoryQuery{
		UserID:    req.UserID,
		Query:     query,
		TopK:      limit,
		DateFrom:  dateFrom,
		DateTo:    dateTo,
		TimeTiers: timeTiers,
	})
	if err != nil {
		return nil, err
	}

	items := make([]map[string]any, 0, len(memories))
	for _, item := range memories {
		if key != "" && item.Key != key {
			continue
		}
		items = append(items, map[string]any{
			"id":              item.ID,
			"key":             item.Key,
			"value":           item.Value,
			"category":        item.Category,
			"source":          item.Source,
			"review":          item.ReviewStatus,
			"importance":      item.Importance,
			"decay_factor":    item.DecayFactor,
			"activity_at":     item.ActivityAt,
			"time_tier":       item.TimeTier,
			"effective_score": item.EffectiveScore,
			"updated_at":      item.UpdatedAt,
		})
	}
	return map[string]any{
		"ok":    true,
		"items": items,
		"total": len(items),
	}, nil
}

func (r *ToolRuntime) writeKeyMemory(ctx context.Context, req domain.UnifiedRequest, arguments map[string]any) (map[string]any, error) {
	if r.memory == nil {
		return nil, fmt.Errorf("memory service unavailable")
	}
	if !req.PersistMemoryEnabled() {
		return nil, fmt.Errorf("memory persistence is disabled for this request")
	}
	if !req.ToolWriteEnabled {
		return nil, fmt.Errorf("tool writes are disabled")
	}
	key := strings.TrimSpace(stringArg(arguments, "key"))
	value := strings.TrimSpace(stringArg(arguments, "value"))
	category := strings.TrimSpace(stringArg(arguments, "category"))
	if key == "" || value == "" {
		return nil, fmt.Errorf("key and value are required")
	}
	if _, ok := r.allowedWriteKeys[key]; !ok {
		return nil, fmt.Errorf("key %q is not in the write whitelist", key)
	}

	record, err := r.memory.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID:   req.UserID,
		Key:      key,
		Value:    value,
		Category: category,
		Source:   "tool",
	})
	if err != nil {
		return nil, err
	}
	if r.audit != nil {
		r.audit.Record(ctx, domain.AuditEvent{
			ActorType:  "tool",
			ActorID:    "write_key_memory",
			Action:     "tool_write_key_memory",
			TargetType: "key_memory",
			TargetID:   record.ID,
			Payload: map[string]any{
				"key":      key,
				"user_id":  req.UserID,
				"category": category,
			},
			CreatedAt: time.Now().UTC(),
		})
	}
	return map[string]any{
		"ok": true,
		"item": map[string]any{
			"id":       record.ID,
			"key":      record.Key,
			"value":    record.Value,
			"category": record.Category,
			"source":   record.Source,
		},
	}, nil
}

func lastUserText(messages []domain.UnifiedMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != domain.RoleUser {
			continue
		}
		if text := strings.TrimSpace(messages[i].Text); text != "" {
			return text
		}
		for _, block := range messages[i].Blocks {
			if text := strings.TrimSpace(block.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

func stringifyToolOutput(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `{"ok":false,"error":"tool output serialization failed"}`
	}
	return string(raw)
}

func objectValue(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func stringArg(arguments map[string]any, key string) string {
	if len(arguments) == 0 {
		return ""
	}
	if value, ok := arguments[key]; ok {
		switch typed := value.(type) {
		case string:
			return typed
		default:
			return fmt.Sprintf("%v", typed)
		}
	}
	return ""
}

func intArg(arguments map[string]any, key string, fallback int) int {
	if len(arguments) == 0 {
		return fallback
	}
	value, ok := arguments[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func timeArg(arguments map[string]any, key string) *time.Time {
	value := strings.TrimSpace(stringArg(arguments, key))
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return &parsed
}

func timeTiersArg(value any) []domain.TimeTier {
	raw, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			out := make([]domain.TimeTier, 0, len(typed))
			for _, item := range typed {
				if tier := domain.ParseTimeTier(item); tier != "" {
					out = append(out, tier)
				}
			}
			return out
		}
		return nil
	}
	out := make([]domain.TimeTier, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			continue
		}
		if tier := domain.ParseTimeTier(text); tier != "" {
			out = append(out, tier)
		}
	}
	return out
}

func memoryTypesArg(value any) []domain.MemoryType {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]domain.MemoryType, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", item)))
		kind := domain.MemoryType(text)
		if domain.IsSupportedMemoryType(kind) {
			out = append(out, kind)
		}
	}
	return out
}
