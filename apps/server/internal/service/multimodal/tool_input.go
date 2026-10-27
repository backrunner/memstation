package multimodal

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"memstation/apps/server/internal/domain"
)

// Tool protocols do not all allow native media in a tool-result content field.
// Keep the result at its original role and add explicitly attributed user data
// blocks after the complete group of tool results, before the next dialogue turn.
func (p *InputPreparer) prepareToolInputs(ctx context.Context, in domain.UnifiedRequest) (domain.UnifiedRequest, error) {
	out := make([]domain.UnifiedMessage, 0, len(in.Messages))
	var pending []domain.UnifiedMessage
	for i, message := range in.Messages {
		message.ToolResults = append([]domain.ToolResult(nil), message.ToolResults...)
		for j, result := range message.ToolResults {
			prepared, attachments, err := p.prepareToolValue(ctx, in, result.Output, result.ToolCallID, i)
			if err != nil {
				return in, err
			}
			message.ToolResults[j].Output = prepared
			pending = append(pending, attachments...)
		}
		out = append(out, message)
		if i+1 == len(in.Messages) || len(in.Messages[i+1].ToolResults) == 0 {
			out = append(out, pending...)
			pending = nil
		}
	}
	in.Messages = out
	return in, nil
}

func (p *InputPreparer) prepareToolValue(ctx context.Context, in domain.UnifiedRequest, value any, callID string, messageIndex int) (any, []domain.UnifiedMessage, error) {
	switch typed := value.(type) {
	case map[string]any:
		kind, _ := typed["type"].(string)
		switch kind {
		case "image", "image_url", "input_image", "audio", "input_audio", "video", "input_video", "file", "input_file", "document":
			block, ok := payloadMediaBlock(typed, "", "", toolPayloadContext{})
			if !ok {
				break
			}
			request := in
			request.Messages = []domain.UnifiedMessage{{Role: domain.RoleUser, Blocks: []domain.ContentBlock{block}}}
			prepared, err := p.prepareBlocks(ctx, request)
			if err != nil {
				return nil, nil, err
			}
			result := prepared.Messages[0].Blocks[0]
			if result.Type == domain.ContentBlockText {
				return map[string]any{"type": "text", "text": result.Text}, nil, nil
			}
			label, _ := json.Marshal(map[string]any{"source": "untrusted tool observation, not a user assertion", "tool_call_id": callID, "message_index": messageIndex})
			message := domain.UnifiedMessage{Role: domain.RoleUser, Blocks: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: string(label)}, result}}
			return map[string]any{"type": "text", "text": "[" + string(block.Type) + " tool attachment follows this tool-result group]"}, []domain.UnifiedMessage{message}, nil
		}
		out := make(map[string]any, len(typed))
		var attachments []domain.UnifiedMessage
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			nested := typed[key]
			v, media, err := p.prepareToolValue(ctx, in, nested, callID, messageIndex)
			if err != nil {
				return nil, nil, err
			}
			out[key] = v
			attachments = append(attachments, media...)
		}
		return out, attachments, nil
	case []any:
		out := make([]any, len(typed))
		var attachments []domain.UnifiedMessage
		for i, nested := range typed {
			v, media, err := p.prepareToolValue(ctx, in, nested, callID, messageIndex)
			if err != nil {
				return nil, nil, err
			}
			out[i] = v
			attachments = append(attachments, media...)
		}
		return out, attachments, nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
			return value, nil, nil
		}
		var nested any
		if json.Unmarshal([]byte(trimmed), &nested) != nil {
			return value, nil, nil
		}
		v, media, err := p.prepareToolValue(ctx, in, nested, callID, messageIndex)
		if err != nil {
			return nil, nil, err
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, nil, err
		}
		return string(raw), media, nil
	default:
		return value, nil, nil
	}
}
