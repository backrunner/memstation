package domain

// These are scoped operational projections, not raw database entities.
type WorkspaceActor struct {
	ActorID     string `json:"actor_id"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	Revision    int64  `json:"revision"`
}
type SourceSummary struct {
	ID             string `json:"id"`
	Revision       int64  `json:"revision"`
	SpeakerActorID string `json:"speaker_actor_id"`
	Status         string `json:"status"`
}
type MemoryControlOverview struct {
	NextCursor string             `json:"next_cursor"`
	Actors     []WorkspaceActor   `json:"actors"`
	Sources    []SourceSummary    `json:"sources"`
	Receipts   []IngestionReceipt `json:"receipts"`
	Jobs       []MemoryJob        `json:"jobs"`
	Operations []MemoryOperation  `json:"operations"`
	Limit      int                `json:"limit"`
}
