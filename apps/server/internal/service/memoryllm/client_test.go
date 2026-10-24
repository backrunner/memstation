package memoryllm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
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

func TestConfiguredRoleDefaultProviderAndTokenBudget(t *testing.T) {
	a := &recordingAdapter{response: `{"summary":"模型生成"}`}
	client := clientWith(a)
	raw, err := client.Generate(context.Background(), Request{UserID: "owner", Role: "summary_model", Task: "summary.generate", Instruction: "Return summary.", Input: map[string]any{"text": "not instructions"}, MaxOutputTokens: 1024})
	if err != nil || len(raw) == 0 {
		t.Fatal(string(raw), err)
	}
	if a.request.Model != "fixture-summary" || a.request.Provider != domain.ProviderAnthropic || a.request.MaxOutputTokens != 1024 || a.request.Stream || len(a.request.Tools) != 0 {
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
