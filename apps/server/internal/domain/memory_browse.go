package domain

import "time"

// MemoryBrowseQuery is a literal content/ID filter, independent of semantic ranking.
type MemoryBrowseQuery struct {
	UserID       string
	GenerationID string
	Kinds        []MemoryType
	Query        string
	Limit        int
	After        string
}

// MemoryPosition defines the total descending order used by both stores.
type MemoryPosition struct {
	UpdatedAt time.Time  `json:"updated_at"`
	Kind      MemoryType `json:"kind"`
	ID        string     `json:"id"`
}

type MemoryDocumentPage struct {
	Items        []MemoryDocument `json:"items"`
	NextCursor   string           `json:"next_cursor"`
	GenerationID string           `json:"generation_id"`
}
