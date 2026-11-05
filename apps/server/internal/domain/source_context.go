package domain

import "time"

// Sequence numbers are zero based, matching persisted messages/segments.
// Refs anchor event ingestion by exact revision; callers must not fall back to
// sequence numbers when revision-aware references are available.
type SourceWindowInput struct {
	UserID               string
	WorkspaceID          string
	ConversationID       string
	ConversationRevision int64
	Sequences            []int
	Refs                 []SourceRef
	Radius               int
	Limit                int
	MaxBytes             int
}

type SourceTurn struct {
	UserID         string
	WorkspaceID    string
	ConversationID string
	Sequence       int
	// Anchor is computed by the source reader from the validated requested
	// revision references or sequence coordinates, never from source text.
	Anchor      bool
	Role        MessageRole
	Speaker     string
	OccurredAt  time.Time
	SourceDate  string
	ExternalRef string
	Text        string
	Refs        []SourceRef
	Parts       []SourcePart
}
