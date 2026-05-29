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

func TestDecodeStoredToolResultRejectsUnwrappedRawOutput(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"content":[{"type":"document","source_url":"https://example.com/report.pdf"}]}`)
	if result, ok := DecodeStoredToolResult(raw); ok {
		t.Fatalf("expected unwrapped raw output to be rejected, got %+v", result)
	}
}
