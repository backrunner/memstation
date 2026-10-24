package domain

import "time"

// RecordBrowseQuery is an exact substring browse, not semantic retrieval.
type RecordBrowseQuery struct {
	UserID string
	Query  string
	Limit  int
	After  string
}

type RecordPosition struct {
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"id"`
}

type RecordPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// ConversationListItem contains summaries without loading message bodies.
type ConversationListItem struct {
	ConversationID string         `json:"conversation_id"`
	UserID         string         `json:"user_id"`
	Provider       string         `json:"provider"`
	Protocol       string         `json:"protocol"`
	Mode           string         `json:"mode"`
	FinalizeStatus string         `json:"finalize_status"`
	SummaryStatus  string         `json:"summary_status"`
	AnalysisStatus string         `json:"analysis_status"`
	MessageCount   int            `json:"message_count"`
	TotalTokens    int            `json:"total_tokens"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}
