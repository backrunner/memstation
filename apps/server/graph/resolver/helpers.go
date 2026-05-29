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
	return &generated.Fact{ID: doc.ID, Content: doc.Content, Lineage: graphFactLineage(doc)}
}

func graphFactLineage(doc domain.MemoryDocument) *generated.FactLineage {
	if doc.Lineage == nil {
		if doc.Metadata == nil {
			return nil
		}
		lineage := &domain.MemoryLineageMeta{
			FactKey:        anyString(doc.Metadata["fact_key"]),
			LineageGroupID: anyString(doc.Metadata["lineage_group_id"]),
			RevisionSeq:    intValue(doc.Metadata["revision_seq"]),
			GenerationID:   anyString(doc.Metadata["generation_id"]),
			CurrentHeadID: firstNonEmptyString(
				anyString(doc.Metadata["current_head_id"]),
				doc.ID,
			),
		}
		lineage.LineagePath = stringSliceValue(doc.Metadata["lineage_path"])
		lineage.SupersededAliasNodeIDs = stringSliceValue(doc.Metadata["superseded_alias_node_ids"])
		if depth := intValue(doc.Metadata["revision_depth"]); depth > 0 {
			lineage.RevisionDepth = depth
		}
		if lineage.FactKey == "" && lineage.LineageGroupID == "" && lineage.RevisionSeq == 0 && len(lineage.LineagePath) == 0 {
			return nil
		}
		doc.Lineage = lineage
	}
	if doc.Lineage == nil {
		return nil
	}
	return &generated.FactLineage{
		FactKey:                nullablePtr(doc.Lineage.FactKey),
		LineageGroupID:         nullablePtr(doc.Lineage.LineageGroupID),
		RevisionSeq:            doc.Lineage.RevisionSeq,
		GenerationID:           nullablePtr(doc.Lineage.GenerationID),
		LineagePath:            append([]string(nil), doc.Lineage.LineagePath...),
		SupersededAliasNodeIDs: append([]string(nil), doc.Lineage.SupersededAliasNodeIDs...),
		RevisionDepth:          doc.Lineage.RevisionDepth,
		CurrentHeadID:          doc.Lineage.CurrentHeadID,
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" && value != "<nil>" && value != "%!v(<nil>)" {
			return value
		}
	}
	return ""
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

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := anyString(item); text != "" && text != "<nil>" && text != "%!v(<nil>)" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
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
