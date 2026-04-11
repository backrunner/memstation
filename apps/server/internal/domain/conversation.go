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
	ConversationID string                `json:"conversation_id"`
	UserID         string                `json:"user_id"`
	RequestFormat  string                `json:"request_format"`
	Provider       Provider              `json:"provider"`
	Mode           Mode                  `json:"mode"`
	FinalizeStatus FinalizeStatus        `json:"finalize_status"`
	SummaryStatus  ProcessingStatus      `json:"summary_status"`
	AnalysisStatus ProcessingStatus      `json:"analysis_status"`
	Messages       []UnifiedMessage      `json:"messages"`
	Parts          []MessagePart         `json:"parts,omitempty"`
	Artifacts      []MessagePartArtifact `json:"artifacts,omitempty"`
	Segments       []ConversationSegment `json:"segments,omitempty"`
	Response       *UnifiedResponse      `json:"response,omitempty"`
	Usage          UsageRecord           `json:"usage"`
	Metadata       map[string]any        `json:"metadata,omitempty"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
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
