package conversation

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

const (
	defaultSegmentMaxMessages   = 6
	defaultSegmentOverlap       = 2
	defaultSegmentMaxCharacters = 1200
)

type SegmentOptions struct {
	MaxMessages   int
	Overlap       int
	MaxCharacters int
}

type dialogueMessage struct {
	Sequence      int
	Role          domain.MessageRole
	Text          string
	PartIDs       []string
	ArtifactIDs   []string
	Modalities    []string
	HasSourceBlob bool
	PageIndexes   []int
	StartMS       int64
	EndMS         int64
}

func DefaultSegmentOptions() SegmentOptions {
	return SegmentOptions{
		MaxMessages:   defaultSegmentMaxMessages,
		Overlap:       defaultSegmentOverlap,
		MaxCharacters: defaultSegmentMaxCharacters,
	}
}

func BuildSegments(userID, conversationID string, messages []domain.UnifiedMessage, now time.Time, options SegmentOptions) []domain.ConversationSegment {
	return BuildSegmentsFromTimeline(userID, conversationID, messages, nil, nil, now, options)
}

func BuildSegmentsFromTimeline(userID, conversationID string, messages []domain.UnifiedMessage, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact, now time.Time, options SegmentOptions) []domain.ConversationSegment {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if options.MaxMessages <= 0 {
		options.MaxMessages = defaultSegmentMaxMessages
	}
	if options.Overlap < 0 {
		options.Overlap = 0
	}
	if options.MaxCharacters <= 0 {
		options.MaxCharacters = defaultSegmentMaxCharacters
	}

	dialogue := collectDialogueMessages(messages, parts, artifacts)
	if len(dialogue) == 0 {
		return nil
	}

	segments := make([]domain.ConversationSegment, 0, max(1, len(dialogue)/max(1, options.MaxMessages)))
	start := 0
	for segmentIndex := 0; start < len(dialogue); segmentIndex++ {
		end := segmentEnd(dialogue, start, options)
		if end <= start {
			end = start + 1
		}
		items := dialogue[start:end]
		content := renderSegmentContent(items)
		hash := segmentContentHash(content)
		overlapCount := 0
		overlapFrom := 0
		if segmentIndex > 0 {
			overlapCount = min(options.Overlap, len(items)-1)
			if overlapCount > 0 {
				overlapFrom = items[0].Sequence
			}
		}
		segments = append(segments, domain.ConversationSegment{
			ID:                  segmentID(conversationID, segmentIndex, hash),
			UserID:              userID,
			ConversationID:      conversationID,
			SegmentIndex:        segmentIndex,
			StartSequence:       items[0].Sequence,
			EndSequence:         items[len(items)-1].Sequence,
			MessageCount:        len(items),
			OverlapFromSequence: overlapFrom,
			OverlapMessageCount: overlapCount,
			Content:             content,
			ContentHash:         hash,
			Metadata: map[string]any{
				"strategy":             "dialogue_window_v1",
				"max_messages":         options.MaxMessages,
				"max_characters":       options.MaxCharacters,
				"overlap_messages":     options.Overlap,
				"start_role":           items[0].Role,
				"end_role":             items[len(items)-1].Role,
				"excluded_roles":       []string{string(domain.RoleSystem), string(domain.RoleTool)},
				"sequence_span":        items[len(items)-1].Sequence - items[0].Sequence + 1,
				"dialogue_message_ids": nil,
				"source_part_ids":      collectSegmentPartIDs(items),
				"source_artifact_ids":  collectSegmentArtifactIDs(items),
				"modalities":           collectSegmentModalities(items),
				"evidence_count":       len(collectSegmentArtifactIDs(items)),
				"source_part_count":    len(collectSegmentPartIDs(items)),
				"has_source_blob":      segmentHasSourceBlob(items),
				"page_indexes":         collectSegmentPageIndexes(items),
				"start_ms":             segmentStartMS(items),
				"end_ms":               segmentEndMS(items),
			},
			CreatedAt: now,
			UpdatedAt: now,
		})
		if end >= len(dialogue) {
			break
		}
		overlap := min(options.Overlap, max(0, end-start-1))
		start = end - overlap
	}
	return segments
}

func SummarizeSegments(segments []domain.ConversationSegment) string {
	if len(segments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		text := strings.TrimSpace(segment.Content)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return truncateSummary(strings.Join(parts, " || "), 400)
}

func SummarizeMessages(messages []domain.UnifiedMessage) string {
	return SummarizeMessagesWithTimeline(messages, nil, nil)
}

func SummarizeMessagesWithTimeline(messages []domain.UnifiedMessage, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact) string {
	timeline := collectDialogueMessages(messages, parts, artifacts)
	if len(timeline) > 0 {
		lines := make([]string, 0, len(timeline))
		for _, item := range timeline {
			lines = append(lines, fmt.Sprintf("%s: %s", item.Role, item.Text))
		}
		return truncateSummary(strings.Join(lines, " | "), 400)
	}
	summaryParts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Role != domain.RoleUser && message.Role != domain.RoleAssistant && message.Role != domain.RoleTool {
			continue
		}
		contentParts := summarizedMessageParts(message)
		if len(contentParts) == 0 {
			continue
		}
		summaryParts = append(summaryParts, fmt.Sprintf("%s: %s", message.Role, strings.Join(contentParts, " | ")))
	}
	if len(summaryParts) > 0 {
		return truncateSummary(strings.Join(summaryParts, " | "), 400)
	}
	return summarizeMessageEnvelope(messages)
}

func collectDialogueMessages(messages []domain.UnifiedMessage, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact) []dialogueMessage {
	if len(parts) > 0 {
		dialogue := collectDialogueFromParts(parts, artifacts)
		if len(dialogue) > 0 {
			return dialogue
		}
	}
	dialogue := make([]dialogueMessage, 0, len(messages))
	for idx, message := range messages {
		if !timelineRole(message.Role) {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" && len(message.Blocks) > 0 {
			text = strings.TrimSpace(message.Blocks[0].Text)
		}
		if text == "" {
			continue
		}
		dialogue = append(dialogue, dialogueMessage{
			Sequence: idx,
			Role:     message.Role,
			Text:     text,
		})
	}
	return dialogue
}

func collectDialogueFromParts(parts []domain.MessagePart, artifacts []domain.MessagePartArtifact) []dialogueMessage {
	if len(parts) == 0 {
		return nil
	}
	artifactIndex := make(map[string][]domain.MessagePartArtifact, len(parts))
	for _, artifact := range artifacts {
		artifactIndex[artifact.PartID] = append(artifactIndex[artifact.PartID], artifact)
	}
	dialogue := make([]dialogueMessage, 0, len(parts))
	for _, part := range parts {
		if !timelineRole(part.Role) {
			continue
		}
		textParts := make([]string, 0, 1+len(artifactIndex[part.PartID]))
		artifactIDs := make([]string, 0, len(artifactIndex[part.PartID]))
		for _, artifact := range artifactIndex[part.PartID] {
			if text := strings.TrimSpace(artifact.TextContent); text != "" {
				textParts = append(textParts, text)
				artifactIDs = append(artifactIDs, artifact.ArtifactID)
			}
		}
		if len(textParts) == 0 {
			if text := strings.TrimSpace(firstNonEmptyString(part.TextContent, part.RawPayload)); text != "" {
				textParts = append(textParts, text)
			}
		}
		if len(textParts) == 0 {
			continue
		}
		dialogue = append(dialogue, dialogueMessage{
			Sequence:      part.SequenceNum,
			Role:          part.Role,
			Text:          truncateSummary(strings.Join(textParts, " | "), 400),
			PartIDs:       []string{part.PartID},
			ArtifactIDs:   artifactIDs,
			Modalities:    compactStrings(partModality(part)),
			HasSourceBlob: strings.TrimSpace(part.BlobID) != "",
			PageIndexes:   compactInts(part.PageIndex),
			StartMS:       part.StartMS,
			EndMS:         maxInt64(part.EndMS, part.StartMS+part.DurationMS),
		})
	}
	return dialogue
}

func timelineRole(role domain.MessageRole) bool {
	return role == domain.RoleUser || role == domain.RoleAssistant || role == domain.RoleTool
}

func segmentEnd(messages []dialogueMessage, start int, options SegmentOptions) int {
	end := start
	totalChars := 0
	for end < len(messages) && end-start < options.MaxMessages {
		rendered := fmt.Sprintf("%s: %s", messages[end].Role, messages[end].Text)
		nextChars := len(rendered)
		if end > start {
			nextChars += 3
		}
		if totalChars > 0 && totalChars+nextChars > options.MaxCharacters {
			break
		}
		totalChars += nextChars
		end++
	}
	return end
}

func renderSegmentContent(messages []dialogueMessage) string {
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		lines = append(lines, fmt.Sprintf("%s: %s", message.Role, message.Text))
	}
	return strings.Join(lines, " | ")
}

func collectSegmentPartIDs(items []dialogueMessage) []string {
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range items {
		for _, id := range item.PartIDs {
			if _, ok := seen[id]; ok || strings.TrimSpace(id) == "" {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func collectSegmentArtifactIDs(items []dialogueMessage) []string {
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range items {
		for _, id := range item.ArtifactIDs {
			if _, ok := seen[id]; ok || strings.TrimSpace(id) == "" {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func collectSegmentModalities(items []dialogueMessage) []string {
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range items {
		for _, modality := range item.Modalities {
			if modality == "" {
				continue
			}
			if _, ok := seen[modality]; ok {
				continue
			}
			seen[modality] = struct{}{}
			out = append(out, modality)
		}
	}
	return out
}

func collectSegmentPageIndexes(items []dialogueMessage) []int {
	out := make([]int, 0)
	seen := map[int]struct{}{}
	for _, item := range items {
		for _, pageIndex := range item.PageIndexes {
			if _, ok := seen[pageIndex]; ok {
				continue
			}
			seen[pageIndex] = struct{}{}
			out = append(out, pageIndex)
		}
	}
	return out
}

func segmentHasSourceBlob(items []dialogueMessage) bool {
	for _, item := range items {
		if item.HasSourceBlob {
			return true
		}
	}
	return false
}

func segmentStartMS(items []dialogueMessage) int64 {
	var value int64
	for idx, item := range items {
		if idx == 0 || (item.StartMS > 0 && item.StartMS < value) {
			value = item.StartMS
		}
	}
	return value
}

func segmentEndMS(items []dialogueMessage) int64 {
	var value int64
	for _, item := range items {
		if item.EndMS > value {
			value = item.EndMS
		}
	}
	return value
}

func summarizedMessageParts(message domain.UnifiedMessage) []string {
	parts := make([]string, 0, 1+len(message.Blocks)+len(message.ToolCalls)+len(message.ToolResults))
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

	if digest := memoryPromptTextFromMetadata(message.Metadata); digest != "" {
		appendUnique(digest)
		if len(message.ToolCalls) > 0 || len(message.ToolResults) > 0 || hasNonTextBlocks(message.Blocks) {
			return parts
		}
	}

	appendUnique(message.Text)
	for _, block := range message.Blocks {
		appendUnique(firstNonEmptyString(block.Text, block.Raw))
	}
	for _, call := range message.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		appendUnique("tool call: " + name)
	}
	for _, result := range message.ToolResults {
		name := strings.TrimSpace(result.Name)
		if name == "" {
			name = strings.TrimSpace(result.ToolCallID)
		}
		if name == "" {
			continue
		}
		appendUnique("tool result: " + name)
	}
	return parts
}

func summarizeMessageEnvelope(messages []domain.UnifiedMessage) string {
	if len(messages) == 0 {
		return "Conversation captured no persisted messages."
	}
	userCount := 0
	assistantCount := 0
	toolCount := 0
	toolCalls := 0
	toolResults := 0
	for _, message := range messages {
		switch message.Role {
		case domain.RoleUser:
			userCount++
		case domain.RoleAssistant:
			assistantCount++
		case domain.RoleTool:
			toolCount++
		}
		toolCalls += len(message.ToolCalls)
		toolResults += len(message.ToolResults)
	}
	parts := make([]string, 0, 5)
	if userCount > 0 {
		parts = append(parts, pluralizeCount(userCount, "user message"))
	}
	if assistantCount > 0 {
		parts = append(parts, pluralizeCount(assistantCount, "assistant message"))
	}
	if toolCount > 0 {
		parts = append(parts, pluralizeCount(toolCount, "tool message"))
	}
	if toolCalls > 0 {
		parts = append(parts, pluralizeCount(toolCalls, "tool call"))
	}
	if toolResults > 0 {
		parts = append(parts, pluralizeCount(toolResults, "tool result"))
	}
	if len(parts) == 0 {
		return pluralizeCount(len(messages), "message") + " persisted without user-visible text."
	}
	return "Conversation captured " + strings.Join(parts, ", ") + "."
}

func truncateSummary(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func segmentContentHash(content string) string {
	sum := sha1.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

func segmentID(conversationID string, index int, _ string) string {
	return fmt.Sprintf("segment:%s:%03d", conversationID, index)
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func compactInts(values ...int) []int {
	out := make([]int, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func partModality(part domain.MessagePart) string {
	switch part.PartType {
	case domain.ContentBlockImage, domain.ContentBlockAudio, domain.ContentBlockVideo, domain.ContentBlockFile:
		return string(part.PartType)
	default:
		return "text"
	}
}

func pluralizeCount(count int, label string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", label)
	}
	return fmt.Sprintf("%d %ss", count, label)
}
