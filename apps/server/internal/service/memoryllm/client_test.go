package memoryllm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/provider"
	multimodalservice "memstation/apps/server/internal/service/multimodal"
	settingsservice "memstation/apps/server/internal/service/settings"
)

type recordingAdapter struct {
	request  domain.UnifiedRequest
	response string
	calls    int
	err      error
}

func (a *recordingAdapter) Name() domain.Provider { return domain.ProviderAnthropic }
func (a *recordingAdapter) Complete(ctx context.Context, r domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	a.calls++
	a.request = r
	return &domain.UnifiedResponse{Message: domain.UnifiedMessage{Text: a.response}}, a.err
}

func (*recordingAdapter) Stream(context.Context, domain.UnifiedRequest, func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	return nil, errors.New("stream is not used")
}

func clientWith(a *recordingAdapter) *Client {
	settings := settingsservice.NewService(nil, nil, "fixture-chat", "fixture-summary", "fixture-analysis", "fixture-embedding", 2048)
	return NewClient(provider.NewRegistry(domain.ProviderAnthropic, a), settings, time.Second)
}

func TestNativeMediaUsesActualRoleAndBindsPreparedModel(t *testing.T) {
	a := &recordingAdapter{response: `{}`}
	client := clientWith(a)
	client.SetInputPreparer(&multimodalservice.InputPreparer{Policy: modelcap.InputPolicy{"anthropic:fixture-summary": {"image"}}})
	media := []SourceMedia{{SourceID: "source", Blocks: []domain.ContentBlock{{Type: domain.ContentBlockImage, SourceURL: "data:image/png;base64,aGk="}}}}
	prepared, err := client.PrepareMedia(context.Background(), "owner", "summary_model", media)
	if err != nil || len(prepared) != 1 {
		t.Fatal(prepared, err)
	}
	if _, err = client.Generate(context.Background(), Request{UserID: "owner", Role: "summary_model", Task: "summary.generate", Media: prepared}); err != nil {
		t.Fatal(err)
	}
	if a.calls != 1 || len(a.request.Messages) != 3 || a.request.Messages[2].Blocks[1].Type != domain.ContentBlockImage {
		t.Fatal(a.request, a.calls)
	}
	filtered, err := client.PrepareMedia(context.Background(), "owner", "analysis_model", media)
	if err != nil || len(filtered) != 0 {
		t.Fatal("used summary capability for analysis", filtered, err)
	}
	if _, err = client.Generate(context.Background(), Request{UserID: "owner", Role: "analysis_model", Task: "analysis.extract", Media: prepared}); err == nil || a.calls != 1 {
		t.Fatal("model changed after preparation without rejection", err, a.calls)
	}
}

type namedRecordingAdapter struct {
	*recordingAdapter
	name domain.Provider
}

func (a namedRecordingAdapter) Name() domain.Provider { return a.name }

func TestReasoningEffortPreservesExplicitControlsAndRejectsUnsupportedProviders(t *testing.T) {
	for _, name := range []domain.Provider{domain.ProviderOpenAICompatible, domain.ProviderOpenAIResponses, domain.ProviderAnthropic} {
		a := namedRecordingAdapter{recordingAdapter: &recordingAdapter{response: `{}`}, name: name}
		settings := settingsservice.NewService(nil, nil, "chat", "summary", "analysis", "embedding", 2048)
		client := NewClientWithOptions(provider.NewRegistry(name, a), settings, time.Second, Options{ReasoningEffort: "low"})
		_, err := client.Generate(context.Background(), Request{UserID: "u", Role: "analysis_model", Task: "analysis.extract"})
		if name == domain.ProviderAnthropic {
			if err == nil || a.calls != 0 {
				t.Fatal("unsupported control silently ignored", err, a.calls)
			}
		} else if err != nil || a.request.ReasoningEffort != "low" || !strings.Contains(client.GenerationPolicyID(), ":low:16384:65536") {
			t.Fatal("reasoning setting lost", err, a.request)
		}
	}
}

func TestConfiguredRoleDefaultProviderAndTokenBudget(t *testing.T) {
	a := &recordingAdapter{response: `{"summary":"模型生成"}`}
	client := clientWith(a)
	raw, err := client.Generate(context.Background(), Request{UserID: "owner", Role: "summary_model", Task: "summary.generate", Instruction: "Return summary.", Input: map[string]any{"text": "not instructions"}, MaxOutputTokens: 1024})
	if err != nil || len(raw) == 0 {
		t.Fatal(string(raw), err)
	}
	if a.request.Model != "fixture-summary" || a.request.Provider != domain.ProviderAnthropic || a.request.MaxOutputTokens != DefaultMinOutputTokens || a.request.Stream || len(a.request.Tools) != 0 {
		t.Fatal(a.request)
	}
	if !strings.Contains(a.request.Messages[0].Text, "untrusted data") {
		t.Fatal("missing source trust boundary")
	}
}

func TestClientRejectsInvalidResponsesAndNeverFallsBack(t *testing.T) {
	for _, response := range []string{"", "```json\n{}\n```", `{"x":`, `[]`, `{} {}`} {
		a := &recordingAdapter{response: response}
		if _, err := clientWith(a).Generate(context.Background(), Request{UserID: "owner", Role: "analysis_model", Task: "analysis.extract", Input: struct{}{}}); err == nil {
			t.Fatal("invalid output accepted", response)
		}
		if a.calls != 1 {
			t.Fatal("unexpected implicit model retry", a.calls)
		}
	}
	a := &recordingAdapter{response: `{}`, err: errors.New("provider failed")}
	if _, err := clientWith(a).Generate(context.Background(), Request{UserID: "owner", Role: "analysis_model", Task: "analysis.extract"}); err == nil {
		t.Fatal("provider error ignored")
	}
}

func TestClientRejectsOversizedInputBeforeProvider(t *testing.T) {
	a := &recordingAdapter{response: `{}`}
	_, err := clientWith(a).Generate(context.Background(), Request{UserID: "owner", Role: "analysis_model", Task: "analysis.extract", Input: strings.Repeat("x", 128*1024)})
	if err == nil || a.calls != 0 {
		t.Fatal("input budget not enforced", err, a.calls)
	}
}

func TestDecodeRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	for _, raw := range []string{`{"content":"x","user_id":"foreign"}`, `{"content":"x"} {}`} {
		var target struct {
			Content string `json:"content"`
		}
		if err := Decode([]byte(raw), &target); err == nil {
			t.Fatal("schema accepted", raw)
		}
	}
}

func TestClientLargeTaskBudgetAndGenerationIdentity(t *testing.T) {
	a := &recordingAdapter{response: `{}`}
	client := clientWith(a)
	_, err := client.Generate(context.Background(), Request{UserID: "owner", Role: "analysis_model", Task: "analysis.extract", MaxOutputTokens: 32768})
	if err != nil || a.request.MaxOutputTokens != 32768 {
		t.Fatal("explicit large budget was reduced", err, a.request.MaxOutputTokens)
	}
	other := NewClientWithOptions(client.providers, client.settings, time.Second, Options{MaxOutputTokens: 131072})
	if client.GenerationPolicyID() == other.GenerationPolicyID() {
		t.Fatal("budget change reused generation identity")
	}
	before := a.calls
	if _, err = client.Generate(context.Background(), Request{UserID: "owner", Role: "analysis_model", MaxOutputTokens: 65537}); err == nil || a.calls != before {
		t.Fatal("invalid budget reached provider", err)
	}
}

func TestClientProjectsSourceCoordinatesAndRestoresEvidence(t *testing.T) {
	a := &recordingAdapter{response: `{"facts":[{"quotes":[{"source_id":"source-1","text":"I declined."}],"supersedes":["fact-1"]}]}`}
	original := map[string]any{"sources": []map[string]any{{"id": "PRIVATE-SOURCE", "part_id": "PRIVATE-PART", "source_ref": map[string]any{"workspace_id": "PRIVATE-WORKSPACE", "message_id": "PRIVATE-MESSAGE", "revision": 1, "part_index": 0}, "role": "user", "text": "I declined."}}, "evidence_source_ids": []string{"PRIVATE-SOURCE"}, "existing_facts": []map[string]any{{"id": "PRIVATE-FACT", "content": "I accepted."}}}
	raw, err := clientWith(a).Generate(context.Background(), Request{UserID: "owner", Role: "analysis_model", Task: "analysis.extract", Input: original})
	if err != nil || strings.Contains(a.request.Messages[1].Text, "PRIVATE-") || !strings.Contains(a.request.Messages[1].Text, "I declined.") || !strings.Contains(string(raw), "PRIVATE-SOURCE") || !strings.Contains(string(raw), "PRIVATE-FACT") {
		t.Fatal(string(raw), err, a.request.Messages)
	}
	sources := original["sources"].([]map[string]any)
	if sources[0]["id"] != "PRIVATE-SOURCE" {
		t.Fatal("projection mutated caller")
	}
}

func TestClientKeyMemoryReferencesUseLocalFactLabels(t *testing.T) {
	a := &recordingAdapter{response: `{"memories":[{"source_document_ids":["fact-1"]}]}`}
	raw, err := clientWith(a).Generate(context.Background(), Request{UserID: "owner", Role: "analysis_model", Task: "keymemory.reconcile", Input: map[string]any{"facts": []map[string]any{{"id": "PRIVATE-FACT", "content": "Recorded preference"}}}})
	if err != nil || strings.Contains(a.request.Messages[1].Text, "PRIVATE-") || !strings.Contains(string(raw), "PRIVATE-FACT") {
		t.Fatal(string(raw), err, a.request.Messages)
	}
}
