package conversation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

const (
	defaultMemoryPromptMaxLength = 700
	memoryPromptTextLimit        = 180
	memoryPromptValueLimit       = 96
	memoryPromptFieldLimit       = 4
	memoryPromptItemLimit        = 2
	MemoryPromptTextMetadataKey  = "memory_prompt_text"
)

type memoryPromptOptions struct {
	MaxLength  int
	TextLimit  int
	ValueLimit int
	FieldLimit int
	ItemLimit  int
}

func PrepareMessagesForStorage(messages []domain.UnifiedMessage) []domain.UnifiedMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]domain.UnifiedMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, PrepareMessageForStorage(message))
	}
	return out
}

func PrepareMessageForStorage(message domain.UnifiedMessage) domain.UnifiedMessage {
	prepared := message
	prepared.Metadata = cloneMessageMetadata(message.Metadata)
	delete(prepared.Metadata, MemoryPromptTextMetadataKey)

	options := defaultMemoryPromptOptions()
	reference := buildMemoryMessageReference(message, options)
	if shouldPersistMemoryPromptText(message) && reference != "" {
		prepared.Metadata[MemoryPromptTextMetadataKey] = reference
	}
	if len(prepared.Metadata) == 0 {
		prepared.Metadata = nil
	}

	switch {
	case prepared.Role == domain.RoleTool && len(prepared.ToolResults) > 0:
		prepared.Text = ""
		prepared.Blocks = nil
	case len(prepared.ToolCalls) > 0:
		prepared.Blocks = compactStoredBlocks(prepared.Text, prepared.Blocks)
	}
	return prepared
}

func BuildMemoryPromptInput(messages []domain.UnifiedMessage) string {
	options := defaultMemoryPromptOptions()
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Role == domain.RoleSystem {
			continue
		}
		reference := buildMemoryMessageReference(message, options)
		if strings.TrimSpace(reference) == "" {
			continue
		}
		line := fmt.Sprintf("%s: %s", message.Role, reference)
		if len(lines) > 0 && lines[len(lines)-1] == line {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return summarizeMessageEnvelope(messages)
	}
	return truncateSummary(strings.Join(lines, " | "), options.MaxLength)
}

func defaultMemoryPromptOptions() memoryPromptOptions {
	return memoryPromptOptions{
		MaxLength:  defaultMemoryPromptMaxLength,
		TextLimit:  memoryPromptTextLimit,
		ValueLimit: memoryPromptValueLimit,
		FieldLimit: memoryPromptFieldLimit,
		ItemLimit:  memoryPromptItemLimit,
	}
}

func buildMemoryMessageReference(message domain.UnifiedMessage, options memoryPromptOptions) string {
	if cached := memoryPromptTextFromMetadata(message.Metadata); cached != "" {
		return cached
	}

	parts := make([]string, 0, 3)
	if message.Role != domain.RoleTool || len(message.ToolResults) == 0 {
		if text := compactMessageText(message, options); text != "" {
			parts = append(parts, text)
		}
	}
	if len(message.ToolCalls) > 0 {
		callParts := make([]string, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			if summary := compactToolCall(call, options); summary != "" {
				callParts = append(callParts, summary)
			}
		}
		if len(callParts) > 0 {
			parts = append(parts, "tool call "+strings.Join(callParts, "; "))
		}
	}
	if len(message.ToolResults) > 0 {
		resultParts := make([]string, 0, len(message.ToolResults))
		for _, result := range message.ToolResults {
			if summary := compactStandaloneToolResult(result, options); summary != "" {
				resultParts = append(resultParts, summary)
			}
		}
		if len(resultParts) > 0 {
			parts = append(parts, "tool result "+strings.Join(resultParts, "; "))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return truncateSummary(strings.Join(parts, " | "), options.TextLimit)
}

func compactMessageText(message domain.UnifiedMessage, options memoryPromptOptions) string {
	parts := make([]string, 0, 1+len(message.Blocks))
	appendUnique := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range parts {
			if existing == value {
				return
			}
		}
		parts = append(parts, value)
	}

	appendUnique(compactTextValue(message.Text, options))
	for _, block := range message.Blocks {
		switch block.Type {
		case domain.ContentBlockText:
			appendUnique(compactTextValue(block.Text, options))
		default:
			appendUnique(compactRawBlock(block.Raw, options))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return truncateSummary(strings.Join(parts, " "), options.TextLimit)
}

func compactToolCall(call domain.ToolCall, options memoryPromptOptions) string {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = strings.TrimSpace(call.ID)
	}
	if name == "" {
		return ""
	}
	args := ""
	if object, ok := call.Arguments.(map[string]any); ok {
		args = compactFieldMap(object, []string{
			"query",
			"key",
			"value",
			"category",
			"limit",
			"top_k",
			"top_n",
			"date_from",
			"date_to",
			"time_tiers",
			"types",
		}, options)
	} else {
		args = compactAnyValue(call.Arguments, options, 0)
	}
	if args == "" {
		return name + "()"
	}
	return fmt.Sprintf("%s(%s)", name, args)
}

func compactToolResult(result domain.ToolResult, options memoryPromptOptions) string {
	summary := compactToolOutput(result.Output, options)
	if summary != "" {
		return summary
	}
	return "completed"
}

func compactStandaloneToolResult(result domain.ToolResult, options memoryPromptOptions) string {
	name := strings.TrimSpace(result.Name)
	if name == "" {
		name = strings.TrimSpace(result.ToolCallID)
	}
	summary := compactToolResult(result, options)
	if name == "" {
		return summary
	}
	return fmt.Sprintf("%s -> %s", name, summary)
}

func shouldPersistMemoryPromptText(message domain.UnifiedMessage) bool {
	return len(message.ToolCalls) > 0 || len(message.ToolResults) > 0 || hasNonTextBlocks(message.Blocks)
}

func memoryPromptTextFromMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	value, _ := metadata[MemoryPromptTextMetadataKey].(string)
	return strings.TrimSpace(value)
}

func compactStoredBlocks(text string, blocks []domain.ContentBlock) []domain.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	text = strings.TrimSpace(text)
	out := make([]domain.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != domain.ContentBlockText {
			continue
		}
		blockText := strings.TrimSpace(block.Text)
		if blockText == "" || blockText == text {
			continue
		}
		out = append(out, block)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compactToolOutput(output any, options memoryPromptOptions) string {
	object, ok := output.(map[string]any)
	if !ok {
		return compactAnyValue(output, options, 0)
	}
	if len(object) == 0 {
		return ""
	}
	parts := make([]string, 0, options.FieldLimit+1)
	appendUnique := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range parts {
			if existing == value {
				return
			}
		}
		parts = append(parts, value)
	}

	if ok, exists := boolValue(object["ok"]); exists {
		appendUnique(fmt.Sprintf("ok=%t", ok))
	}
	if status := compactAnyValue(object["status"], options, 0); status != "" {
		appendUnique("status=" + status)
	}
	if total, exists := intValue(object["total"]); exists {
		appendUnique(fmt.Sprintf("total=%d", total))
	}
	if errText := compactAnyValue(object["error"], options, 0); errText != "" {
		appendUnique("error=" + errText)
	}
	if content := compactPrimaryContent(object, options); content != "" {
		appendUnique(content)
	}
	if items := compactItems(object["items"], options); items != "" {
		appendUnique("items=" + items)
	}
	if len(parts) == 0 {
		appendUnique(compactFieldMap(object, []string{
			"content",
			"text",
			"summary",
			"message",
			"answer",
			"result",
			"value",
			"key",
			"category",
		}, options))
	}
	if len(parts) == 0 {
		appendUnique(compactAnyValue(object, options, 0))
	}
	return truncateSummary(strings.Join(parts, ", "), options.TextLimit)
}

func compactPrimaryContent(output map[string]any, options memoryPromptOptions) string {
	for _, key := range []string{"summary", "message", "answer", "result", "content", "text", "value"} {
		if value := compactAnyValue(output[key], options, 0); value != "" {
			return value
		}
	}
	return ""
}

func compactItems(value any, options memoryPromptOptions) string {
	items := anySlice(value)
	if len(items) == 0 {
		return ""
	}
	rendered := make([]string, 0, min(len(items), options.ItemLimit))
	for _, item := range items[:min(len(items), options.ItemLimit)] {
		if text := compactListItem(item, options); text != "" {
			rendered = append(rendered, text)
		}
	}
	if len(rendered) == 0 {
		return fmt.Sprintf("[%d items]", len(items))
	}
	if len(items) > len(rendered) {
		rendered = append(rendered, fmt.Sprintf("+%d more", len(items)-len(rendered)))
	}
	return "[" + strings.Join(rendered, "; ") + "]"
}

func compactListItem(value any, options memoryPromptOptions) string {
	item, ok := value.(map[string]any)
	if !ok {
		return compactAnyValue(value, options, 1)
	}
	if key := compactAnyValue(item["key"], options, 1); key != "" {
		if val := compactAnyValue(item["value"], options, 1); val != "" {
			return fmt.Sprintf("%s=%s", key, val)
		}
	}
	if kind := compactAnyValue(item["type"], options, 1); kind != "" {
		if content := compactAnyValue(item["content"], options, 1); content != "" {
			return fmt.Sprintf("%s:%s", kind, content)
		}
	}
	return compactFieldMap(item, []string{
		"type",
		"content",
		"key",
		"value",
		"title",
		"category",
		"score",
		"effective_score",
	}, options)
}

func compactFieldMap(value map[string]any, preferred []string, options memoryPromptOptions) string {
	if len(value) == 0 {
		return ""
	}
	selected := make([]string, 0, options.FieldLimit)
	used := map[string]struct{}{}
	appendField := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		if len(selected) >= options.FieldLimit {
			return
		}
		if _, ok := used[key]; ok {
			return
		}
		item, exists := value[key]
		if !exists || item == nil {
			return
		}
		rendered := compactAnyValue(item, options, 1)
		if rendered == "" {
			return
		}
		selected = append(selected, fmt.Sprintf("%s=%s", key, rendered))
		used[key] = struct{}{}
	}

	for _, key := range preferred {
		appendField(key)
	}
	keys := sortedKeys(value)
	for _, key := range keys {
		appendField(key)
	}

	if len(selected) == 0 {
		return ""
	}
	if extra := len(value) - len(selected); extra > 0 {
		selected = append(selected, fmt.Sprintf("+%d keys", extra))
	}
	return strings.Join(selected, ", ")
}

func compactAnyValue(value any, options memoryPromptOptions, depth int) string {
	if value == nil {
		return ""
	}
	if depth > 2 {
		return truncateSummary(strings.Join(strings.Fields(fmt.Sprint(value)), " "), options.ValueLimit)
	}
	switch typed := value.(type) {
	case string:
		return compactTextValue(typed, options)
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case time.Time:
		if typed.IsZero() {
			return ""
		}
		return typed.UTC().Format(time.RFC3339)
	case []string:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return compactItems(items, options)
	case []map[string]any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return compactItems(items, options)
	case []any:
		return compactItems(typed, options)
	case map[string]any:
		return "{" + compactFieldMap(typed, nil, options) + "}"
	default:
		raw, err := json.Marshal(typed)
		if err == nil {
			return compactRawBlock(string(raw), options)
		}
		return truncateSummary(strings.Join(strings.Fields(fmt.Sprint(typed)), " "), options.ValueLimit)
	}
}

func compactTextValue(value string, options memoryPromptOptions) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	if decoded := compactJSONText(value, options); decoded != "" {
		return decoded
	}
	return truncateMiddle(value, min(options.TextLimit, options.ValueLimit))
}

func compactRawBlock(raw string, options memoryPromptOptions) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if decoded := compactJSONText(raw, options); decoded != "" {
		return decoded
	}
	return truncateMiddle(strings.Join(strings.Fields(raw), " "), options.ValueLimit)
}

func compactJSONText(value string, options memoryPromptOptions) string {
	if !looksLikeJSON(value) {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return ""
	}
	return compactAnyValue(parsed, options, 1)
}

func looksLikeJSON(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	return (strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")) ||
		(strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]"))
}

func anySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	case []string:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func hasNonTextBlocks(blocks []domain.ContentBlock) bool {
	for _, block := range blocks {
		if block.Type != domain.ContentBlockText && strings.TrimSpace(firstNonEmptyString(block.Raw, block.Text)) != "" {
			return true
		}
	}
	return false
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case uint:
		return int(typed), true
	case uint8:
		return int(typed), true
	case uint16:
		return int(typed), true
	case uint32:
		return int(typed), true
	case uint64:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneMessageMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func truncateMiddle(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 8 {
		return truncateSummary(value, limit)
	}
	edge := (limit - 3) / 2
	if edge <= 0 {
		return truncateSummary(value, limit)
	}
	return value[:edge] + "..." + value[len(value)-edge:]
}
