package domain

import "time"

// TextSpan indexes Unicode code points in one content block, [start,end).
type TextSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
type SourceRef struct {
	WorkspaceID string    `json:"workspace_id"`
	MessageID   string    `json:"message_id"`
	Revision    int64     `json:"revision"`
	PartIndex   int       `json:"part_index"`
	Span        *TextSpan `json:"span,omitempty"`
}
type SourceSpeaker struct {
	ExternalID  string `json:"external_id,omitempty"`
	ActorID     string `json:"actor_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Kind        string `json:"kind"`
}
type SourcePart struct {
	Media   *ContentBlock  `json:"media,omitempty"`
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	ActorID string         `json:"actor_id,omitempty"`
	Source  *SourceRef     `json:"source,omitempty"`
	Author  *SourceSpeaker `json:"author,omitempty"`
}
type SourceEvent struct {
	EventID         string        `json:"event_id"`
	Type            string        `json:"type"`
	SourceMessageID string        `json:"source_message_id"`
	SourceRevision  int64         `json:"source_revision"`
	Speaker         SourceSpeaker `json:"speaker"`
	Parts           []SourcePart  `json:"parts,omitempty"`
	ReplyTo         *SourceRef    `json:"reply_to,omitempty"`
	OccurredAt      time.Time     `json:"occurred_at"`
	DeliveryStatus  string        `json:"delivery_status,omitempty"`
	DeliveryReceipt string        `json:"delivery_receipt,omitempty"`
}
type ObserveBatch struct {
	WorkspaceID     string        `json:"workspace_id"`
	IntegrationID   string        `json:"integration_id"`
	ExternalSpaceID string        `json:"external_space_id"`
	ThreadID        string        `json:"thread_id,omitempty"`
	PartitionID     string        `json:"partition_id,omitempty"`
	Events          []SourceEvent `json:"events"`
}
type EventAcceptance struct {
	ConversationID string `json:"conversation_id,omitempty"`
	EventID        string `json:"event_id"`
	MessageID      string `json:"message_id"`
	Status         string `json:"status"`
	Sequence       int64  `json:"sequence"`
}
type IngestionReceipt struct {
	ID               string            `json:"id"`
	WorkspaceID      string            `json:"workspace_id"`
	StreamID         string            `json:"stream_id"`
	PartitionID      string            `json:"partition_id,omitempty"`
	GenerationID     string            `json:"generation_id,omitempty"`
	AcceptedSequence int64             `json:"accepted_sequence"`
	IndexedSequence  int64             `json:"indexed_sequence"`
	Status           string            `json:"status"`
	Stage            string            `json:"stage,omitempty"`
	FailureCode      string            `json:"failure_code,omitempty"`
	RetryCount       int               `json:"retry_count"`
	NextRetryAt      *time.Time        `json:"next_retry_at,omitempty"`
	DeadLetter       bool              `json:"dead_letter"`
	Events           []EventAcceptance `json:"events"`
}
type MemoryPartition struct {
	ID               string   `json:"id"`
	UserID           string   `json:"user_id"`
	WorkspaceID      string   `json:"workspace_id"`
	AgentID          string   `json:"agent_id"`
	AudienceActorIDs []string `json:"audience_actor_ids"`
	Revision         int64    `json:"revision"`
}

type SourceRecord struct {
	ID             string       `json:"id"`
	WorkspaceID    string       `json:"workspace_id"`
	PartitionID    string       `json:"partition_id,omitempty"`
	IntegrationID  string       `json:"integration_id"`
	Revision       int64        `json:"revision"`
	SpeakerActorID string       `json:"speaker_actor_id,omitempty"`
	Parts          []SourcePart `json:"parts"`
	ReplyTo        *SourceRef   `json:"reply_to,omitempty"`
	OccurredAt     time.Time    `json:"occurred_at"`
}

type IntegrationConfig struct {
	ID                string   `json:"id,omitempty"`
	UserID            string   `json:"user_id"`
	WorkspaceID       string   `json:"workspace_id"`
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	ExternalSpaceID   string   `json:"external_space_id"`
	AgentID           string   `json:"agent_id"`
	AllowedEventTypes []string `json:"allowed_event_types"`
	Revision          int64    `json:"revision"`
}

type MemoryJob struct {
	GenerationID   string     `json:"generation_id"`
	Queue          string     `json:"queue"`
	OperationID    string     `json:"operation_id,omitempty"`
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	WorkspaceID    string     `json:"workspace_id"`
	PartitionID    string     `json:"partition_id"`
	ConversationID string     `json:"conversation_id"`
	TaskType       string     `json:"task_type"`
	Status         string     `json:"status"`
	Attempt        int        `json:"attempt"`
	FailureCode    string     `json:"failure_code,omitempty"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
}

type MemoryTaskScope struct {
	ReceiptID            string `json:"receipt_id"`
	OperationID          string `json:"operation_id"`
	UserID               string `json:"user_id"`
	WorkspaceID          string `json:"workspace_id"`
	PartitionID          string `json:"partition_id"`
	ConversationID       string `json:"conversation_id"`
	NodeID               string `json:"node_id"`
	GenerationID         string `json:"generation_id"`
	ConversationRevision int64  `json:"conversation_revision"`
}

// MessageSource is hydrated only from the trusted event projection, never gateway metadata.
type MessageSource struct {
	WorkspaceID string        `json:"workspace_id"`
	MessageID   string        `json:"message_id"`
	Revision    int64         `json:"revision"`
	Speaker     SourceSpeaker `json:"speaker"`
	Parts       []SourcePart  `json:"parts"`
	ReplyTo     *SourceRef    `json:"reply_to,omitempty"`
	OccurredAt  time.Time     `json:"occurred_at"`
}

type ConversationWindowInput struct {
	WorkspaceID     string `json:"workspace_id"`
	IntegrationID   string `json:"integration_id"`
	ExternalSpaceID string `json:"external_space_id"`
	ThreadID        string `json:"thread_id,omitempty"`
	PartitionID     string `json:"partition_id,omitempty"`
}
type ConversationWindow struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	PartitionID string `json:"partition_id,omitempty"`
	StreamID    string `json:"stream_id"`
	Revision    int64  `json:"revision"`
	Status      string `json:"status"`
}
