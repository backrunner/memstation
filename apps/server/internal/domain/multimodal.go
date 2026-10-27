package domain

import "time"

// NormalizeMessageParts binds adapter output to its verified parent and rejects
// parent substitutions. Callers validate the workspace before normalization.
func NormalizeMessageParts(user, workspace, conversation string, parts []MessagePart) ([]MessagePart, error) {
	out := append([]MessagePart(nil), parts...)
	for i := range out {
		p := &out[i]
		if (p.UserID != "" && p.UserID != user) || (p.WorkspaceID != "" && p.WorkspaceID != workspace) || (p.ConversationID != "" && p.ConversationID != conversation) {
			return nil, ErrAccessDenied
		}
		p.UserID = user
		p.WorkspaceID = workspace
		p.ConversationID = conversation
	}
	return out, nil
}

func NormalizePartArtifacts(user, workspace, conversation string, artifacts []MessagePartArtifact) ([]MessagePartArtifact, error) {
	out := append([]MessagePartArtifact(nil), artifacts...)
	for i := range out {
		p := &out[i]
		if (p.UserID != "" && p.UserID != user) || (p.WorkspaceID != "" && p.WorkspaceID != workspace) || (p.ConversationID != "" && p.ConversationID != conversation) {
			return nil, ErrAccessDenied
		}
		p.UserID = user
		p.WorkspaceID = workspace
		p.ConversationID = conversation
	}
	return out, nil
}

type BlobSourceKind string

const (
	BlobSourceKindUpload   BlobSourceKind = "upload"
	BlobSourceKindExternal BlobSourceKind = "external"
	BlobSourceKindProvider BlobSourceKind = "provider"
)

type BlobObject struct {
	PartitionID      string         `json:"partition_id,omitempty"`
	WorkspaceID      string         `json:"workspace_id,omitempty"`
	BlobID           string         `json:"blob_id"`
	UserID           string         `json:"user_id"`
	SourceKind       BlobSourceKind `json:"source_kind"`
	StorageBackend   string         `json:"storage_backend"`
	StorageKey       string         `json:"storage_key"`
	MIMEType         string         `json:"mime_type,omitempty"`
	SHA256           string         `json:"sha256,omitempty"`
	SizeBytes        int64          `json:"size_bytes,omitempty"`
	OriginalFilename string         `json:"original_filename,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	DeletedAt        *time.Time     `json:"deleted_at,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type MessagePart struct {
	PartitionID    string           `json:"partition_id,omitempty"`
	WorkspaceID    string           `json:"workspace_id,omitempty"`
	PartID         string           `json:"part_id"`
	UserID         string           `json:"user_id"`
	ConversationID string           `json:"conversation_id"`
	SequenceNum    int              `json:"sequence_num"`
	PartIndex      int              `json:"part_index"`
	Role           MessageRole      `json:"role"`
	PartType       ContentBlockType `json:"part_type"`
	MIMEType       string           `json:"mime_type,omitempty"`
	BlobID         string           `json:"blob_id,omitempty"`
	SourceURL      string           `json:"source_url,omitempty"`
	ProviderFileID string           `json:"provider_file_id,omitempty"`
	Filename       string           `json:"filename,omitempty"`
	TextContent    string           `json:"text_content,omitempty"`
	RawPayload     string           `json:"raw_payload,omitempty"`
	PageIndex      int              `json:"page_index,omitempty"`
	StartMS        int64            `json:"start_ms,omitempty"`
	EndMS          int64            `json:"end_ms,omitempty"`
	DurationMS     int64            `json:"duration_ms,omitempty"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type MessagePartArtifact struct {
	PartitionID    string         `json:"partition_id,omitempty"`
	WorkspaceID    string         `json:"workspace_id,omitempty"`
	ArtifactID     string         `json:"artifact_id"`
	UserID         string         `json:"user_id"`
	ConversationID string         `json:"conversation_id"`
	PartID         string         `json:"part_id"`
	ArtifactType   string         `json:"artifact_type"`
	Status         string         `json:"status"`
	ExtractorKind  string         `json:"extractor_kind,omitempty"`
	ModelName      string         `json:"model_name,omitempty"`
	TextContent    string         `json:"text_content,omitempty"`
	PageIndex      int            `json:"page_index,omitempty"`
	StartMS        int64          `json:"start_ms,omitempty"`
	EndMS          int64          `json:"end_ms,omitempty"`
	Confidence     float64        `json:"confidence,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type MemoryEvidence struct {
	ID             string         `json:"id"`
	GenerationID   string         `json:"generation_id,omitempty"`
	UserID         string         `json:"user_id"`
	NodeID         string         `json:"node_id"`
	ConversationID string         `json:"conversation_id"`
	SequenceNum    int            `json:"sequence_num"`
	PartID         string         `json:"part_id,omitempty"`
	ArtifactID     string         `json:"artifact_id,omitempty"`
	PageIndex      int            `json:"page_index,omitempty"`
	StartMS        int64          `json:"start_ms,omitempty"`
	EndMS          int64          `json:"end_ms,omitempty"`
	EvidenceKind   string         `json:"evidence_kind"`
	ExcerptText    string         `json:"excerpt_text,omitempty"`
	Weight         float64        `json:"weight"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

func ModalitiesFromBlocks(blocks []ContentBlock) []string {
	if len(blocks) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		modality := block.Modality()
		if modality == "" {
			continue
		}
		if _, ok := seen[modality]; ok {
			continue
		}
		seen[modality] = struct{}{}
		out = append(out, modality)
	}
	return out
}
