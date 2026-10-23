package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

func ensureConversationTimeline(ctx context.Context, services Services, conversation domain.ConversationRecord) ([]domain.MessagePart, []domain.MessagePartArtifact, error) {
	if services.Multimodal == nil {
		return nil, nil, nil
	}
	parts, err := services.Multimodal.ListMessageParts(ctx, conversation.UserID, conversation.ConversationID)
	if err != nil {
		return nil, nil, err
	}
	if len(parts) == 0 {
		parts, err = services.Multimodal.MaterializeConversation(ctx, conversation)
		if err != nil {
			return nil, nil, err
		}
	}
	artifacts, err := services.Multimodal.ListPartArtifacts(ctx, conversation.UserID, conversation.ConversationID)
	if err != nil {
		return nil, nil, err
	}
	if len(artifacts) == 0 {
		artifacts, err = services.Multimodal.ExtractConversation(ctx, conversation, parts)
		if err != nil {
			return nil, nil, err
		}
	}
	return parts, artifacts, nil
}

func buildNodeEvidences(userID, generationID, nodeID, conversationID string, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact, sourcePartIDs, sourceArtifactIDs []string) []domain.MemoryEvidence {
	partFilter := make(map[string]struct{}, len(sourcePartIDs))
	for _, id := range sourcePartIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			partFilter[id] = struct{}{}
		}
	}
	artifactFilter := make(map[string]struct{}, len(sourceArtifactIDs))
	for _, id := range sourceArtifactIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			artifactFilter[id] = struct{}{}
		}
	}
	if len(partFilter) == 0 && len(artifactFilter) == 0 {
		for _, part := range parts {
			partFilter[part.PartID] = struct{}{}
		}
		for _, artifact := range artifacts {
			artifactFilter[artifact.ArtifactID] = struct{}{}
		}
	}

	artifactByPart := map[string][]domain.MessagePartArtifact{}
	for _, artifact := range artifacts {
		if len(artifactFilter) > 0 {
			if _, ok := artifactFilter[artifact.ArtifactID]; !ok {
				continue
			}
		}
		artifactByPart[artifact.PartID] = append(artifactByPart[artifact.PartID], artifact)
	}

	evidences := make([]domain.MemoryEvidence, 0)
	now := time.Now().UTC()
	for _, part := range parts {
		if len(partFilter) > 0 {
			if _, ok := partFilter[part.PartID]; !ok {
				continue
			}
		}
		partArtifacts := artifactByPart[part.PartID]
		if len(partArtifacts) > 0 {
			for idx, artifact := range partArtifacts {
				evidences = append(evidences, domain.MemoryEvidence{
					ID:             memoryEvidenceID(nodeID, conversationID, part.PartID, artifact.ArtifactID, part.SequenceNum, len(evidences)+idx),
					GenerationID:   generationID,
					UserID:         userID,
					NodeID:         nodeID,
					ConversationID: conversationID,
					SequenceNum:    part.SequenceNum,
					PartID:         part.PartID,
					ArtifactID:     artifact.ArtifactID,
					PageIndex:      choosePageIndex(artifact.PageIndex, part.PageIndex),
					StartMS:        chooseInt64(artifact.StartMS, part.StartMS),
					EndMS:          chooseInt64(artifact.EndMS, part.EndMS),
					EvidenceKind:   artifact.ArtifactType,
					ExcerptText:    truncate(strings.TrimSpace(artifact.TextContent), 280),
					Weight:         1,
					Metadata:       cloneMap(artifact.Metadata),
					CreatedAt:      now,
				})
			}
			continue
		}
		excerpt := strings.TrimSpace(firstNonEmptyWorkerString(part.TextContent, part.RawPayload))
		if excerpt == "" {
			continue
		}
		evidences = append(evidences, domain.MemoryEvidence{
			ID:             memoryEvidenceID(nodeID, conversationID, part.PartID, "", part.SequenceNum, len(evidences)),
			GenerationID:   generationID,
			UserID:         userID,
			NodeID:         nodeID,
			ConversationID: conversationID,
			SequenceNum:    part.SequenceNum,
			PartID:         part.PartID,
			PageIndex:      part.PageIndex,
			StartMS:        part.StartMS,
			EndMS:          part.EndMS,
			EvidenceKind:   string(part.PartType),
			ExcerptText:    truncate(excerpt, 280),
			Weight:         0.85,
			Metadata:       cloneMap(part.Metadata),
			CreatedAt:      now,
		})
	}
	return evidences
}

func memoryEvidenceID(nodeID, conversationID, partID, artifactID string, sequenceNum, index int) string {
	seed := fmt.Sprintf("%s|%s|%s|%s|%d|%d", nodeID, conversationID, partID, artifactID, sequenceNum, index)
	sum := sha256.Sum256([]byte(seed))
	return "evidence:" + hex.EncodeToString(sum[:12])
}

func storeDocumentEvidences(ctx context.Context, services Services, userID, generationID, nodeID, conversationID string, evidences []domain.MemoryEvidence) error {
	if services.Memories == nil || strings.TrimSpace(nodeID) == "" {
		return nil
	}
	for idx := range evidences {
		if evidences[idx].GenerationID == "" {
			evidences[idx].GenerationID = generationID
		}
		if evidences[idx].ConversationID == "" {
			evidences[idx].ConversationID = conversationID
		}
	}
	return services.Memories.ReplaceEvidences(ctx, userID, nodeID, evidences)
}

func cloneNodeEvidences(userID, generationID, nodeID string, source []domain.MemoryEvidence) []domain.MemoryEvidence {
	if len(source) == 0 {
		return nil
	}
	now := time.Now().UTC()
	out := make([]domain.MemoryEvidence, 0, len(source))
	for idx, evidence := range source {
		cloned := evidence
		cloned.ID = memoryEvidenceID(nodeID, evidence.ConversationID, evidence.PartID, evidence.ArtifactID, evidence.SequenceNum, idx)
		cloned.UserID = userID
		cloned.NodeID = nodeID
		cloned.GenerationID = generationID
		cloned.CreatedAt = now
		cloned.Metadata = cloneMap(evidence.Metadata)
		out = append(out, cloned)
	}
	return out
}

func modalitiesFromParts(parts []domain.MessagePart) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		modality := string(part.PartType)
		switch part.PartType {
		case domain.ContentBlockText, domain.ContentBlockJSON, domain.ContentBlockToolUse, domain.ContentBlockToolResult:
			modality = "text"
		}
		if _, ok := seen[modality]; ok || modality == "" {
			continue
		}
		seen[modality] = struct{}{}
		out = append(out, modality)
	}
	return out
}

func hasSourceBlob(parts []domain.MessagePart) bool {
	for _, part := range parts {
		if strings.TrimSpace(part.BlobID) != "" {
			return true
		}
	}
	return false
}

func choosePageIndex(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func chooseInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyWorkerString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// groundedNodeEvidences keeps source identities stable across derived nodes.
// Unmatched summary references are context, never independent corroboration.
func groundedNodeEvidences(doc domain.MemoryDocument, source []domain.MemoryEvidence) []domain.MemoryEvidence {
	quotes, _ := doc.Metadata["source_quotes"].([]any)
	out := []domain.MemoryEvidence{}
	for _, raw := range quotes {
		q, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		quote, _ := q["quote"].(string)
		part, _ := q["part_id"].(string)
		sourceID, _ := q["source_id"].(string)
		seq, _ := q["sequence_num"].(int)
		if quote == "" || sourceID == "" {
			continue
		}
		evidence := domain.MemoryEvidence{ID: memoryEvidenceID(doc.NodeID, doc.ConversationID, part, sourceID, seq, 0), UserID: doc.UserID, GenerationID: doc.GenerationID, NodeID: doc.NodeID, ConversationID: doc.ConversationID, SequenceNum: seq, PartID: part, ExcerptText: quote, EvidenceKind: "exact_quote", Weight: 1, Metadata: cloneMap(q), CreatedAt: time.Now().UTC()}
		out = append(out, evidence)
	}
	if len(out) > 0 {
		return out
	}
	// Artifact observations have explicit identifiers even when textual claims
	// cannot be matched. Other inherited references are explicitly inferential.
	for _, e := range cloneNodeEvidences(doc.UserID, doc.GenerationID, doc.NodeID, source) {
		e.Metadata = cloneMap(e.Metadata)
		e.Metadata["support_kind"] = "model_inference"
		e.Metadata["source_id"] = fmt.Sprintf("%s|%s|%d|%s|%s", doc.UserID, e.ConversationID, e.SequenceNum, e.PartID, e.ArtifactID)
		e.Weight = min(e.Weight, 0.35)
		e.EvidenceKind = "context_reference"
		if artifact := stringValue(doc.Metadata, "artifact_id"); artifact != "" && e.ArtifactID == artifact {
			e.Metadata["support_kind"] = "tool_observation"
			e.Weight = 0.85
			e.EvidenceKind = "artifact_observation"
		}
		out = append(out, e)
	}
	return out
}

func relationWithEvidence(ctx context.Context, services Services, relation domain.MemoryRelation) (domain.MemoryRelation, error) {
	evidences, err := services.Memories.ListEvidences(ctx, relation.UserID, relation.DstNodeID)
	if err != nil {
		return relation, err
	}
	relation.Metadata = cloneMap(relation.Metadata)
	ids := []string{}
	sources := []string{}
	seen := map[string]bool{}
	for _, e := range evidences {
		source := stringValue(e.Metadata, "source_id")
		kind := stringValue(e.Metadata, "support_kind")
		if source == "" || kind == "model_inference" || seen[source] {
			continue
		}
		seen[source] = true
		ids = append(ids, e.ID)
		sources = append(sources, source)
	}
	relation.Metadata["evidence_ids"] = ids
	relation.Metadata["source_ids"] = sources
	relation.Metadata["confidence"] = 0.5
	if len(sources) > 0 {
		relation.Metadata["confidence"] = 0.9
	}
	return relation, nil
}
