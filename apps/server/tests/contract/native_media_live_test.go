package contract_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
	openaiprovider "memstation/apps/server/internal/provider/openai"
	analysisservice "memstation/apps/server/internal/service/analysis"
	"memstation/apps/server/internal/service/memoryllm"
	multimodalservice "memstation/apps/server/internal/service/multimodal"
	settingsservice "memstation/apps/server/internal/service/settings"
)

type nativeSmokeAdapter struct {
	ports.ProviderAdapter
	calls int
	usage domain.UsageRecord
}

func (a *nativeSmokeAdapter) Complete(ctx context.Context, in domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	a.calls++
	response, err := a.ProviderAdapter.Complete(ctx, in)
	if response != nil {
		a.usage.InputTokens += response.Usage.InputTokens
		a.usage.OutputTokens += response.Usage.OutputTokens
		a.usage.TotalTokens += response.Usage.TotalTokens
	}
	return response, err
}

// Explicit manual smoke: one logical extraction, bounded production schema and
// truncation retries, no benchmark or judge calls.
func TestLiveNativeImageMemory(t *testing.T) {
	if os.Getenv("MEMSTATION_NATIVE_MEDIA_SMOKE") != "true" {
		t.Skip("manual native-image smoke")
	}
	model, base := os.Getenv("MEMSTATION_NATIVE_MEDIA_MODEL"), os.Getenv("MEMSTATION_NATIVE_MEDIA_BASE_URL")
	if model == "" || base == "" {
		t.Fatal("explicit model and endpoint required")
	}
	image := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			image.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image); err != nil {
		t.Fatal(err)
	}
	adapter := &nativeSmokeAdapter{ProviderAdapter: openaiprovider.NewCompatibleAdapter(provider.StaticConfigResolver{domain.ProviderOpenAICompatible: {BaseURL: base, APIKey: os.Getenv("MEMSTATION_NATIVE_MEDIA_API_KEY")}}, 90*time.Second)}
	defer func() {
		t.Logf("Native media smoke: %d generation calls; input_tokens=%d output_tokens=%d total_tokens=%d; no judge", adapter.calls, adapter.usage.InputTokens, adapter.usage.OutputTokens, adapter.usage.TotalTokens)
	}()
	settings := settingsservice.NewService(nil, nil, model, model, model, "", 1024)
	client := memoryllm.NewClientWithOptions(provider.NewRegistry(domain.ProviderOpenAICompatible, adapter), settings, 90*time.Second, memoryllm.Options{ReasoningEffort: "low", MinOutputTokens: 2048, MaxOutputTokens: 8192})
	client.SetInputPreparer(&multimodalservice.InputPreparer{Policy: modelcap.InputPolicy{string(domain.ProviderOpenAICompatible) + ":" + model: {"image"}}})
	service := analysisservice.NewService(client)
	result, err := service.AnalyzeConversation(context.Background(), analysisservice.Input{UserID: "native-smoke", ConversationID: "native-smoke", Conversation: domain.ConversationRecord{UserID: "native-smoke", ConversationID: "native-smoke", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Blocks: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "Remember the solid color in this attached image as the status indicator color for project Prism. The color is specified only by the image."}, {Type: domain.ContentBlockImage, SourceURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes()), MIMEType: "image/png"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	observed := false
	for _, fact := range result.Facts {
		observations, _ := fact.Metadata["source_media_observations"].([]any)
		if len(observations) > 0 && strings.Contains(strings.ToLower(fact.Content), "red") {
			observed = true
		}
	}
	if output := os.Getenv("MEMSTATION_NATIVE_MEDIA_ARTIFACT"); output != "" {
		raw, err := json.MarshalIndent(map[string]any{"result": result, "usage": adapter.usage, "generation_calls": adapter.calls, "model": model}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(output, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !observed {
		t.Fatal("native image did not produce a grounded red-color observation")
	}
}
