package domain

import "time"

type (
	FinalizeStatus   string
	ProcessingStatus string
)

const (
	FinalizeStatusActive  FinalizeStatus = "active"
	FinalizeStatusEnding  FinalizeStatus = "ending"
	FinalizeStatusEnded   FinalizeStatus = "ended"
	FinalizeStatusTimeout FinalizeStatus = "timeout"
	FinalizeStatusFailed  FinalizeStatus = "failed"
)

const (
	ProcessingStatusPending   ProcessingStatus = "pending"
	ProcessingStatusQueued    ProcessingStatus = "queued"
	ProcessingStatusRunning   ProcessingStatus = "running"
	ProcessingStatusCompleted ProcessingStatus = "completed"
	ProcessingStatusFailed    ProcessingStatus = "failed"
	ProcessingStatusSkipped   ProcessingStatus = "skipped"
)

type ConversationRecord struct {
	PartitionID      string                `json:"partition_id,omitempty"`
	ContextMode      string                `json:"context_mode"`
	WorkspaceID      string                `json:"workspace_id"`
	AgentID          string                `json:"agent_id"`
	Revision         int64                 `json:"revision"`
	ExpectedRevision *int64                `json:"-"`
	ConversationID   string                `json:"conversation_id"`
	UserID           string                `json:"user_id"`
	RequestFormat    string                `json:"request_format"`
	Provider         Provider              `json:"provider"`
	Mode             Mode                  `json:"mode"`
	FinalizeStatus   FinalizeStatus        `json:"finalize_status"`
	SummaryStatus    ProcessingStatus      `json:"summary_status"`
	AnalysisStatus   ProcessingStatus      `json:"analysis_status"`
	Messages         []UnifiedMessage      `json:"messages"`
	Parts            []MessagePart         `json:"parts,omitempty"`
	Artifacts        []MessagePartArtifact `json:"artifacts,omitempty"`
	Segments         []ConversationSegment `json:"segments,omitempty"`
	Response         *UnifiedResponse      `json:"response,omitempty"`
	Usage            UsageRecord           `json:"usage"`
	Metadata         map[string]any        `json:"metadata,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

// ConversationFinalization is a request to seal the stored conversation. The
// store reads retention settings and identity from the locked parent row.
type ConversationFinalization struct {
	UserID         string
	WorkspaceID    string
	ConversationID string
	Status         FinalizeStatus
	Reason         string
	// Recheck the scheduler's timeout cutoff under the same lock as finalization.
	TimeoutBefore *time.Time
}

// SegmentSourceSlice uses UTF-8 byte offsets [start,end), unlike SourceRef.Span
// which uses code points. Offsets address the named original text field and the
// rendered segment. SourceSHA256 covers the complete field, not just the slice.
// The enclosing memory carries conversation revision / snapshot / generation.
type SegmentSourceSlice struct {
	SequenceNum      int    `json:"sequence_num"`
	PartID           string `json:"part_id,omitempty"`
	ArtifactID       string `json:"artifact_id,omitempty"`
	Field            string `json:"field"`
	Index            int    `json:"index"`
	SourceSHA256     string `json:"source_sha256"`
	ByteStart        int    `json:"byte_start"`
	ByteEnd          int    `json:"byte_end"`
	ContentByteStart int    `json:"content_byte_start"`
	ContentByteEnd   int    `json:"content_byte_end"`
}

type ConversationSegment struct {
	ID                  string         `json:"id"`
	GenerationID        string         `json:"generation_id,omitempty"`
	UserID              string         `json:"user_id"`
	ConversationID      string         `json:"conversation_id"`
	SegmentIndex        int            `json:"segment_index"`
	StartSequence       int            `json:"start_sequence"`
	EndSequence         int            `json:"end_sequence"`
	MessageCount        int            `json:"message_count"`
	OverlapFromSequence int            `json:"overlap_from_sequence"`
	OverlapMessageCount int            `json:"overlap_message_count"`
	Content             string         `json:"content"`
	ContentHash         string         `json:"content_hash"`
	Metadata            map[string]any `json:"metadata,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

type ConversationFinalizeInput struct {
	ConversationID string         `json:"conversation_id"`
	UserID         string         `json:"user_id"`
	Status         FinalizeStatus `json:"status"`
	Reason         string         `json:"reason"`
	MessageCount   int            `json:"message_count"`
	TokenCount     int            `json:"token_count"`
}

type ConversationStateUpdate struct {
	ConversationID string            `json:"conversation_id"`
	UserID         string            `json:"user_id"`
	FinalizeStatus *FinalizeStatus   `json:"finalize_status,omitempty"`
	SummaryStatus  *ProcessingStatus `json:"summary_status,omitempty"`
	AnalysisStatus *ProcessingStatus `json:"analysis_status,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at"`
}
