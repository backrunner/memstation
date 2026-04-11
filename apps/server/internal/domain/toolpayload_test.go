package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStoredToolResultPayloadRoundTripsWrappedMetadataAndArrayOutput(t *testing.T) {
	t.Parallel()

	payload := StoredToolResultPayload([]ToolResult{{
		ToolCallID: "call-1",
		Name:       "inspect_assets",
		Output: []any{
			map[string]any{
				"type":      "input_image",
				"image_url": "https://example.com/chart.png",
				"text":      "launch Friday",
			},
		},
	}})

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result, ok := DecodeStoredToolResult(raw)
	if !ok {
		t.Fatalf("expected stored tool result to decode")
	}
	if result.ToolCallID != "call-1" || result.Name != "inspect_assets" {
		t.Fatalf("expected tool metadata to round-trip, got %+v", result)
	}

	gotOutput, ok := result.Output.([]any)
	if !ok {
		t.Fatalf("expected array output, got %#v", result.Output)
	}
	wantOutput := []any{
		map[string]any{
			"type":      "input_image",
			"image_url": "https://example.com/chart.png",
			"text":      "launch Friday",
		},
	}
	if !reflect.DeepEqual(gotOutput, wantOutput) {
		t.Fatalf("unexpected output: %#v", gotOutput)
	}
}

func TestDecodeStoredToolResultAcceptsLegacyRawOutput(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"content":[{"type":"document","source_url":"https://example.com/report.pdf"}]}`)
	result, ok := DecodeStoredToolResult(raw)
	if !ok {
		t.Fatalf("expected legacy raw output to decode")
	}
	if result.ToolCallID != "" || result.Name != "" {
		t.Fatalf("expected no wrapper metadata for legacy output, got %+v", result)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected object output, got %#v", result.Output)
	}
	content, ok := output["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected nested content array, got %#v", output["content"])
	}
}
