package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

type LOCOMOOptions struct {
	SkipCategoryFive bool `json:"skip_category_five"`
}

type locomoSample struct {
	SampleID       string           `json:"sample_id"`
	Conversation   any              `json:"conversation"`
	EventSummary   any              `json:"event_summary"`
	Observation    any              `json:"observation"`
	SessionSummary any              `json:"session_summary"`
	QA             []map[string]any `json:"qa"`
}

type locomoDoc struct {
	Ref      string
	Document DatasetDocument
}

func LoadLOCOMODataset(path string, options LOCOMOOptions) (Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Dataset{}, err
	}
	var samples []locomoSample
	if err := json.Unmarshal(raw, &samples); err != nil {
		return Dataset{}, err
	}
	dataset, err := buildLOCOMODataset(samples, options)
	if err != nil {
		return Dataset{}, err
	}
	if err := dataset.Validate(); err != nil {
		return Dataset{}, err
	}
	return dataset, nil
}

func buildLOCOMODataset(samples []locomoSample, options LOCOMOOptions) (Dataset, error) {
	dataset := Dataset{
		Name:        "locomo-external",
		Description: "External LOCOMO-shaped retrieval benchmark adapted for MemStation retrieval/ranking evaluation.",
		Documents:   make([]DatasetDocument, 0),
		Queries:     make([]QueryCase, 0),
	}
	docByRef := map[string]DatasetDocument{}
	for _, sample := range samples {
		docs := collectLOCOMODocuments(sample)
		for _, item := range docs {
			if item.Ref == "" || strings.TrimSpace(item.Document.Content) == "" {
				continue
			}
			if _, exists := docByRef[item.Ref]; exists {
				continue
			}
			docByRef[item.Ref] = item.Document
		}
		queries, err := buildLOCOMOQueries(sample, docByRef, options)
		if err != nil {
			return Dataset{}, err
		}
		dataset.Queries = append(dataset.Queries, queries...)
	}
	for _, item := range docByRef {
		dataset.Documents = append(dataset.Documents, item)
	}
	sort.Slice(dataset.Documents, func(i, j int) bool {
		return dataset.Documents[i].ID < dataset.Documents[j].ID
	})
	sort.Slice(dataset.Queries, func(i, j int) bool {
		return dataset.Queries[i].CaseID < dataset.Queries[j].CaseID
	})
	return dataset, nil
}

func collectLOCOMODocuments(sample locomoSample) []locomoDoc {
	docs := make([]locomoDoc, 0)
	docs = append(docs, flattenLOCOMOSection(sample.SampleID, conversationRefPrefix(sample.Conversation), "conversation", sample.Conversation, domain.MemoryTypeFact)...)
	docs = append(docs, flattenLOCOMOSection(sample.SampleID, "event", "event_summary", sample.EventSummary, domain.MemoryTypeSummary)...)
	docs = append(docs, flattenLOCOMOSection(sample.SampleID, "observation", "observation", sample.Observation, domain.MemoryTypeFact)...)
	docs = append(docs, flattenLOCOMOSection(sample.SampleID, "session", "session_summary", sample.SessionSummary, domain.MemoryTypeSummary)...)
	return docs
}

func buildLOCOMOQueries(sample locomoSample, docByRef map[string]DatasetDocument, options LOCOMOOptions) ([]QueryCase, error) {
	queries := make([]QueryCase, 0, len(sample.QA))
	for idx, item := range sample.QA {
		question := firstNonEmptyString(item, "question", "query", "prompt")
		if question == "" {
			continue
		}
		category := intValue(item["category"])
		if options.SkipCategoryFive && category == 5 {
			continue
		}
		evidenceRefs := collectEvidenceRefs(item)
		positives := make([]string, 0, len(evidenceRefs))
		graded := map[string]float64{}
		userID := ""
		for _, ref := range evidenceRefs {
			doc, ok := docByRef[sample.SampleID+"::"+ref]
			if !ok {
				continue
			}
			positives = append(positives, doc.ID)
			graded[doc.ID] = 1
			if userID == "" {
				userID = doc.UserID
			}
		}
		if len(positives) == 0 {
			continue
		}
		if userID == "" {
			userID = locomoUserID(sample.SampleID, firstNonEmptyString(item, "speaker", "role", "name"))
		}
		queries = append(queries, QueryCase{
			CaseID:          fmt.Sprintf("locomo.%s.%02d", sample.SampleID, idx+1),
			UserID:          userID,
			Query:           question,
			TopN:            10,
			Positives:       uniqueStrings(positives),
			GradedRelevance: graded,
			Metadata: map[string]any{
				"sample_id": sample.SampleID,
				"category":  category,
				"answer":    firstNonEmptyString(item, "answer", "target_answer"),
			},
			Notes: fmt.Sprintf("LOCOMO sample %s category %d", sample.SampleID, category),
		})
	}
	return queries, nil
}

func flattenLOCOMOSection(sampleID, fallbackRef, section string, value any, kind domain.MemoryType) []locomoDoc {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return []locomoDoc{buildLOCOMODoc(sampleID, fallbackRef, section, "", typed, kind)}
	case []any:
		out := make([]locomoDoc, 0)
		for idx, item := range typed {
			ref := fmt.Sprintf("%s:%d", fallbackRef, idx+1)
			out = append(out, flattenLOCOMOSection(sampleID, ref, section, item, kind)...)
		}
		return out
	case map[string]any:
		text := firstNonEmptyString(typed, "text", "content", "summary", "description", "observation", "message", "utterance")
		if text != "" {
			ref := firstNonEmptyString(typed, "id", "ref", "reference", "doc_id", "turn_id", "evidence_id", "dia_id")
			if ref == "" {
				ref = fallbackRef
			}
			speaker := firstNonEmptyString(typed, "speaker", "role", "name", "persona")
			return []locomoDoc{buildLOCOMODoc(sampleID, ref, section, speaker, text, kind)}
		}
		out := make([]locomoDoc, 0)
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for idx, key := range keys {
			ref := fmt.Sprintf("%s:%d", fallbackRef, idx+1)
			out = append(out, flattenLOCOMOSection(sampleID, ref, section, typed[key], kind)...)
		}
		return out
	default:
		return nil
	}
}

func buildLOCOMODoc(sampleID, ref, section, speaker, content string, kind domain.MemoryType) locomoDoc {
	content = strings.TrimSpace(content)
	if content == "" {
		return locomoDoc{}
	}
	normalizedRef := strings.TrimSpace(ref)
	if normalizedRef == "" {
		normalizedRef = section
	}
	idPrefix := strings.NewReplacer(":", "-", "/", "-", " ", "-").Replace(normalizedRef)
	userID := locomoUserID(sampleID, speaker)
	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	return locomoDoc{
		Ref: sampleID + "::" + normalizedRef,
		Document: DatasetDocument{
			ID:          fmt.Sprintf("locomo-doc-%s-%s", sampleID, idPrefix),
			NodeID:      fmt.Sprintf("locomo-node-%s-%s", sampleID, idPrefix),
			UserID:      userID,
			Type:        kind,
			Content:     content,
			Importance:  1,
			DecayFactor: 1,
			Metadata: map[string]any{
				"ref":     normalizedRef,
				"section": section,
				"speaker": speaker,
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
}

func conversationRefPrefix(conversation any) string {
	switch typed := conversation.(type) {
	case []any:
		for _, item := range typed {
			mapped, ok := item.(map[string]any)
			if !ok {
				continue
			}
			prefix := firstNonEmptyString(mapped, "dialogue_id", "dialog_id", "session_id", "conversation_id", "dia_id")
			if prefix != "" {
				return prefix
			}
		}
	case map[string]any:
		for _, key := range []string{"session_1", "conversation_1", "dialogue_1"} {
			if value, ok := typed[key]; ok {
				if prefix := conversationRefPrefix(value); prefix != "" {
					return prefix
				}
			}
		}
	}
	return "D1"
}

func collectEvidenceRefs(item map[string]any) []string {
	values := make([]string, 0)
	for _, key := range []string{"evidence", "evidence_refs", "supporting_facts", "references"} {
		values = append(values, collectStringValues(item[key])...)
	}
	return uniqueStrings(values)
}

func collectStringValues(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		parts := strings.FieldsFunc(typed, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n'
		})
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	case []any:
		out := make([]string, 0)
		for _, item := range typed {
			out = append(out, collectStringValues(item)...)
		}
		return out
	case map[string]any:
		candidate := firstNonEmptyString(typed, "id", "ref", "reference")
		if candidate != "" {
			return []string{candidate}
		}
	}
	return nil
}

func firstNonEmptyString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case fmt.Stringer:
			text := strings.TrimSpace(typed.String())
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}

func locomoUserID(sampleID, speaker string) string {
	normalized := strings.TrimSpace(strings.ToLower(speaker))
	if normalized == "" {
		normalized = "primary"
	}
	normalized = strings.NewReplacer(" ", "-", "/", "-", ":", "-").Replace(normalized)
	return fmt.Sprintf("locomo-%s-%s", sampleID, normalized)
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
