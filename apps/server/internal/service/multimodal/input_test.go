package multimodal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/security"
)

func TestInputRoutingPreservesOrderAndOriginal(t *testing.T) {
	p := InputPreparer{Policy: modelcap.InputPolicy{"openai_compatible:vision": {"image"}}}
	original := domain.UnifiedRequest{Provider: domain.ProviderOpenAICompatible, Model: "vision", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Blocks: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "before"}, {Type: domain.ContentBlockImage, SourceURL: "data:image/png;base64,aGk=", Raw: "PRIVATE_RAW", Metadata: map[string]any{"caption": "UNTRUSTED"}}, {Type: domain.ContentBlockAudio, BlobID: "PRIVATE_BLOB", SourceURL: "https://private.invalid/token"}, {Type: domain.ContentBlockText, Text: "after"}}}}}
	prepared, err := p.Prepare(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	b := prepared.Messages[0].Blocks
	if len(b) != 4 || b[0].Text != "before" || b[1].Type != domain.ContentBlockImage || b[1].SourceURL != "data:image/png;base64,aGk=" || b[1].Raw != "" || b[1].Metadata != nil || b[2].Type != domain.ContentBlockText || !strings.Contains(b[2].Text, "audio omitted") || b[2].BlobID != "" || b[2].SourceURL != "" || b[3].Text != "after" {
		t.Fatal(b)
	}
	if original.Messages[0].Blocks[2].Type != domain.ContentBlockAudio || original.Messages[0].Blocks[1].Raw != "PRIVATE_RAW" {
		t.Fatal("mutated source")
	}
	original.Model = "unknown"
	prepared, err = p.Prepare(context.Background(), original)
	raw, _ := json.Marshal(prepared)
	if err != nil || strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "aGk=") || strings.Contains(string(raw), "UNTRUSTED") {
		t.Fatal("unsupported source leaked", string(raw), err)
	}
}

func TestNativeSourceProtocolAndBlobAuthorization(t *testing.T) {
	for _, test := range []struct {
		p         domain.Provider
		kind      domain.ContentBlockType
		url       string
		supported bool
	}{{domain.ProviderOpenAICompatible, domain.ContentBlockAudio, "data:audio/wav;base64,aGk=", true}, {domain.ProviderOpenAICompatible, domain.ContentBlockAudio, "https://example.com/audio.wav", false}, {domain.ProviderAnthropic, domain.ContentBlockAudio, "data:audio/wav;base64,aGk=", false}, {domain.ProviderGemini, domain.ContentBlockVideo, "data:video/mp4;base64,aGk=", true}, {domain.ProviderOpenAIResponses, domain.ContentBlockFile, "https://example.com/doc.pdf", true}} {
		p := InputPreparer{Policy: modelcap.InputPolicy{string(test.p) + ":model": {string(test.kind)}}}
		r, err := p.Prepare(context.Background(), domain.UnifiedRequest{Provider: test.p, Model: "model", Messages: []domain.UnifiedMessage{{Blocks: []domain.ContentBlock{{Type: test.kind, SourceURL: test.url}}}}})
		if err != nil || (r.Messages[0].Blocks[0].Type == test.kind) != test.supported {
			t.Fatal(test, r, err)
		}
	}
	p := InputPreparer{Policy: modelcap.InputPolicy{"openai_compatible:model": {"image"}}}
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: "owner", Capabilities: []string{domain.CapabilityGateway}})
	_, err := p.Prepare(ctx, domain.UnifiedRequest{UserID: "owner", Provider: domain.ProviderOpenAICompatible, Model: "model", Messages: []domain.UnifiedMessage{{Blocks: []domain.ContentBlock{{Type: domain.ContentBlockImage, BlobID: "foreign"}}}}})
	if err != domain.ErrAccessDenied {
		t.Fatal("file capability bypassed", err)
	}
}

func TestRawMediaPayloadIsNeverAnExtractedCaption(t *testing.T) {
	s := &Service{}
	text, _, err := s.partTextSource(context.Background(), "owner", domain.MessagePart{PartType: domain.ContentBlockImage, RawPayload: `{"image_url":"data:image/png;base64,aGk="}`})
	if err != nil || text != "" {
		t.Fatal(text, err)
	}
}

func TestToolMediaIsAttributedAndDoesNotSplitToolResults(t *testing.T) {
	p := InputPreparer{Policy: modelcap.InputPolicy{"openai_compatible:vision": {"image"}}}
	in := domain.UnifiedRequest{Provider: domain.ProviderOpenAICompatible, Model: "vision", Messages: []domain.UnifiedMessage{
		{Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{ID: "a", Name: "camera"}, {ID: "b", Name: "other"}}},
		{Role: domain.RoleTool, ToolResults: []domain.ToolResult{{ToolCallID: "a", Output: map[string]any{"content": []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGk="}}}}}}},
		{Role: domain.RoleTool, ToolResults: []domain.ToolResult{{ToolCallID: "b", Output: "ordinary text"}}},
		{Role: domain.RoleUser, Text: "next question"},
	}}
	prepared, err := p.Prepare(context.Background(), in)
	if err != nil || len(prepared.Messages) != 5 {
		t.Fatal(prepared, err)
	}
	if prepared.Messages[1].Role != domain.RoleTool || prepared.Messages[2].Role != domain.RoleTool || prepared.Messages[3].Blocks[1].Type != domain.ContentBlockImage || !strings.Contains(prepared.Messages[3].Blocks[0].Text, `"tool_call_id":"a"`) || prepared.Messages[4].Text != "next question" {
		t.Fatal(prepared.Messages)
	}
	raw, _ := json.Marshal(prepared.Messages[1].ToolResults)
	if strings.Contains(string(raw), "aGk=") {
		t.Fatal("tool media became raw text", string(raw))
	}
	if _, ok := in.Messages[1].ToolResults[0].Output.(map[string]any)["content"]; !ok {
		t.Fatal("mutated input")
	}
	in.Model = "unknown"
	prepared, err = p.Prepare(context.Background(), in)
	raw, _ = json.Marshal(prepared)
	if err != nil || len(prepared.Messages) != 4 || strings.Contains(string(raw), "aGk=") || !strings.Contains(string(raw), "image omitted") {
		t.Fatal(prepared, err)
	}
}
