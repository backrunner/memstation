package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/memoryllm"
	"memstation/apps/server/internal/testutil/memoryllmfixture"
)

type generatorFunc func(context.Context, memoryllm.Request) (json.RawMessage, error)

func (f generatorFunc) Generate(c context.Context, r memoryllm.Request) (json.RawMessage, error) {
	return f(c, r)
}

func sourceInput() Input {
	return Input{UserID: "owner", ConversationID: "conversation", Summary: domain.MemoryDocument{NodeID: "summary"}, Conversation: domain.ConversationRecord{UserID: "owner", ConversationID: "conversation", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "我不是项目负责人，先前的上线时间也只是一个假设。"}, {Role: domain.RoleAssistant, Text: "Then you should lead the release tomorrow."}}}}
}

func modelExtraction(t *testing.T, input Input) json.RawMessage {
	t.Helper()
	raw, err := (memoryllmfixture.Generator{}).Generate(context.Background(), memoryllm.Request{Task: "analysis.extract", Input: map[string]any{"sources": Sources(input.Conversation)}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func withBody(raw json.RawMessage) *Service {
	return NewService(generatorFunc(func(context.Context, memoryllm.Request) (json.RawMessage, error) { return raw, nil }))
}

func TestAnalysisRequiresLLMAndNeverGeneratesFallback(t *testing.T) {
	input := sourceInput()
	for _, s := range []*Service{NewService(), NewService(generatorFunc(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
		return nil, errors.New("provider unavailable")
	}))} {
		result, err := s.AnalyzeConversation(context.Background(), input)
		if err == nil || len(result.Facts) != 0 || result.Impression.ID != "" {
			t.Fatal("failed model produced memory", result, err)
		}
	}
}

func TestAnalysisAcceptsGenuineEmptyModelResult(t *testing.T) {
	result, err := withBody(json.RawMessage(`{"facts":[],"impression":null,"items":[]}`)).AnalyzeConversation(context.Background(), sourceInput())
	if err != nil || len(result.Facts) != 0 || result.Fact.ID != "" || result.Impression.ID != "" || len(result.GenericItems) != 0 {
		t.Fatal(result, err)
	}
}

func TestAnalysisStoresOnlyModelSelectedContentAndStableIDs(t *testing.T) {
	input := sourceInput()
	body := modelExtraction(t, input)
	s := withBody(body)
	first, err := s.AnalyzeConversation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AnalyzeConversation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same validated model output changed identities")
	}
	if first.Fact.Content != input.Conversation.Messages[0].Text || first.Fact.Metadata["fact_key"] != "test_fact" || first.Fact.Metadata["extraction_method"] != "llm" {
		t.Fatal(first.Fact)
	}
	for _, doc := range append(first.Facts, first.GenericItems...) {
		if !utf8.ValidString(doc.Content) || doc.UserID != "owner" || doc.Metadata["support_kind"] != "user_statement" {
			t.Fatal(doc)
		}
	}
}

func TestAnalysisRejectsMalformedOrUnsupportedModelOutput(t *testing.T) {
	input := sourceInput()
	base := modelExtraction(t, input)
	cases := map[string]func(map[string]any){
		"extra owner":   func(v map[string]any) { v["user_id"] = "foreign" },
		"invalid score": func(v map[string]any) { v["facts"].([]any)[0].(map[string]any)["importance"] = 9 },
		"invented quote": func(v map[string]any) {
			v["facts"].([]any)[0].(map[string]any)["quotes"] = []any{map[string]any{"source_id": "message-1", "text": "I own the project"}}
		},
		"assistant evidence": func(v map[string]any) {
			v["facts"].([]any)[0].(map[string]any)["quotes"] = []any{map[string]any{"source_id": "message-2", "text": input.Conversation.Messages[1].Text}}
		},
		"unknown source": func(v map[string]any) {
			v["facts"].([]any)[0].(map[string]any)["quotes"] = []any{map[string]any{"source_id": "missing", "text": "fake"}}
		},
		"missing facts": func(v map[string]any) { delete(v, "facts") },
		"unknown kind":  func(v map[string]any) { v["items"].([]any)[0].(map[string]any)["type"] = "api_key" },
		"foreign supersession": func(v map[string]any) {
			v["facts"].([]any)[0].(map[string]any)["supersedes"] = []string{"another-owner-fact"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(base, &value); err != nil {
				t.Fatal(err)
			}
			mutate(value)
			raw, _ := json.Marshal(value)
			if result, err := withBody(raw).AnalyzeConversation(context.Background(), input); err == nil || len(result.Facts) != 0 {
				t.Fatal("invalid batch accepted", result, err)
			}
		})
	}
}

func TestAnalysisOnlySupersedesExplicitMatchingFactIDs(t *testing.T) {
	input := sourceInput()
	input.ExistingFacts = []domain.MemoryDocument{{ID: "old", UserID: "owner", Type: domain.MemoryTypeFact, Content: "Old assertion", Metadata: map[string]any{"fact_key": "test_fact", "subject": "user"}}}
	raw := modelExtraction(t, input)
	var extraction Extraction
	if err := json.Unmarshal(raw, &extraction); err != nil {
		t.Fatal(err)
	}
	result, err := withBody(raw).AnalyzeConversation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fact.Metadata["supersedes_fact_ids"].([]string)) != 0 {
		t.Fatal("matching key implicitly superseded another fact")
	}
	extraction.Facts[0].Supersedes = []string{"old"}
	raw, _ = json.Marshal(extraction)
	result, err = withBody(raw).AnalyzeConversation(context.Background(), input)
	if err != nil || len(result.Fact.Metadata["supersedes_fact_ids"].([]string)) != 1 {
		t.Fatal(result, err)
	}
	input.ExistingFacts[0].UserID = "foreign"
	if _, err := withBody(raw).AnalyzeConversation(context.Background(), input); err == nil {
		t.Fatal("foreign existing fact accepted")
	}
}

func TestSummaryUsesModelForEveryChunkAndMerge(t *testing.T) {
	input := sourceInput()
	input.Conversation.Messages = []domain.UnifiedMessage{{Role: domain.RoleUser, Text: strings.Repeat("中文🙂", 6000)}}
	calls := []string{}
	generator := generatorFunc(func(ctx context.Context, r memoryllm.Request) (json.RawMessage, error) {
		calls = append(calls, r.Task)
		if r.Role != "summary_model" {
			t.Fatal(r.Role)
		}
		return json.RawMessage(`{"summary":"模型提炼后的摘要","topic_label":"项目计划","keywords":["项目"],"importance":1.2,"decay_factor":1.3}`), nil
	})
	result, err := NewService(generator).SummarizeConversation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "模型提炼后的摘要" || result.ChunkCount < 2 || calls[len(calls)-1] != "summary.merge" {
		t.Fatal(result, calls)
	}
	for _, batch := range mustSourceBatches(t, Sources(input.Conversation)) {
		for _, source := range batch {
			if !utf8.ValidString(source.Text) {
				t.Fatal("invalid UTF-8 source chunk")
			}
		}
	}
}

func mustSourceBatches(t *testing.T, sources []Source) [][]Source {
	t.Helper()
	b, err := sourceBatches(sources)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestKeyMemoryRequiresAllowedKeyAndValidatedFactReferences(t *testing.T) {
	input := sourceInput()
	documents := []domain.MemoryDocument{{ID: "fact", UserID: "owner", ConversationID: "conversation", Type: domain.MemoryTypeFact, Content: "偏好简洁的中文回复"}}
	good := json.RawMessage(`{"memories":[{"key":"preferences","value":"偏好简洁的中文回复","category":"profile","importance":1.4,"decay_factor":2,"source_document_ids":["fact"]}]}`)
	candidates, err := withBody(good).ReconcileKeyMemories(context.Background(), input, documents, nil, []string{"preferences"})
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(strings.ReplaceAll(string(good), "preferences", "unauthorized")), json.RawMessage(strings.ReplaceAll(string(good), `["fact"]`, `["foreign"]`)), json.RawMessage(`{"memories":null}`)} {
		if _, err := withBody(raw).ReconcileKeyMemories(context.Background(), input, documents, nil, []string{"preferences"}); err == nil {
			t.Fatal("invalid key memory output accepted")
		}
	}
}

func TestFactValidityUsesInstantsAcrossTimeZones(t *testing.T) {
	input := sourceInput()
	raw := modelExtraction(t, input)
	var extracted Extraction
	if err := json.Unmarshal(raw, &extracted); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		from, to string
		valid    bool
	}{
		{"2026-10-24T09:00:00+08:00", "2026-10-24T02:00:00Z", true},
		{"2026-10-24T09:00:00+08:00", "2026-10-24T08:00:00+07:00", false},
	} {
		extracted.Facts[0].ValidFrom = tc.from
		extracted.Facts[0].ValidTo = tc.to
		raw, _ = json.Marshal(extracted)
		_, err := withBody(raw).AnalyzeConversation(context.Background(), input)
		if (err == nil) != tc.valid {
			t.Fatalf("%s..%s: %v", tc.from, tc.to, err)
		}
	}
}

func TestSummaryMergeBoundsAllRequests(t *testing.T) {
	input := sourceInput()
	input.Conversation.Messages = []domain.UnifiedMessage{{Role: domain.RoleUser, Text: strings.Repeat("中文🙂", 15000)}}
	merges := 0
	generator := generatorFunc(func(ctx context.Context, r memoryllm.Request) (json.RawMessage, error) {
		encoded, _ := json.Marshal(r.Input)
		if len(encoded) > 128*1024 {
			t.Fatal("unbounded merge input")
		}
		if r.Task == "summary.merge" {
			merges++
			if len(r.Input.(map[string]any)["chunk_summaries"].([]Summary)) > 4 {
				t.Fatal("unbounded merge group")
			}
		}
		return json.Marshal(Summary{Summary: strings.Repeat("文", 8000), TopicLabel: "项目", Keywords: []string{}, Importance: 1, DecayFactor: 1})
	})
	result, err := NewService(generator).SummarizeConversation(context.Background(), input)
	if err != nil || result.ChunkCount < 5 || merges < 3 {
		t.Fatal(result.ChunkCount, merges, err)
	}
}
