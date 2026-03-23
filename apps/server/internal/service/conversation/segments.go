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
	Sequence int
	Role     domain.MessageRole
	Text     string
}

func DefaultSegmentOptions() SegmentOptions {
	return SegmentOptions{
		MaxMessages:   defaultSegmentMaxMessages,
		Overlap:       defaultSegmentOverlap,
		MaxCharacters: defaultSegmentMaxCharacters,
	}
}

func BuildSegments(userID, conversationID string, messages []domain.UnifiedMessage, now time.Time, options SegmentOptions) []domain.ConversationSegment {
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

	dialogue := collectDialogueMessages(messages)
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

func collectDialogueMessages(messages []domain.UnifiedMessage) []dialogueMessage {
	dialogue := make([]dialogueMessage, 0, len(messages))
	for idx, message := range messages {
		if message.Role != domain.RoleUser && message.Role != domain.RoleAssistant {
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
