package resolver

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/graph/generated"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func userIDFromContext(ctx context.Context) string {
	if userID := security.UserIDFromContext(ctx); userID != "" {
		return userID
	}
	return "admin"
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valueOrDefault(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func graphUserSetting(snapshot map[string]any) *generated.UserSetting {
	return &generated.UserSetting{
		ChatModel:          nullablePtr(anyString(snapshot["chat_model"])),
		SummaryModel:       nullablePtr(anyString(snapshot["summary_model"])),
		AnalysisModel:      nullablePtr(anyString(snapshot["analysis_model"])),
		EmbeddingModel:     nullablePtr(anyString(snapshot["embedding_model"])),
		ToolWriteEnabled:   anyBool(snapshot["tool_write_enabled"]),
		MaxInjectionTokens: intValue(snapshot["max_injection_tokens"]),
	}
}

func graphKeyMemory(km domain.KeyMemory) *generated.KeyMemory {
	return &generated.KeyMemory{ID: km.ID, Key: km.Key, Value: km.Value, Category: nullablePtr(km.Category)}
}

func graphSummary(doc domain.MemoryDocument) *generated.MemorySummary {
	var label string
	if doc.Metadata != nil {
		label = anyString(doc.Metadata["topic_label"])
	}
	return &generated.MemorySummary{ID: doc.ID, Summary: doc.Content, TopicLabel: nullablePtr(label)}
}

func graphImpression(doc domain.MemoryDocument) *generated.Impression {
	return &generated.Impression{ID: doc.ID, Description: doc.Content}
}

func graphFact(doc domain.MemoryDocument) *generated.Fact {
	return &generated.Fact{ID: doc.ID, Content: doc.Content}
}

func nullablePtr(value string) *string {
	if value == "" || value == "<nil>" || value == "%!v(<nil>)" {
		return nil
	}
	return &value
}

func anyString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func anyBool(value any) bool {
	typed, _ := value.(bool)
	return typed
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case int32:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func (r *Resolver) recordAudit(ctx context.Context, action, targetType, targetID string, payload map[string]any) {
	if r.AuditService == nil {
		return
	}
	r.AuditService.Record(ctx, domain.AuditEvent{
		ID:         uuid.NewString(),
		ActorType:  actorType(ctx),
		ActorID:    userIDFromContext(ctx),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Payload:    payload,
		CreatedAt:  time.Now().UTC(),
	})
}

func actorType(ctx context.Context) string {
	if security.AdminFromContext(ctx) {
		return "admin"
	}
	return "user"
}
