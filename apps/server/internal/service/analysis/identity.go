package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"memstation/apps/server/internal/domain"
)

func factKeyFor(doc domain.MemoryDocument) string { return metadataString(doc.Metadata, "fact_key") }

func normalizeMemoryWritePolicy(policy MemoryWritePolicy) MemoryWritePolicy {
	if policy.MaxGenericItems < 0 {
		return MemoryWritePolicy{EnableGenericItems: false}
	}
	if !policy.EnableGenericItems && policy.MaxGenericItems == 0 {
		policy.EnableGenericItems = true
	}
	if policy.MaxGenericItems <= 0 {
		policy.MaxGenericItems = 12
	}
	return policy
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
	if len(doc.SubjectActorIDs) > 0 {
		actors := append([]string{}, doc.SubjectActorIDs...)
		slices.Sort(actors)
		identity, _ := json.Marshal([]any{doc.Metadata["lineage_revision_key"], actors})
		hash := sha256.Sum256(identity)
		doc.Metadata["lineage_revision_key"] = hex.EncodeToString(hash[:])
	}

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

func genericItemSeed(kind domain.MemoryType, conversationID, content string, metadata map[string]any) string {
	builder := strings.Builder{}
	builder.WriteString(string(kind))
	builder.WriteByte('|')
	builder.WriteString(conversationID)
	builder.WriteByte('|')
	builder.WriteString(metadataString(metadata, "source"))
	builder.WriteByte('|')
	builder.WriteString(metadataString(metadata, "tool_call_id"))
	builder.WriteByte('|')
	builder.WriteString(metadataString(metadata, "artifact_id"))
	builder.WriteByte('|')
	builder.WriteString(metadataString(metadata, "title"))
	builder.WriteByte('|')
	builder.WriteString(normalizeWhitespace(content))
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

func GenericMemoryDocumentID(userID string, kind domain.MemoryType, conversationID, seed string) string {
	return string(kind) + ":" + sanitizeFactKey(userID) + ":" + sanitizeFactKey(conversationID) + ":" + lineageSuffix(seed)
}

func GenericMemoryNodeID(userID string, kind domain.MemoryType, conversationID, seed string) string {
	return "node:" + string(kind) + ":" + sanitizeFactKey(userID) + ":" + sanitizeFactKey(conversationID) + ":" + lineageSuffix(seed)
}

func CanonicalEntityDocumentID(userID, canonicalKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(canonicalKey)))
	return "entity:" + sanitizeFactKey(userID) + ":" + lineageSuffix(hex.EncodeToString(sum[:]))
}

func CanonicalEntityNodeID(userID, canonicalKey string) string {
	return "node:" + CanonicalEntityDocumentID(userID, canonicalKey)
}

func canonicalEntityKey(content string, metadata map[string]any) string {
	kind := strings.ToLower(strings.TrimSpace(metadataString(metadata, "entity_kind")))
	if kind == "" {
		kind = "topic"
	}
	name := firstNonEmpty(metadataString(metadata, "title"), strings.TrimPrefix(content, "Entity: "), strings.TrimPrefix(content, "Tool entity: "))
	name = strings.ToLower(normalizeWhitespace(name))
	name = strings.Trim(name, " \t\r\n.,:;!?()[]{}\"'")
	scope := domain.NormalizeEntityAlias(metadataString(metadata, "entity_scope"))
	if scope != "" {
		return kind + ":" + scope + ":" + name
	}
	return kind + ":" + name
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

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
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
