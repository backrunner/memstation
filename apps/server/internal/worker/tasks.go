package worker

const (
	TypeConversationFinalize      = "conversation.finalize"
	TypeSummaryGenerate           = "summary.generate"
	TypeEmbeddingGenerate         = "embedding.generate"
	TypeAnalysisExtract           = "analysis.extract"
	TypeKeyMemoryReconcile        = "keymemory.reconcile"
	TypeMemoryRebuildConversation = "memory.rebuild.conversation"
	TypeMemoryRebuildActivate     = "memory.rebuild.activate"
)

const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueLow      = "low"
	QueueBulk     = "bulk"
)

type SummaryPayload struct {
	ConversationID   string `json:"conversation_id"`
	UserID           string `json:"user_id"`
	SummaryVersion   int    `json:"summary_version"`
	FinalizeReason   string `json:"finalize_reason"`
	GenerationID     string `json:"generation_id"`
	EmbeddingVersion int    `json:"embedding_version"`
	EmbeddingModel   string `json:"embedding_model"`
}

type EmbeddingPayload struct {
	UserID           string `json:"user_id"`
	GenerationID     string `json:"generation_id"`
	NodeID           string `json:"node_id"`
	NodeType         string `json:"node_type"`
	EmbeddingVersion int    `json:"embedding_version"`
	Content          string `json:"content"`
	EmbeddingModel   string `json:"embedding_model"`
}

type AnalysisPayload struct {
	UserID           string   `json:"user_id"`
	ConversationID   string   `json:"conversation_id"`
	SummaryIDs       []string `json:"summary_ids"`
	GenerationID     string   `json:"generation_id"`
	EmbeddingVersion int      `json:"embedding_version"`
	EmbeddingModel   string   `json:"embedding_model"`
}

type KeyMemoryReconcilePayload struct {
	UserID         string `json:"user_id"`
	ConversationID string `json:"conversation_id"`
	GenerationID   string `json:"generation_id"`
}

type MemoryRebuildConversationPayload struct {
	UserID       string `json:"user_id"`
	GenerationID string `json:"generation_id"`
	SnapshotID   string `json:"snapshot_id"`
}

type MemoryRebuildActivatePayload struct {
	UserID       string `json:"user_id"`
	GenerationID string `json:"generation_id"`
}
