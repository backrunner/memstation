package domain

import "time"

// MemoryWriteCondition identifies the exact snapshot the administrator edited.
// UpdatedAt is transported as RFC3339Nano, never as a JavaScript Date/number.
// Create permits a new (or previously deleted) key only, never an upsert.
type MemoryWriteCondition struct {
	ID           string    `json:"-"`
	UpdatedAt    time.Time `json:"expected_updated_at"`
	GenerationID string    `json:"generation_id"`
	Create       bool      `json:"-"`
}
