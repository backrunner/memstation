package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/processing"
)

// LightweightDocument records a mention, never an inferred actor or a user fact.
// Slice at the supplied offset before shared validation, preserving repeated mentions.
func LightweightDocument(user, conversation string, source Source, span processing.Span, policy string) (domain.MemoryDocument, error) {
	runes := []rune(source.Text)
	if span.Start < 0 || span.End <= span.Start || span.End > len(runes) || span.Text != string(runes[span.Start:span.End]) || source.Role != domain.RoleUser && source.Role != domain.RoleTool || source.MediaSHA256 != "" {
		return domain.MemoryDocument{}, errors.New("invalid lightweight source span")
	}
	startByte := len(string(runes[:span.Start]))
	source.ByteOffset += startByte
	source.Text = string(runes[span.Start:])
	if source.Ref != nil {
		ref := *source.Ref
		start := 0
		if ref.Span != nil {
			start = ref.Span.Start
		}
		ref.Span = &domain.TextSpan{Start: start + span.Start, End: start + span.Start + utf8.RuneCountInString(source.Text)}
		source.Ref = &ref
	}
	doc, err := documentFromItem(Item{Content: span.Text, Subject: "source mention", SubjectScope: "workspace", SubjectActorIDs: []string{}, Importance: 1, DecayFactor: 1, Confidence: span.Confidence, Quotes: []Quote{{SourceID: source.ID, Text: span.Text}}}, domain.MemoryTypeEntity, user, conversation, []Source{source})
	if err != nil {
		return doc, err
	}
	identity, _ := json.Marshal([]string{user, conversation, source.ID, fmt.Sprint(source.ByteOffset), span.Text, span.Label, policy})
	doc.ID = domain.AccessID("mention", string(identity))
	doc.NodeID = "node:" + doc.ID
	doc.Metadata["extraction_method"] = "lightweight"
	doc.Metadata["source"] = "lightweight"
	doc.Metadata["score_reason"] = "neutral_source_policy"
	doc.Metadata["entity_kind"] = span.Label
	doc.Metadata["canonical_name"] = strings.TrimSpace(span.Text)
	doc.Metadata["processing_policy_id"] = policy
	doc.Metadata["interpretation"] = "source_mention"
	return doc, nil
}

type LightweightWindow struct {
	Source Source
	Text   string
	Start  int
}

func LightweightWindows(c domain.ConversationRecord) ([]LightweightWindow, []string, bool) {
	windows := []LightweightWindow{}
	texts := []string{}
	media := false
	for _, source := range Sources(c) {
		if source.MediaSHA256 != "" {
			media = true
			continue
		}
		if source.Role != domain.RoleUser && source.Role != domain.RoleTool {
			continue
		}
		runes := []rune(source.Text)
		for start := 0; start < len(runes); {
			end := min(start+512, len(runes))
			text := string(runes[start:end])
			windows = append(windows, LightweightWindow{Source: source, Text: text, Start: start})
			texts = append(texts, text)
			if end == len(runes) {
				break
			}
			start = end - 64
		}
	}
	return windows, texts, media
}
