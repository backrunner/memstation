package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
)

type HeaderOptions struct {
	ConversationID   string
	Mode             domain.Mode
	Provider         domain.Provider
	EndConversation  bool
	InjectContext    bool
	InjectTools      bool
	ToolWriteEnabled bool
	MemoryPersist    bool
	SourceRetain     *bool
}

func ParseHeaderOptions(r *http.Request) HeaderOptions {
	memoryPersist := parseBooleanHeader(r.Header.Get("X-MemStation-Memory-Persist"), true)
	return HeaderOptions{
		ConversationID:   r.Header.Get("X-MemStation-Session"),
		Mode:             parseMode(r.Header.Get("X-MemStation-Mode")),
		Provider:         domain.Provider(strings.ToLower(r.Header.Get("X-MemStation-Provider"))),
		EndConversation:  strings.EqualFold(r.Header.Get("X-MemStation-End-Conversation"), "true"),
		InjectContext:    strings.EqualFold(r.Header.Get("X-MemStation-Inject-Context"), "true"),
		InjectTools:      strings.EqualFold(r.Header.Get("X-MemStation-Inject-Tools"), "true"),
		ToolWriteEnabled: memoryPersist && strings.EqualFold(r.Header.Get("X-MemStation-Tool-Write"), "true"),
		MemoryPersist:    memoryPersist,
		SourceRetain:     parseOptionalBooleanHeader(r.Header.Get("X-MemStation-Source-Retain")),
	}
}

func parseMode(value string) domain.Mode {
	if strings.EqualFold(value, string(domain.ModeInject)) {
		return domain.ModeInject
	}
	return domain.ModeTransparent
}

func toBlocks(raw json.RawMessage) []domain.ContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []domain.ContentBlock{{Type: domain.ContentBlockText, Text: text}}
	}
	var array []any
	if err := json.Unmarshal(raw, &array); err == nil {
		return blocksFromArray(array)
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err == nil {
		return []domain.ContentBlock{blockFromMap(item)}
	}
	return []domain.ContentBlock{{Type: domain.ContentBlockJSON, Raw: string(raw)}}
}

func textFromBlocks(raw json.RawMessage) string {
	blocks := toBlocks(raw)
	if len(blocks) == 0 {
		return ""
	}
	return textFromCanonicalBlocks(blocks)
}

func buildRequest(protocol domain.Protocol, model string, stream bool, headers HeaderOptions, messages []domain.UnifiedMessage, tools []domain.ToolDefinition) domain.UnifiedRequest {
	conversationID := strings.TrimSpace(headers.ConversationID)
	if conversationID == "" {
		conversationID = uuid.NewString()
	}
	return domain.UnifiedRequest{
		RequestID:        uuid.NewString(),
		ConversationID:   conversationID,
		SessionID:        conversationID,
		Protocol:         protocol,
		Provider:         headers.Provider,
		Mode:             headers.Mode,
		Model:            model,
		Stream:           stream,
		EndConversation:  headers.EndConversation,
		InjectContext:    headers.InjectContext,
		InjectTools:      headers.InjectTools,
		ToolWriteEnabled: headers.ToolWriteEnabled,
		MemoryPersist:    boolPtr(headers.MemoryPersist),
		SourceRetain:     headers.SourceRetain,
		Messages:         messages,
		Tools:            tools,
		ReceivedAt:       time.Now().UTC(),
	}
}

func parseBooleanHeader(value string, fallback bool) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return fallback
	}
	switch trimmed {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func parseOptionalBooleanHeader(value string) *bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return nil
	}
	parsed := parseBooleanHeader(trimmed, false)
	return &parsed
}

func blocksFromArray(items []any) []domain.ContentBlock {
	out := make([]domain.ContentBlock, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			out = append(out, domain.ContentBlock{Type: domain.ContentBlockText, Text: typed})
		case map[string]any:
			out = append(out, blockFromMap(typed))
		default:
			raw, _ := json.Marshal(typed)
			out = append(out, domain.ContentBlock{Type: domain.ContentBlockJSON, Raw: string(raw)})
		}
	}
	return out
}

func blockFromMap(item map[string]any) domain.ContentBlock {
	blockType := strings.TrimSpace(strings.ToLower(anyString(item["type"])))
	switch blockType {
	case "", "text", "input_text", "output_text":
		return domain.ContentBlock{
			Type:     domain.ContentBlockText,
			Text:     firstNonEmpty(anyString(item["text"]), anyString(item["output_text"])),
			Metadata: metadataWithout(item, "type", "text", "output_text"),
		}
	case "image", "image_url", "input_image":
		sourceURL, providerFileID, mimeType, metadata := imageSourceFields(item)
		return domain.ContentBlock{
			Type:           domain.ContentBlockImage,
			MIMEType:       mimeType,
			BlobID:         anyString(item["blob_id"]),
			SourceURL:      sourceURL,
			ProviderFileID: providerFileID,
			Filename:       firstNonEmpty(anyString(item["filename"]), anyString(metadata["filename"]), provider.NativeSourceFilename(item)),
			Text:           firstNonEmpty(anyString(item["text"]), anyString(metadata["caption"]), anyString(metadata["ocr_text"])),
			Raw:            rawJSONText(item),
			Metadata:       metadata,
		}
	case "audio", "input_audio":
		sourceURL, providerFileID, mimeType, metadata := mediaSourceFields(item, "audio")
		return domain.ContentBlock{
			Type:           domain.ContentBlockAudio,
			MIMEType:       mimeType,
			BlobID:         anyString(item["blob_id"]),
			SourceURL:      sourceURL,
			ProviderFileID: providerFileID,
			Filename:       firstNonEmpty(anyString(item["filename"]), anyString(metadata["filename"]), provider.NativeSourceFilename(item)),
			Text:           firstNonEmpty(anyString(item["text"]), anyString(metadata["transcript"]), anyString(metadata["summary"])),
			Raw:            rawJSONText(item),
			StartMS:        anyInt64(item["start_ms"]),
			EndMS:          anyInt64(item["end_ms"]),
			DurationMS:     anyInt64(item["duration_ms"]),
			Metadata:       metadata,
		}
	case "video", "input_video":
		sourceURL, providerFileID, mimeType, metadata := mediaSourceFields(item, "video")
		return domain.ContentBlock{
			Type:           domain.ContentBlockVideo,
			MIMEType:       mimeType,
			BlobID:         anyString(item["blob_id"]),
			SourceURL:      sourceURL,
			ProviderFileID: providerFileID,
			Filename:       firstNonEmpty(anyString(item["filename"]), anyString(metadata["filename"]), provider.NativeSourceFilename(item)),
			Text:           firstNonEmpty(anyString(item["text"]), anyString(metadata["transcript"]), anyString(metadata["summary"])),
			Raw:            rawJSONText(item),
			StartMS:        anyInt64(item["start_ms"]),
			EndMS:          anyInt64(item["end_ms"]),
			DurationMS:     anyInt64(item["duration_ms"]),
			Metadata:       metadata,
		}
	case "file", "input_file", "document":
		sourceURL, providerFileID, mimeType, metadata := mediaSourceFields(item, "file")
		return domain.ContentBlock{
			Type:           domain.ContentBlockFile,
			MIMEType:       mimeType,
			BlobID:         anyString(item["blob_id"]),
			SourceURL:      sourceURL,
			ProviderFileID: providerFileID,
			Filename:       firstNonEmpty(anyString(item["filename"]), anyString(metadata["filename"]), provider.NativeSourceFilename(item)),
			Text:           firstNonEmpty(anyString(item["text"]), anyString(metadata["document_text"]), anyString(metadata["parsed_text"])),
			Raw:            rawJSONText(item),
			PageIndex:      anyInt(item["page_index"]),
			StartMS:        anyInt64(item["start_ms"]),
			EndMS:          anyInt64(item["end_ms"]),
			Metadata:       metadata,
		}
	case "tool_use":
		return domain.ContentBlock{
			Type:     domain.ContentBlockToolUse,
			Text:     anyString(item["name"]),
			Raw:      rawJSONText(item["input"]),
			Metadata: metadataWithout(item, "type", "name", "input"),
		}
	case "tool_result":
		return domain.ContentBlock{
			Type:     domain.ContentBlockToolResult,
			Text:     contentText(item["content"]),
			Raw:      rawJSONText(item),
			Metadata: metadataWithout(item, "type", "content"),
		}
	default:
		return domain.ContentBlock{
			Type:     domain.ContentBlockUnknown,
			Text:     anyString(item["text"]),
			Raw:      rawJSONText(item),
			Metadata: metadataWithout(item, "type", "text"),
		}
	}
}

func textFromCanonicalBlocks(blocks []domain.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.TextProxy()); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func metadataWithout(item map[string]any, keys ...string) map[string]any {
	if len(item) == 0 {
		return nil
	}
	excluded := map[string]struct{}{}
	for _, key := range keys {
		excluded[key] = struct{}{}
	}
	out := map[string]any{}
	for key, value := range item {
		if _, ok := excluded[key]; ok {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func imageSourceFields(item map[string]any) (string, string, string, map[string]any) {
	if u, id, mimeType, ok := provider.NativeSourceFields(item, "image"); ok {
		return u, id, mimeType, nil
	}
	metadata := metadataWithout(item, "type", "text", "image_url", "file_id", "mime_type", "detail")
	if raw := item["image_url"]; raw != nil {
		switch typed := raw.(type) {
		case string:
			return typed, anyString(item["file_id"]), anyString(item["mime_type"]), metadata
		case map[string]any:
			metadata = mergeMetadataMaps(metadata, metadataWithout(typed))
			return firstNonEmpty(anyString(typed["url"]), anyString(typed["image_url"])), firstNonEmpty(anyString(item["file_id"]), anyString(typed["file_id"])), firstNonEmpty(anyString(item["mime_type"]), anyString(typed["mime_type"])), metadata
		}
	}
	return anyString(item["source_url"]), anyString(item["file_id"]), anyString(item["mime_type"]), metadata
}

func mediaSourceFields(item map[string]any, key string) (string, string, string, map[string]any) {
	if u, id, mimeType, ok := provider.NativeSourceFields(item, key); ok {
		return u, id, mimeType, nil
	}
	metadata := metadataWithout(item, "type", "text", key, "mime_type", "file_id", "source_url")
	if raw := item[key]; raw != nil {
		switch typed := raw.(type) {
		case string:
			return typed, anyString(item["file_id"]), anyString(item["mime_type"]), metadata
		case map[string]any:
			metadata = mergeMetadataMaps(metadata, metadataWithout(typed))
			return firstNonEmpty(anyString(typed["url"]), anyString(typed["source_url"]), anyString(item["source_url"])), firstNonEmpty(anyString(typed["file_id"]), anyString(item["file_id"])), firstNonEmpty(anyString(typed["mime_type"]), anyString(item["mime_type"])), metadata
		}
	}
	return anyString(item["source_url"]), anyString(item["file_id"]), anyString(item["mime_type"]), metadata
}

func rawJSONText(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}

func anyInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func anyInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

func mergeMetadataMaps(values ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, value := range values {
		for key, item := range value {
			out[key] = item
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func chatCompletionContent(message domain.UnifiedMessage) any {
	blocks := canonicalMessageBlocks(message)
	if len(blocks) == 0 {
		return message.Text
	}
	if len(blocks) == 1 && blocks[0].Type == domain.ContentBlockText {
		return blocks[0].Text
	}
	items := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		item := openAIBlock(block, false)
		if len(item) == 0 {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return textFromCanonicalBlocks(blocks)
	}
	return items
}

func responsesMessageContent(message domain.UnifiedMessage, output bool) []map[string]any {
	blocks := canonicalMessageBlocks(message)
	if len(blocks) == 0 {
		if strings.TrimSpace(message.Text) == "" {
			return nil
		}
		blocks = []domain.ContentBlock{{Type: domain.ContentBlockText, Text: message.Text}}
	}
	items := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		item := openAIBlock(block, output)
		if len(item) == 0 {
			continue
		}
		items = append(items, item)
	}
	return items
}

func anthropicMessageContent(message domain.UnifiedMessage) []map[string]any {
	blocks := canonicalMessageBlocks(message)
	if len(blocks) == 0 {
		if strings.TrimSpace(message.Text) == "" {
			return []map[string]any{{"type": "text", "text": "Continue."}}
		}
		blocks = []domain.ContentBlock{{Type: domain.ContentBlockText, Text: message.Text}}
	}
	items := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		item := anthropicBlock(block)
		if len(item) == 0 {
			continue
		}
		items = append(items, item)
	}
	return items
}

func canonicalMessageBlocks(message domain.UnifiedMessage) []domain.ContentBlock {
	if len(message.Blocks) > 0 {
		return message.Blocks
	}
	if strings.TrimSpace(message.Text) != "" {
		return []domain.ContentBlock{{Type: domain.ContentBlockText, Text: message.Text}}
	}
	return nil
}

func openAIBlock(block domain.ContentBlock, output bool) map[string]any {
	switch block.Type {
	case domain.ContentBlockText:
		blockType := "input_text"
		if output {
			blockType = "output_text"
		}
		return map[string]any{"type": blockType, "text": block.TextProxy()}
	case domain.ContentBlockImage:
		item := map[string]any{"type": "input_image"}
		if block.SourceURL != "" {
			item["image_url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			item["file_id"] = block.ProviderFileID
		}
		if block.BlobID != "" {
			item["blob_id"] = block.BlobID
		}
		if block.MIMEType != "" {
			item["mime_type"] = block.MIMEType
		}
		if block.Filename != "" {
			item["filename"] = block.Filename
		}
		return item
	case domain.ContentBlockAudio:
		item := map[string]any{"type": "input_audio"}
		audio := map[string]any{}
		if block.SourceURL != "" {
			audio["url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			audio["file_id"] = block.ProviderFileID
		}
		if block.MIMEType != "" {
			audio["mime_type"] = block.MIMEType
		}
		if len(audio) > 0 {
			item["audio"] = audio
		}
		if block.BlobID != "" {
			item["blob_id"] = block.BlobID
		}
		if block.StartMS > 0 {
			item["start_ms"] = block.StartMS
		}
		if block.EndMS > 0 {
			item["end_ms"] = block.EndMS
		}
		if block.DurationMS > 0 {
			item["duration_ms"] = block.DurationMS
		}
		return item
	case domain.ContentBlockVideo:
		item := map[string]any{"type": "input_video"}
		video := map[string]any{}
		if block.SourceURL != "" {
			video["url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			video["file_id"] = block.ProviderFileID
		}
		if block.MIMEType != "" {
			video["mime_type"] = block.MIMEType
		}
		if len(video) > 0 {
			item["video"] = video
		}
		if block.BlobID != "" {
			item["blob_id"] = block.BlobID
		}
		if block.StartMS > 0 {
			item["start_ms"] = block.StartMS
		}
		if block.EndMS > 0 {
			item["end_ms"] = block.EndMS
		}
		if block.DurationMS > 0 {
			item["duration_ms"] = block.DurationMS
		}
		return item
	case domain.ContentBlockFile:
		item := map[string]any{"type": "file"}
		if block.SourceURL != "" {
			item["source_url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			item["file_id"] = block.ProviderFileID
		}
		if block.BlobID != "" {
			item["blob_id"] = block.BlobID
		}
		if block.Filename != "" {
			item["filename"] = block.Filename
		}
		if block.MIMEType != "" {
			item["mime_type"] = block.MIMEType
		}
		if block.PageIndex > 0 {
			item["page_index"] = block.PageIndex
		}
		if block.StartMS > 0 {
			item["start_ms"] = block.StartMS
		}
		if block.EndMS > 0 {
			item["end_ms"] = block.EndMS
		}
		return item
	default:
		if text := block.TextProxy(); text != "" {
			blockType := "input_text"
			if output {
				blockType = "output_text"
			}
			return map[string]any{"type": blockType, "text": text}
		}
		return nil
	}
}

func anthropicBlock(block domain.ContentBlock) map[string]any {
	switch block.Type {
	case domain.ContentBlockText:
		return map[string]any{"type": "text", "text": block.TextProxy()}
	case domain.ContentBlockImage:
		item := map[string]any{"type": "image"}
		if block.SourceURL != "" {
			item["source_url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			item["file_id"] = block.ProviderFileID
		}
		if block.MIMEType != "" {
			item["mime_type"] = block.MIMEType
		}
		if block.BlobID != "" {
			item["blob_id"] = block.BlobID
		}
		return item
	case domain.ContentBlockFile:
		item := map[string]any{"type": "document"}
		if block.SourceURL != "" {
			item["source_url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			item["file_id"] = block.ProviderFileID
		}
		if block.MIMEType != "" {
			item["mime_type"] = block.MIMEType
		}
		if block.Filename != "" {
			item["filename"] = block.Filename
		}
		if block.PageIndex > 0 {
			item["page_index"] = block.PageIndex
		}
		return item
	case domain.ContentBlockAudio:
		item := map[string]any{"type": "audio"}
		if block.SourceURL != "" {
			item["source_url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			item["file_id"] = block.ProviderFileID
		}
		if block.MIMEType != "" {
			item["mime_type"] = block.MIMEType
		}
		return item
	case domain.ContentBlockVideo:
		item := map[string]any{"type": "video"}
		if block.SourceURL != "" {
			item["source_url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			item["file_id"] = block.ProviderFileID
		}
		if block.MIMEType != "" {
			item["mime_type"] = block.MIMEType
		}
		if block.StartMS > 0 {
			item["start_ms"] = block.StartMS
		}
		if block.EndMS > 0 {
			item["end_ms"] = block.EndMS
		}
		return item
	default:
		if text := block.TextProxy(); text != "" {
			return map[string]any{"type": "text", "text": text}
		}
		return nil
	}
}
