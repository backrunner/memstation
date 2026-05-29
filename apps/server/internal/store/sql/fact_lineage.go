package sql

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

func factLineageGroupID(userID string, doc domain.MemoryDocument) string {
	if factKey := factKeyFromMetadata(doc.Metadata); factKey != "" {
		return canonicalFactLineageGroupID(userID, factKey)
	}
	if groupID := factLineageGroupIDFromMetadata(doc.Metadata); groupID != "" && factLineageGroupBelongsToUser(userID, groupID) {
		return groupID
	}
	return ""
}

func canonicalFactLineageGroupID(userID, factKey string) string {
	return fmt.Sprintf("fact-lineage:%s:%s", strings.TrimSpace(userID), sanitizeLineageKey(factKey))
}

func factLineageGroupBelongsToUser(userID, groupID string) bool {
	userID = strings.TrimSpace(userID)
	groupID = strings.TrimSpace(groupID)
	if userID == "" || groupID == "" {
		return false
	}
	return strings.HasPrefix(groupID, "fact-lineage:"+userID+":")
}

func factLineageGroupIDFromMetadata(metadata map[string]any) string {
	return strings.TrimSpace(stringMetadataValue(metadata, "lineage_group_id"))
}

func factKeyFromMetadata(metadata map[string]any) string {
	return strings.TrimSpace(stringMetadataValue(metadata, "fact_key"))
}

func lineageRevisionKeyFromMetadata(metadata map[string]any) string {
	return strings.TrimSpace(stringMetadataValue(metadata, "lineage_revision_key"))
}

func factLineageRevisionSeqFromMetadata(metadata map[string]any) int {
	if len(metadata) == 0 {
		return 0
	}
	switch typed := metadata["revision_seq"].(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		value, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return value
		}
	}
	return 0
}

func sanitizeLineageKey(value string) string {
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

func lineageRevisionKey(doc domain.MemoryDocument) string {
	if key := lineageRevisionKeyFromMetadata(doc.Metadata); key != "" {
		return key
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(doc.UserID),
		strings.TrimSpace(doc.ConversationID),
		strings.TrimSpace(factKeyFromMetadata(doc.Metadata)),
		strings.TrimSpace(doc.Content),
		strings.TrimSpace(doc.ID),
	}, "\x1f")))
	return hex.EncodeToString(sum[:])
}

func lineageRevisionID(doc domain.MemoryDocument) string {
	groupID := factLineageGroupID(doc.UserID, doc)
	return fmt.Sprintf("%s:%s", groupID, lineageRevisionKey(doc))
}

func ensureFactLineageMetadata(doc domain.MemoryDocument) domain.MemoryDocument {
	if doc.Metadata == nil {
		doc.Metadata = map[string]any{}
	}
	if factKey := factKeyFromMetadata(doc.Metadata); factKey != "" {
		doc.Metadata["fact_key"] = factKey
	}
	groupID := factLineageGroupID(doc.UserID, doc)
	if groupID != "" {
		doc.Metadata["lineage_group_id"] = groupID
	} else {
		delete(doc.Metadata, "lineage_group_id")
	}
	if key := lineageRevisionKeyFromMetadata(doc.Metadata); key != "" {
		doc.Metadata["lineage_revision_key"] = key
	} else if groupID != "" && doc.ID != "" {
		doc.Metadata["lineage_revision_key"] = sanitizeLineageKey(doc.ID)
	}
	if seq := factLineageRevisionSeqFromMetadata(doc.Metadata); seq > 0 {
		doc.Metadata["revision_seq"] = seq
	}
	return doc
}

func uniqueSortedStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func factLineageEntryFromDocument(doc domain.MemoryDocument) domain.FactLineageEntry {
	entry := domain.FactLineageEntry{
		FactID:         doc.ID,
		NodeID:         doc.NodeID,
		FactKey:        factKeyFromMetadata(doc.Metadata),
		LineageGroupID: factLineageGroupIDFromMetadata(doc.Metadata),
		RevisionSeq:    factLineageRevisionSeqFromMetadata(doc.Metadata),
		IsSuperseded:   boolValue(doc.Metadata, "is_superseded"),
		SupersededByID: strings.TrimSpace(stringValue(doc.Metadata, "superseded_by_id")),
		CreatedAt:      doc.CreatedAt,
		UpdatedAt:      doc.UpdatedAt,
	}
	if validFrom := metadataTimePtr(doc.Metadata, "valid_from"); validFrom != nil {
		entry.ValidFrom = validFrom
	}
	if validTo := metadataTimePtr(doc.Metadata, "valid_to"); validTo != nil {
		entry.ValidTo = validTo
	}
	return entry
}

func factLineageFromEntries(entries []domain.FactLineageEntry) domain.FactLineage {
	lineage := domain.FactLineage{
		Entries:       append([]domain.FactLineageEntry(nil), entries...),
		RevisionDepth: len(entries),
	}
	if len(entries) == 0 {
		return lineage
	}
	sort.Slice(lineage.Entries, func(i, j int) bool {
		if lineage.Entries[i].RevisionSeq == lineage.Entries[j].RevisionSeq {
			if lineage.Entries[i].CreatedAt.Equal(lineage.Entries[j].CreatedAt) {
				return lineage.Entries[i].FactID < lineage.Entries[j].FactID
			}
			return lineage.Entries[i].CreatedAt.Before(lineage.Entries[j].CreatedAt)
		}
		return lineage.Entries[i].RevisionSeq < lineage.Entries[j].RevisionSeq
	})
	head := lineage.Entries[len(lineage.Entries)-1]
	for idx := len(lineage.Entries) - 1; idx >= 0; idx-- {
		candidate := lineage.Entries[idx]
		if !candidate.IsSuperseded && candidate.SupersededByID == "" {
			head = candidate
			break
		}
	}
	lineage.CurrentHeadID = head.FactID
	lineage.FactKey = head.FactKey
	lineage.LineageGroupID = head.LineageGroupID
	lineage.LineagePath = make([]string, 0, len(lineage.Entries))
	for _, entry := range lineage.Entries {
		lineage.LineagePath = append(lineage.LineagePath, entry.FactID)
	}
	return lineage
}

func metadataTimePtr(metadata map[string]any, key string) *time.Time {
	if metadata == nil {
		return nil
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return nil
		}
		cloned := typed.UTC()
		return &cloned
	case *time.Time:
		if typed == nil || typed.IsZero() {
			return nil
		}
		cloned := typed.UTC()
		return &cloned
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			cloned := parsed.UTC()
			return &cloned
		}
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			cloned := parsed.UTC()
			return &cloned
		}
	}
	return nil
}
