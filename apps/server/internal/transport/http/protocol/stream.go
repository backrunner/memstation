package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"memstation/apps/server/internal/domain"
)

type StreamWriter interface {
	Event(domain.StreamEvent) error
	Close(domain.UnifiedResponse) error
	Error(error) error
	Started() bool
}

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	started bool
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming unsupported")
	}
	return &sseWriter{w: w, flusher: flusher}, nil
}

func (w *sseWriter) Started() bool { return w.started }

func (w *sseWriter) ensureStarted() {
	if w.started {
		return
	}
	w.started = true
	w.w.Header().Set("Content-Type", "text/event-stream")
	w.w.Header().Set("Cache-Control", "no-cache")
	w.w.Header().Set("Connection", "keep-alive")
	w.w.Header().Set("X-Accel-Buffering", "no")
}

func (w *sseWriter) writeData(payload any) error {
	w.ensureStarted()
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := w.w.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := w.w.Write(raw); err != nil {
		return err
	}
	if _, err := w.w.Write([]byte("\n\n")); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

func (w *sseWriter) writeNamedEvent(name string, payload any) error {
	w.ensureStarted()
	if name != "" {
		if _, err := w.w.Write([]byte("event: " + name + "\n")); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := w.w.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := w.w.Write(raw); err != nil {
		return err
	}
	if _, err := w.w.Write([]byte("\n\n")); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

func (w *sseWriter) writeRaw(value string) error {
	w.ensureStarted()
	if _, err := w.w.Write([]byte(value)); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

type ChatCompletionStreamWriter struct {
	base        *sseWriter
	response    domain.UnifiedResponse
	roleSent    bool
	contentSent bool
	usageSeen   domain.UsageRecord
}

func NewChatCompletionStreamWriter(w http.ResponseWriter) (*ChatCompletionStreamWriter, error) {
	base, err := newSSEWriter(w)
	if err != nil {
		return nil, err
	}
	return &ChatCompletionStreamWriter{base: base}, nil
}

func (w *ChatCompletionStreamWriter) Started() bool { return w.base.Started() }

func (w *ChatCompletionStreamWriter) Event(event domain.StreamEvent) error {
	switch event.Type {
	case domain.StreamEventResponseStart:
		w.response.ResponseID = firstNonEmpty(event.ResponseID, w.response.ResponseID)
		w.response.Model = firstNonEmpty(event.Model, w.response.Model)
		w.response.Provider = event.Provider
		if !event.CreatedAt.IsZero() {
			w.response.CreatedAt = event.CreatedAt
		}
		return w.ensureRoleChunk(firstNonEmpty(string(event.Role), string(domain.RoleAssistant)))
	case domain.StreamEventTextDelta:
		w.response.ResponseID = firstNonEmpty(event.ResponseID, w.response.ResponseID)
		w.response.Model = firstNonEmpty(event.Model, w.response.Model)
		w.response.Provider = event.Provider
		if !event.CreatedAt.IsZero() && w.response.CreatedAt.IsZero() {
			w.response.CreatedAt = event.CreatedAt
		}
		if err := w.ensureRoleChunk(firstNonEmpty(string(event.Role), string(domain.RoleAssistant))); err != nil {
			return err
		}
		if event.Delta == "" {
			return nil
		}
		w.response.Message.Text += event.Delta
		w.contentSent = true
		return w.base.writeData(chatChunk(w.response.ResponseID, w.response.Model, w.createdUnix(), map[string]any{
			"content": event.Delta,
		}, nil))
	case domain.StreamEventUsage:
		w.usageSeen = event.Usage
		w.response.Usage = event.Usage
	}
	return nil
}

func (w *ChatCompletionStreamWriter) Close(response domain.UnifiedResponse) error {
	w.mergeResponse(response)
	if err := w.ensureRoleChunk("assistant"); err != nil {
		return err
	}
	if !w.contentSent && w.response.Message.Text != "" {
		if err := w.base.writeData(chatChunk(w.response.ResponseID, w.response.Model, w.createdUnix(), map[string]any{
			"content": w.response.Message.Text,
		}, nil)); err != nil {
			return err
		}
	}
	if len(w.response.Message.ToolCalls) > 0 {
		if err := w.base.writeData(chatChunk(w.response.ResponseID, w.response.Model, w.createdUnix(), map[string]any{
			"tool_calls": formatOpenAIStreamToolCalls(w.response.Message.ToolCalls),
		}, nil)); err != nil {
			return err
		}
	}
	if w.response.Usage.TotalTokens > 0 {
		if err := w.base.writeData(map[string]any{
			"id":      w.response.ResponseID,
			"object":  "chat.completion.chunk",
			"created": w.createdUnix(),
			"model":   w.response.Model,
			"choices": []map[string]any{},
			"usage": map[string]any{
				"prompt_tokens":     w.response.Usage.InputTokens,
				"completion_tokens": w.response.Usage.OutputTokens,
				"total_tokens":      w.response.Usage.TotalTokens,
			},
		}); err != nil {
			return err
		}
	}
	finishReason := "stop"
	if len(w.response.Message.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if err := w.base.writeData(chatChunk(w.response.ResponseID, w.response.Model, w.createdUnix(), map[string]any{}, finishReason)); err != nil {
		return err
	}
	return w.base.writeRaw("data: [DONE]\n\n")
}

func (w *ChatCompletionStreamWriter) Error(err error) error {
	return w.base.writeData(map[string]any{
		"error": map[string]any{
			"message": err.Error(),
		},
	})
}

func (w *ChatCompletionStreamWriter) ensureRoleChunk(role string) error {
	if w.roleSent {
		return nil
	}
	w.roleSent = true
	return w.base.writeData(chatChunk(w.response.ResponseID, w.response.Model, w.createdUnix(), map[string]any{
		"role": role,
	}, nil))
}

func (w *ChatCompletionStreamWriter) mergeResponse(response domain.UnifiedResponse) {
	w.response.ResponseID = firstNonEmpty(w.response.ResponseID, response.ResponseID)
	w.response.Model = firstNonEmpty(w.response.Model, response.Model)
	if w.response.Provider == "" {
		w.response.Provider = response.Provider
	}
	if w.response.CreatedAt.IsZero() {
		w.response.CreatedAt = response.CreatedAt
	}
	if w.response.Message.Text == "" {
		w.response.Message = response.Message
	}
	if len(w.response.Message.ToolCalls) == 0 {
		w.response.Message.ToolCalls = response.Message.ToolCalls
	}
	if w.response.Usage.TotalTokens == 0 {
		w.response.Usage = response.Usage
	}
}

func (w *ChatCompletionStreamWriter) createdUnix() int64 {
	if w.response.CreatedAt.IsZero() {
		return time.Now().Unix()
	}
	return w.response.CreatedAt.Unix()
}

func chatChunk(responseID, model string, created int64, delta map[string]any, finishReason any) map[string]any {
	return map[string]any{
		"id":      responseID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         delta,
			"finish_reason": finishReason,
		}},
	}
}

func formatOpenAIStreamToolCalls(items []domain.ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for index, item := range items {
		out = append(out, map[string]any{
			"index": index,
			"id":    item.ID,
			"type":  "function",
			"function": map[string]any{
				"name":      item.Name,
				"arguments": marshalArguments(item.Arguments),
			},
		})
	}
	return out
}

type ResponsesStreamWriter struct {
	base        *sseWriter
	response    domain.UnifiedResponse
	started     bool
	contentSent bool
	usageSeen   domain.UsageRecord
}

func NewResponsesStreamWriter(w http.ResponseWriter) (*ResponsesStreamWriter, error) {
	base, err := newSSEWriter(w)
	if err != nil {
		return nil, err
	}
	return &ResponsesStreamWriter{base: base}, nil
}

func (w *ResponsesStreamWriter) Started() bool { return w.base.Started() }

func (w *ResponsesStreamWriter) Event(event domain.StreamEvent) error {
	switch event.Type {
	case domain.StreamEventResponseStart:
		w.response.ResponseID = firstNonEmpty(event.ResponseID, w.response.ResponseID)
		w.response.Model = firstNonEmpty(event.Model, w.response.Model)
		w.response.Provider = event.Provider
		if !event.CreatedAt.IsZero() {
			w.response.CreatedAt = event.CreatedAt
		}
		if !w.started {
			w.started = true
			return w.base.writeData(map[string]any{
				"type": "response.created",
				"response": map[string]any{
					"id":     w.response.ResponseID,
					"model":  w.response.Model,
					"status": "in_progress",
				},
			})
		}
	case domain.StreamEventTextDelta:
		if !w.started {
			if err := w.Event(domain.StreamEvent{
				Type:       domain.StreamEventResponseStart,
				ResponseID: event.ResponseID,
				Provider:   event.Provider,
				Model:      event.Model,
				Role:       event.Role,
				CreatedAt:  event.CreatedAt,
			}); err != nil {
				return err
			}
		}
		w.response.Message.Text += event.Delta
		if event.Delta == "" {
			return nil
		}
		w.contentSent = true
		return w.base.writeData(map[string]any{
			"type":  "response.output_text.delta",
			"delta": event.Delta,
		})
	case domain.StreamEventUsage:
		w.usageSeen = event.Usage
		w.response.Usage = event.Usage
	}
	return nil
}

func (w *ResponsesStreamWriter) Close(response domain.UnifiedResponse) error {
	w.mergeResponse(response)
	if !w.started {
		if err := w.Event(domain.StreamEvent{
			Type:       domain.StreamEventResponseStart,
			ResponseID: w.response.ResponseID,
			Provider:   w.response.Provider,
			Model:      w.response.Model,
			Role:       w.response.Message.Role,
			CreatedAt:  w.response.CreatedAt,
		}); err != nil {
			return err
		}
		if w.response.Message.Text != "" {
			if err := w.base.writeData(map[string]any{
				"type":  "response.output_text.delta",
				"delta": w.response.Message.Text,
			}); err != nil {
				return err
			}
		}
	} else if !w.contentSent && w.response.Message.Text != "" {
		if err := w.base.writeData(map[string]any{
			"type":  "response.output_text.delta",
			"delta": w.response.Message.Text,
		}); err != nil {
			return err
		}
	}
	for index, call := range w.response.Message.ToolCalls {
		if err := w.base.writeData(map[string]any{
			"type":         "response.output_item.done",
			"output_index": index,
			"item": map[string]any{
				"id":        call.ID,
				"type":      "function_call",
				"call_id":   call.ID,
				"name":      call.Name,
				"arguments": marshalArguments(call.Arguments),
			},
		}); err != nil {
			return err
		}
	}
	return w.base.writeData(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     w.response.ResponseID,
			"model":  w.response.Model,
			"status": "completed",
			"usage": map[string]any{
				"input_tokens":  w.response.Usage.InputTokens,
				"output_tokens": w.response.Usage.OutputTokens,
				"total_tokens":  w.response.Usage.TotalTokens,
			},
		},
	})
}

func (w *ResponsesStreamWriter) Error(err error) error {
	return w.base.writeData(map[string]any{
		"type": "response.error",
		"error": map[string]any{
			"message": err.Error(),
		},
	})
}

func (w *ResponsesStreamWriter) mergeResponse(response domain.UnifiedResponse) {
	w.response.ResponseID = firstNonEmpty(w.response.ResponseID, response.ResponseID)
	w.response.Model = firstNonEmpty(w.response.Model, response.Model)
	if w.response.Provider == "" {
		w.response.Provider = response.Provider
	}
	if w.response.CreatedAt.IsZero() {
		w.response.CreatedAt = response.CreatedAt
	}
	if w.response.Message.Text == "" {
		w.response.Message = response.Message
	}
	if w.response.Usage.TotalTokens == 0 {
		w.response.Usage = response.Usage
	}
	if len(w.response.Message.ToolCalls) == 0 {
		w.response.Message.ToolCalls = response.Message.ToolCalls
	}
}

type AnthropicStreamWriter struct {
	base           *sseWriter
	response       domain.UnifiedResponse
	messageStarted bool
	contentOpened  bool
	contentSent    bool
}

func NewAnthropicStreamWriter(w http.ResponseWriter) (*AnthropicStreamWriter, error) {
	base, err := newSSEWriter(w)
	if err != nil {
		return nil, err
	}
	return &AnthropicStreamWriter{base: base}, nil
}

func (w *AnthropicStreamWriter) Started() bool { return w.base.Started() }

func (w *AnthropicStreamWriter) Event(event domain.StreamEvent) error {
	switch event.Type {
	case domain.StreamEventResponseStart:
		w.response.ResponseID = firstNonEmpty(event.ResponseID, w.response.ResponseID)
		w.response.Model = firstNonEmpty(event.Model, w.response.Model)
		w.response.Provider = event.Provider
		if !event.CreatedAt.IsZero() {
			w.response.CreatedAt = event.CreatedAt
		}
		if event.Role != "" {
			w.response.Message.Role = event.Role
		}
		return w.ensureMessageStart()
	case domain.StreamEventTextDelta:
		w.response.ResponseID = firstNonEmpty(event.ResponseID, w.response.ResponseID)
		w.response.Model = firstNonEmpty(event.Model, w.response.Model)
		if event.Role != "" {
			w.response.Message.Role = event.Role
		}
		if err := w.ensureStart(); err != nil {
			return err
		}
		w.response.Message.Text += event.Delta
		if event.Delta == "" {
			return nil
		}
		w.contentSent = true
		return w.base.writeNamedEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type": "text_delta",
				"text": event.Delta,
			},
		})
	case domain.StreamEventUsage:
		w.response.Usage = event.Usage
	}
	return nil
}

func (w *AnthropicStreamWriter) Close(response domain.UnifiedResponse) error {
	w.mergeResponse(response)
	hasText := w.response.Message.Text != ""
	if hasText || len(w.response.Message.ToolCalls) == 0 || w.contentOpened {
		if err := w.ensureStart(); err != nil {
			return err
		}
	} else if err := w.ensureMessageStart(); err != nil {
		return err
	}
	if !w.contentSent && w.response.Message.Text != "" {
		if err := w.base.writeNamedEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type": "text_delta",
				"text": w.response.Message.Text,
			},
		}); err != nil {
			return err
		}
	}
	if len(w.response.Message.ToolCalls) > 0 && w.contentOpened {
		if err := w.closeTextContentBlock(); err != nil {
			return err
		}
	}
	for index, call := range w.response.Message.ToolCalls {
		toolIndex := index + 1
		if !w.contentOpened && !w.contentSent && !hasText {
			toolIndex = index
		}
		if err := w.base.writeNamedEvent("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": toolIndex,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    call.ID,
				"name":  call.Name,
				"input": call.Arguments,
			},
		}); err != nil {
			return err
		}
		if err := w.base.writeNamedEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": toolIndex,
		}); err != nil {
			return err
		}
	}
	if w.contentOpened {
		if err := w.closeTextContentBlock(); err != nil {
			return err
		}
	}
	stopReason := "end_turn"
	if len(w.response.Message.ToolCalls) > 0 {
		stopReason = "tool_use"
	}
	if err := w.base.writeNamedEvent("message_delta", map[string]any{
		"type": "message_delta",
		"usage": map[string]any{
			"output_tokens": w.response.Usage.OutputTokens,
		},
		"delta": map[string]any{
			"stop_reason": stopReason,
		},
	}); err != nil {
		return err
	}
	return w.base.writeNamedEvent("message_stop", map[string]any{
		"type": "message_stop",
	})
}

func (w *AnthropicStreamWriter) Error(err error) error {
	return w.base.writeNamedEvent("error", map[string]any{
		"type":    "error",
		"message": err.Error(),
	})
}

func (w *AnthropicStreamWriter) ensureStart() error {
	if err := w.ensureMessageStart(); err != nil {
		return err
	}
	if !w.contentOpened {
		w.contentOpened = true
		return w.base.writeNamedEvent("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		})
	}
	return nil
}

func (w *AnthropicStreamWriter) closeTextContentBlock() error {
	if !w.contentOpened {
		return nil
	}
	w.contentOpened = false
	return w.base.writeNamedEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	})
}

func (w *AnthropicStreamWriter) ensureMessageStart() error {
	if w.messageStarted {
		return nil
	}
	w.messageStarted = true
	role := w.response.Message.Role
	if role == "" {
		role = domain.RoleAssistant
	}
	return w.base.writeNamedEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":    w.response.ResponseID,
			"model": w.response.Model,
			"role":  role,
		},
	})
}

func (w *AnthropicStreamWriter) mergeResponse(response domain.UnifiedResponse) {
	w.response.ResponseID = firstNonEmpty(w.response.ResponseID, response.ResponseID)
	w.response.Model = firstNonEmpty(w.response.Model, response.Model)
	if w.response.Provider == "" {
		w.response.Provider = response.Provider
	}
	if w.response.CreatedAt.IsZero() {
		w.response.CreatedAt = response.CreatedAt
	}
	if w.response.Message.Role == "" {
		w.response.Message.Role = response.Message.Role
	}
	if w.response.Message.Text == "" {
		w.response.Message.Text = response.Message.Text
	}
	if len(w.response.Message.ToolCalls) == 0 {
		w.response.Message.ToolCalls = response.Message.ToolCalls
	}
	if w.response.Usage.TotalTokens == 0 {
		w.response.Usage = response.Usage
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
