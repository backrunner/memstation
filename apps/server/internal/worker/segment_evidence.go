package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/textutil"
)

// A chunk's evidence must quote that chunk, not the beginning of its source.
// Older windows retain their original evidence representation.
func segmentNodeEvidences(doc domain.MemoryDocument, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact, messages []domain.UnifiedMessage) ([]domain.MemoryEvidence, error) {
	value, exists := doc.Metadata["source_slices"]
	if !exists {
		return buildNodeEvidences(doc.UserID, doc.GenerationID, doc.NodeID, doc.ConversationID, parts, artifacts, stringSliceValue(doc.Metadata, "source_part_ids"), stringSliceValue(doc.Metadata, "source_artifact_ids")), nil
	}
	raw, err := json.Marshal(value)
	var slices []domain.SegmentSourceSlice
	if err != nil || json.Unmarshal(raw, &slices) != nil || len(slices) == 0 {
		return nil, errors.New("invalid segment source slices")
	}
	partByID := map[string]domain.MessagePart{}
	for _, p := range parts {
		partByID[p.PartID] = p
	}
	artifactByID := map[string]domain.MessagePartArtifact{}
	for _, a := range artifacts {
		artifactByID[a.ArtifactID] = a
	}
	out := make([]domain.MemoryEvidence, 0, len(slices))
	for i, s := range slices {
		if s.SequenceNum < 0 || s.Index < 0 || s.ByteStart < 0 || s.ByteEnd <= s.ByteStart || s.ContentByteStart < 0 || s.ContentByteEnd > len(doc.Content) || s.ContentByteEnd <= s.ContentByteStart || s.ContentByteEnd-s.ContentByteStart != s.ByteEnd-s.ByteStart || len(s.SourceSHA256) != 64 {
			return nil, errors.New("invalid segment source span")
		}
		text := doc.Content[s.ContentByteStart:s.ContentByteEnd]
		if !utf8.ValidString(text) {
			return nil, errors.New("invalid segment source encoding")
		}
		var page int
		var startMS, endMS int64
		var original string
		if s.PartID != "" {
			p, ok := partByID[s.PartID]
			if !ok || p.SequenceNum != s.SequenceNum || p.PartIndex != s.Index || (p.UserID != "" && p.UserID != doc.UserID) || (p.ConversationID != "" && p.ConversationID != doc.ConversationID) {
				return nil, errors.New("segment source part mismatch")
			}
			page, startMS, endMS = p.PageIndex, p.StartMS, p.EndMS
			switch s.Field {
			case "part.text_content":
				original = p.TextContent
			case "part.raw_payload":
				original = p.RawPayload
			case "artifact.text_content":
				a, ok := artifactByID[s.ArtifactID]
				if !ok || a.PartID != p.PartID || (a.UserID != "" && a.UserID != doc.UserID) || (a.ConversationID != "" && a.ConversationID != doc.ConversationID) {
					return nil, errors.New("segment source artifact mismatch")
				}
				original = a.TextContent
				page, startMS, endMS = a.PageIndex, a.StartMS, a.EndMS
			default:
				return nil, errors.New("invalid segment source field")
			}
		} else {
			var sourceErr error
			original, sourceErr = segmentMessageSource(s, messages)
			if sourceErr != nil {
				return nil, sourceErr
			}
		}
		hash := sha256.Sum256([]byte(original))
		if s.SourceSHA256 != hex.EncodeToString(hash[:]) || s.ByteEnd > len(original) || original[s.ByteStart:s.ByteEnd] != text {
			return nil, errors.New("segment source revision mismatch")
		}

		// Slices, OCR and captions of one source part are not independent
		// corroboration. Exact field hashes remain in source_slice for verification.
		metadata := map[string]any{"source_slice": s, "support_kind": "source_slice", "source_id": fmt.Sprintf("%s|%s|%d|%s|%v|%v|%v", doc.UserID, doc.ConversationID, s.SequenceNum, s.PartID, doc.Metadata["source_conversation_revision"], doc.Metadata["source_snapshot_id"], doc.Metadata["source_snapshot_version"])}
		for _, key := range []string{"source_conversation_revision", "source_snapshot_id", "source_snapshot_version", "source_hash"} {
			if v, ok := doc.Metadata[key]; ok {
				metadata[key] = v
			}
		}
		out = append(out, domain.MemoryEvidence{ID: memoryEvidenceID(doc.NodeID, doc.ConversationID, s.PartID, s.ArtifactID, s.SequenceNum, i), UserID: doc.UserID, GenerationID: doc.GenerationID, NodeID: doc.NodeID, ConversationID: doc.ConversationID, SequenceNum: s.SequenceNum, PartID: s.PartID, ArtifactID: s.ArtifactID, PageIndex: page, StartMS: startMS, EndMS: endMS, EvidenceKind: "source_slice", ExcerptText: textutil.PrefixBytes(text, 280), Weight: 1, Metadata: metadata, CreatedAt: time.Now().UTC()})
	}
	return out, nil
}

func segmentMessageSource(s domain.SegmentSourceSlice, messages []domain.UnifiedMessage) (string, error) {
	if s.SequenceNum < 0 || s.SequenceNum >= len(messages) || s.Index < 0 || s.ArtifactID != "" {
		return "", errors.New("invalid segment message reference")
	}
	m := messages[s.SequenceNum]
	switch s.Field {
	case "message.text":
		if s.Index == 0 {
			return m.Text, nil
		}
	case "message.blocks.text", "message.blocks.raw", "message.blocks.text_proxy":
		if s.Index < len(m.Blocks) {
			b := m.Blocks[s.Index]
			switch s.Field {
			case "message.blocks.text":
				return b.Text, nil
			case "message.blocks.raw":
				return b.Raw, nil
			default:
				return b.TextProxy(), nil
			}
		}
	case "message.tool_results.output_json":
		if s.Index < len(m.ToolResults) {
			raw, err := json.Marshal(m.ToolResults[s.Index].Output)
			return string(raw), err
		}
	}
	return "", errors.New("invalid segment message field")
}
