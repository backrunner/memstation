package conversation

import (
	"crypto/sha256"
	"encoding/hex"
	"unicode"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/textutil"
)

// Splitting is lossless transport budgeting, not semantic memory generation.
func splitDialogueSources(sources []dialogueMessage, maxBytes int) []dialogueMessage {
	var out []dialogueMessage
	for _, source := range sources {
		capacity := maxBytes - len(source.Role) - len(": ")
		hash := sha256.Sum256([]byte(source.Text))
		for start := 0; start < len(source.Text); {
			length := len(textutil.PrefixBytes(source.Text[start:], capacity))
			// Defensive progress for malformed UTF-8 supplied by in-process callers.
			// Valid JSON text never needs this branch; do not loop on continuation bytes.
			if length == 0 {
				length = 1
			}
			// Prefer a nearby whitespace boundary without discarding the whitespace.
			if start+length < len(source.Text) {
				boundary := 0
				for offset, r := range source.Text[start : start+length] {
					if unicode.IsSpace(r) && offset >= length*3/4 {
						boundary = offset + utf8.RuneLen(r)
					}
				}
				if boundary > 0 {
					length = boundary
				}
			}
			item := source
			item.Text = source.Text[start : start+length]
			item.Source.SourceSHA256 = hex.EncodeToString(hash[:])
			item.Source.ByteStart, item.Source.ByteEnd = start, start+length
			out = append(out, item)
			start += length
		}
	}
	return out
}

func countDialogueMessages(items []dialogueMessage) int {
	seen := map[int]bool{}
	for _, item := range items {
		seen[item.Sequence] = true
	}
	return len(seen)
}

func segmentSourceSlices(items []dialogueMessage) []domain.SegmentSourceSlice {
	out := make([]domain.SegmentSourceSlice, 0, len(items))
	position := 0
	for i, item := range items {
		if i > 0 {
			position += len(" | ")
		}
		position += len(item.Role) + len(": ")
		source := item.Source
		source.ContentByteStart, source.ContentByteEnd = position, position+len(item.Text)
		out = append(out, source)
		position += len(item.Text)
	}
	return out
}
