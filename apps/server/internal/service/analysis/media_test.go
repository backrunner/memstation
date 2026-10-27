package analysis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/memoryllm"
)

type nativeGenerator struct {
	generatorFunc
	supported bool
}

func (g nativeGenerator) PrepareMedia(_ context.Context, _ string, _ string, media []memoryllm.SourceMedia) ([]memoryllm.SourceMedia, error) {
	if g.supported {
		return media, nil
	}
	return nil, nil
}

func TestNativeSummaryAndFactInputs(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsupported", true: "native"}[supported], func(t *testing.T) {
			input := sourceInput()
			input.Conversation.Messages = []domain.UnifiedMessage{{Role: domain.RoleUser, Blocks: []domain.ContentBlock{{Type: domain.ContentBlockImage, SourceURL: "data:image/png;base64,aGk=", Raw: "PRIVATE_PAYLOAD"}}}}
			calls := 0
			g := nativeGenerator{supported: supported, generatorFunc: func(_ context.Context, r memoryllm.Request) (json.RawMessage, error) {
				calls++
				sources := r.Input.(map[string]any)["sources"].([]Source)
				if len(sources) != 1 || sources[0].NativeMedia != supported || (len(r.Media) > 0) != supported {
					t.Fatal("media routing lost", sources, r.Media)
				}
				encoded, _ := json.Marshal(r.Input)
				if strings.Contains(string(encoded), "aGk=") || strings.Contains(string(encoded), "PRIVATE_PAYLOAD") {
					t.Fatal("media serialized as text")
				}
				if r.Task == "summary.generate" {
					return json.RawMessage(`{"summary":"A supplied image.","topic_label":"image","keywords":[],"importance":1,"decay_factor":1}`), nil
				}
				if !supported {
					return json.RawMessage(`{"facts":[],"items":[],"impression":null}`), nil
				}
				return json.Marshal(map[string]any{"facts": []any{map[string]any{"key": "image_content", "category": "observation", "subject": "attachment", "content": "The image shows a dog.", "confidence": 0.7, "importance": 1, "decay_factor": 1, "quotes": []any{}, "supersedes": []any{}, "media_observations": []MediaObservation{{SourceID: sources[0].ID, Observation: "A dog is visible."}}}}, "items": []any{}, "impression": nil})
			}}
			s := NewService(g)
			if _, err := s.SummarizeConversation(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			result, err := s.AnalyzeConversation(context.Background(), input)
			if err != nil || calls != 2 {
				t.Fatal(result, err, calls)
			}
			if supported {
				if len(result.Facts) != 1 || len(result.Facts[0].Metadata["source_quotes"].([]any)) != 0 || len(result.Facts[0].Metadata["source_media_observations"].([]any)) != 1 || result.Facts[0].Metadata["support_kind"] != "model_inference" {
					t.Fatal(result)
				}
			} else if len(result.Facts) != 0 {
				t.Fatal("unsupported image produced fact")
			}
		})
	}
}

func TestMediaObservationsRejectUnsupportedAssistantAndFabricatedQuote(t *testing.T) {
	item := Item{Subject: "attachment", Content: "A dog is visible", Importance: 1, DecayFactor: 1, Confidence: .7, MediaObservations: []MediaObservation{{SourceID: "image", Observation: "A dog"}}}
	for _, source := range []Source{{ID: "image", Role: domain.RoleUser, Text: "[image omitted]", MediaSHA256: "hash"}, {ID: "image", Role: domain.RoleAssistant, NativeMedia: true, media: []domain.ContentBlock{{Type: domain.ContentBlockImage}}}} {
		if _, err := documentFromItem(item, domain.MemoryTypeFact, "owner", "conv", []Source{source}); err == nil {
			t.Fatal("invalid observation accepted", source)
		}
	}
	item.MediaObservations = nil
	item.Quotes = []Quote{{SourceID: "image", Text: "[image attachment]"}}
	if _, err := documentFromItem(item, domain.MemoryTypeFact, "owner", "conv", []Source{{ID: "image", Role: domain.RoleUser, Text: "[image attachment]", MediaSHA256: "hash"}}); err == nil {
		t.Fatal("marker accepted as fact evidence")
	}
}
