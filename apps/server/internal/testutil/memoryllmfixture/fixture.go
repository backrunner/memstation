// Package memoryllmfixture supplies an explicit simulated model for tests and
// the offline recovery drill. Application assembly must never install it.
package memoryllmfixture

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"memstation/apps/server/internal/service/memoryllm"
)

type Generator struct {
	SummaryText      string
	GenericTypes     []string
	ContentByType    map[string]string
	PreferLastSource bool
}

func (g Generator) Generate(ctx context.Context, request memoryllm.Request) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(request.Input)
	var input struct {
		Sources []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"sources"`
		Chunks []struct {
			Summary string `json:"summary"`
		} `json:"chunk_summaries"`
		Facts []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"facts"`
		Allowed []string `json:"allowed_keys"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch request.Task {
	case "summary.generate", "summary.merge":
		texts := []string{}
		for _, source := range input.Sources {
			texts = append(texts, source.Text)
		}
		for _, chunk := range input.Chunks {
			texts = append(texts, chunk.Summary)
		}
		summary := strings.Join(texts, " | ")
		if g.SummaryText != "" {
			summary = g.SummaryText
		}
		if len(summary) > 20000 {
			summary = string([]rune(summary)[:min(2000, len([]rune(summary)))])
		}
		return json.Marshal(map[string]any{"summary": summary, "topic_label": "Fixture topic", "keywords": []string{"fixture"}, "importance": 1, "decay_factor": 1})
	case "analysis.extract":
		sources := append([]struct {
			ID   string `json:"id"`
			Role string `json:"role"`
			Text string `json:"text"`
		}{}, input.Sources...)
		if g.PreferLastSource {
			for i, j := 0, len(sources)-1; i < j; i, j = i+1, j-1 {
				sources[i], sources[j] = sources[j], sources[i]
			}
		}
		for _, source := range sources {
			if source.Role != "user" && source.Role != "tool" {
				continue
			}
			item := map[string]any{"content": source.Text, "subject": "user", "importance": 1, "decay_factor": 1, "confidence": .9, "quotes": []any{map[string]any{"source_id": source.ID, "text": source.Text}}}
			fact := map[string]any{}
			for k, v := range item {
				fact[k] = v
			}
			fact["key"] = "test_fact"
			fact["category"] = "test"
			fact["supersedes"] = []string{}
			task := map[string]any{}
			for k, v := range item {
				task[k] = v
			}
			task["key"] = "test_task"
			task["type"] = "task"
			task["title"] = "Fixture task"
			task["status"] = "pending"
			impression := map[string]any{"content": "Simulated model observation for worker contract tests.", "subject": "conversation", "importance": 1, "decay_factor": 1, "confidence": .8, "quotes": item["quotes"]}
			types := g.GenericTypes
			if types == nil {
				types = []string{"task"}
			}
			items := []any{}
			relations := []any{}
			for _, kind := range types {
				value := map[string]any{}
				for k, v := range task {
					value[k] = v
				}
				value["type"] = kind
				if kind == "artifact" && g.PreferLastSource {
					value["artifact_id"] = source.ID
				}
				value["key"] = "test_" + kind
				if text := g.ContentByType[kind]; text != "" {
					value["content"] = text
				}
				if kind == "entity" {
					value["entity_kind"] = "tool"
					value["canonical_name"] = "inspect_assets"
					value["title"] = "inspect_assets"
				}
				items = append(items, value)
			}
			if len(g.GenericTypes) > 1 {
				relations = append(relations, map[string]any{"source_key": "test_agent_action", "target_key": "test_entity", "type": "used_tool", "weight": .9})
			}
			return json.Marshal(map[string]any{"facts": []any{fact}, "impression": impression, "items": items, "relations": relations})
		}
		return json.RawMessage(`{"facts":[],"impression":null,"items":[]}`), nil
	case "keymemory.reconcile":
		if len(input.Allowed) == 0 || len(input.Facts) == 0 {
			return json.RawMessage(`{"memories":[]}`), nil
		}
		return json.Marshal(map[string]any{"memories": []any{map[string]any{"key": input.Allowed[0], "value": input.Facts[0].Content, "category": "test", "importance": 1, "decay_factor": 1, "source_document_ids": []string{input.Facts[0].ID}}}})
	default:
		return nil, errors.New("unexpected simulated model task")
	}
}
