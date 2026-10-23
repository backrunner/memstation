package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
)

// GroundDocument accepts only a verbatim source span. Semantic similarity or
// copying a summary's references cannot establish that a source supports a claim.
func GroundDocument(doc domain.MemoryDocument, conversation domain.ConversationRecord) domain.MemoryDocument {
	doc.Metadata = cloneMap(doc.Metadata)
	if doc.Metadata == nil {
		doc.Metadata = map[string]any{}
	}
	doc.Metadata["support_kind"] = "model_inference"
	doc.Metadata["source_quotes"] = []any{}
	if doc.Type == domain.MemoryTypeImpression {
		return doc
	}
	claim := strings.TrimSpace(doc.Content)
	if _, tail, ok := strings.Cut(claim, ": "); ok {
		claim = strings.TrimSpace(tail)
	}
	if utf8.RuneCountInString(claim) < 8 {
		return doc
	}
	parts := conversation.Parts
	if len(parts) == 0 {
		for i, m := range conversation.Messages {
			parts = append(parts, domain.MessagePart{SequenceNum: i + 1, Role: m.Role, TextContent: m.Text})
		}
	}
	quotes := []any{}
	partIDs := []string{}
	for _, p := range parts {
		if p.Role != domain.RoleUser && p.Role != domain.RoleTool {
			continue
		}
		start := strings.Index(p.TextContent, claim)
		if start < 0 {
			continue
		}
		kind := "user_statement"
		if p.Role == domain.RoleTool {
			kind = "tool_observation"
		}
		raw := fmt.Sprintf("%s|%s|%d|%s|%d|%d", doc.UserID, doc.ConversationID, p.SequenceNum, p.PartID, start, start+len(claim))
		sum := sha256.Sum256([]byte(raw))
		quotes = append(quotes, map[string]any{"part_id": p.PartID, "sequence_num": p.SequenceNum, "quote": claim, "start_byte": start, "end_byte": start + len(claim), "support_kind": kind, "source_id": hex.EncodeToString(sum[:])})
		if p.PartID != "" {
			partIDs = append(partIDs, p.PartID)
		}
		doc.Metadata["support_kind"] = kind
	}
	doc.Metadata["source_quotes"] = quotes
	doc.Metadata["source_part_ids"] = partIDs
	return doc
}
