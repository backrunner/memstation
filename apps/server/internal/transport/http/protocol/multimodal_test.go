package protocol

import (
	"encoding/json"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

func TestNormalizeChatCompletionPreservesMultimodalBlocks(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal([]map[string]any{
		{"type": "input_text", "text": "Please analyze these sources."},
		{
			"type": "input_image",
			"image_url": map[string]any{
				"url":       "https://example.com/whiteboard.png",
				"mime_type": "image/png",
			},
			"filename": "whiteboard.png",
			"text":     "whiteboard launch notes",
		},
		{
			"type": "input_audio",
			"audio": map[string]any{
				"url":       "https://example.com/meeting.m4a",
				"mime_type": "audio/mp4",
			},
			"text":     "Alice confirmed the rollback owner is Chen.",
			"start_ms": 1500,
			"end_ms":   4200,
		},
		{
			"type":       "file",
			"source_url": "https://example.com/spec.pdf",
			"mime_type":  "application/pdf",
			"filename":   "spec.pdf",
			"page_index": 3,
			"text":       "Release checklist and rollback conditions.",
		},
	})
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}

	req := NormalizeChatCompletion(OpenAIChatCompletionsRequest{
		Model: "gpt-4.1",
		Messages: []OpenAIChatMessage{{
			Role:    "user",
			Content: raw,
		}},
	}, HeaderOptions{})

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	blocks := req.Messages[0].Blocks
	if len(blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(blocks))
	}
	if blocks[1].Type != domain.ContentBlockImage || blocks[1].SourceURL != "https://example.com/whiteboard.png" {
		t.Fatalf("unexpected image block: %+v", blocks[1])
	}
	if blocks[2].Type != domain.ContentBlockAudio || blocks[2].StartMS != 1500 || blocks[2].EndMS != 4200 {
		t.Fatalf("unexpected audio block: %+v", blocks[2])
	}
	if blocks[3].Type != domain.ContentBlockFile || blocks[3].PageIndex != 3 {
		t.Fatalf("unexpected file block: %+v", blocks[3])
	}
}

func TestNormalizeResponsesPreservesMultimodalBlocks(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal([]map[string]any{{
		"type": "message",
		"role": "user",
		"content": []map[string]any{
			{"type": "input_text", "text": "Summarize the uploaded assets."},
			{
				"type":      "input_image",
				"image_url": "https://example.com/diagram.png",
				"mime_type": "image/png",
				"filename":  "diagram.png",
				"text":      "diagram title",
			},
			{
				"type":       "file",
				"source_url": "https://example.com/report.pdf",
				"mime_type":  "application/pdf",
				"filename":   "report.pdf",
				"text":       "quarterly report excerpt",
			},
		},
	}})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	req := NormalizeResponses(OpenAIResponsesRequest{
		Model: "gpt-4.1",
		Input: raw,
	}, HeaderOptions{})

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	blocks := req.Messages[0].Blocks
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}
	if blocks[1].Type != domain.ContentBlockImage || blocks[2].Type != domain.ContentBlockFile {
		t.Fatalf("unexpected response blocks: %+v", blocks)
	}
}

func TestNormalizeResponsesPreservesStructuredToolOutput(t *testing.T) {
	t.Parallel()

	req := NormalizeResponses(OpenAIResponsesRequest{
		Model: "gpt-4.1",
		Input: mustMarshalJSON(t, []map[string]any{{
			"type":    "function_call_output",
			"call_id": "call-1",
			"output": []map[string]any{
				{
					"type":       "file",
					"source_url": "https://example.com/report.pdf",
					"mime_type":  "application/pdf",
					"text":       "report confirms Chen owns rollback",
				},
			},
		}}),
	}, HeaderOptions{})

	if len(req.Messages) != 1 || len(req.Messages[0].ToolResults) != 1 {
		t.Fatalf("unexpected normalized tool result message: %+v", req.Messages)
	}
	content, ok := req.Messages[0].ToolResults[0].Output.([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected structured tool output content, got %#v", req.Messages[0].ToolResults[0].Output)
	}
}

func TestFormatResponsesPreservesMultimodalBlocks(t *testing.T) {
	t.Parallel()

	payload := FormatResponses(domain.UnifiedResponse{
		ResponseID: "resp-mm-1",
		Protocol:   domain.ProtocolOpenAIResponses,
		Provider:   domain.ProviderOpenAICompatible,
		Model:      "gpt-4.1",
		CreatedAt:  time.Date(2026, 4, 11, 9, 0, 0, 0, time.UTC),
		Message: domain.UnifiedMessage{
			Role: domain.RoleAssistant,
			Blocks: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "I found two relevant source assets."},
				{Type: domain.ContentBlockImage, SourceURL: "https://example.com/whiteboard.png", MIMEType: "image/png", Filename: "whiteboard.png"},
				{Type: domain.ContentBlockFile, SourceURL: "https://example.com/spec.pdf", MIMEType: "application/pdf", Filename: "spec.pdf", PageIndex: 2},
			},
		},
	})

	output, ok := payload["output"].([]map[string]any)
	if !ok || len(output) != 1 {
		t.Fatalf("unexpected response output: %#v", payload["output"])
	}
	content, ok := output[0]["content"].([]map[string]any)
	if !ok || len(content) != 3 {
		t.Fatalf("unexpected response content: %#v", output[0]["content"])
	}
	if content[1]["type"] != "input_image" {
		t.Fatalf("expected image block, got %#v", content[1])
	}
	if content[2]["type"] != "file" || content[2]["page_index"] != 2 {
		t.Fatalf("expected file block with page index, got %#v", content[2])
	}
}

func TestNormalizeAndFormatAnthropicPreserveMultimodalBlocks(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal([]map[string]any{
		{"type": "text", "text": "Review the image and the PDF."},
		{"type": "image", "source_url": "https://example.com/photo.jpg", "mime_type": "image/jpeg"},
		{"type": "document", "source_url": "https://example.com/brief.pdf", "mime_type": "application/pdf", "filename": "brief.pdf"},
		{"type": "tool_use", "id": "call_1", "name": "recall_memory", "input": map[string]any{"query": "rollback owner"}},
	})
	if err != nil {
		t.Fatalf("marshal anthropic content: %v", err)
	}

	req := NormalizeAnthropic(AnthropicMessagesRequest{
		Model: "anthropic:claude-sonnet",
		Messages: []AnthropicMessage{{
			Role:    "assistant",
			Content: raw,
		}},
	}, HeaderOptions{})

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	if len(req.Messages[0].Blocks) != 4 {
		t.Fatalf("expected 4 anthropic blocks, got %d", len(req.Messages[0].Blocks))
	}
	if len(req.Messages[0].ToolCalls) != 1 || req.Messages[0].ToolCalls[0].Name != "recall_memory" {
		t.Fatalf("unexpected anthropic tool calls: %+v", req.Messages[0].ToolCalls)
	}

	formatted := FormatAnthropic(domain.UnifiedResponse{
		ResponseID: "msg-mm-1",
		Protocol:   domain.ProtocolAnthropic,
		Provider:   domain.ProviderAnthropic,
		Model:      "claude-sonnet",
		Message: domain.UnifiedMessage{
			Role: domain.RoleAssistant,
			Blocks: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "I extracted the diagram and transcript."},
				{Type: domain.ContentBlockImage, SourceURL: "https://example.com/frame.jpg", MIMEType: "image/jpeg"},
				{Type: domain.ContentBlockAudio, SourceURL: "https://example.com/audio.mp3", MIMEType: "audio/mpeg"},
			},
		},
	})

	content, ok := formatted["content"].([]map[string]any)
	if !ok || len(content) != 3 {
		t.Fatalf("unexpected anthropic formatted content: %#v", formatted["content"])
	}
	if content[1]["type"] != "image" || content[2]["type"] != "audio" {
		t.Fatalf("unexpected anthropic content types: %#v", content)
	}
}

func TestNormalizeAnthropicPreservesStructuredToolResultOutput(t *testing.T) {
	t.Parallel()

	req := NormalizeAnthropic(AnthropicMessagesRequest{
		Model: "anthropic:claude-sonnet",
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: mustMarshalJSON(t, []map[string]any{
				{
					"type":        "tool_result",
					"tool_use_id": "call-1",
					"content": []map[string]any{
						{
							"type":       "image",
							"source_url": "https://example.com/frame.png",
							"mime_type":  "image/png",
						},
					},
				},
			}),
		}},
	}, HeaderOptions{})

	if len(req.Messages) != 1 || len(req.Messages[0].ToolResults) != 1 {
		t.Fatalf("unexpected anthropic tool result message: %+v", req.Messages)
	}
	content, ok := req.Messages[0].ToolResults[0].Output.([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected anthropic structured tool result content, got %#v", req.Messages[0].ToolResults[0].Output)
	}
}

func mustMarshalJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}
