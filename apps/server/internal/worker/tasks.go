package worker

const (
	TypeConversationFinalize = "conversation.finalize"
	TypeSummaryGenerate      = "summary.generate"
	TypeEmbeddingGenerate    = "embedding.generate"
	TypeAnalysisExtract      = "analysis.extract"
	TypeKeyMemoryReconcile   = "keymemory.reconcile"
	TypeEmbeddingRebuild     = "embedding.rebuild"
)

const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueLow      = "low"
	QueueBulk     = "bulk"
)

type SummaryPayload struct {
	ConversationID string `json:"conversation_id"`
	UserID         string `json:"user_id"`
	SummaryVersion int    `json:"summary_version"`
	FinalizeReason string `json:"finalize_reason"`
}

type EmbeddingPayload struct {
	UserID           string `json:"user_id"`
	NodeID           string `json:"node_id"`
	NodeType         string `json:"node_type"`
	EmbeddingVersion int    `json:"embedding_version"`
	Content          string `json:"content"`
	EmbeddingModel   string `json:"embedding_model"`
}

type AnalysisPayload struct {
	UserID         string   `json:"user_id"`
	ConversationID string   `json:"conversation_id"`
	SummaryIDs     []string `json:"summary_ids"`
}

type EmbeddingRebuildPayload struct {
	UserID           string `json:"user_id"`
	EmbeddingVersion int    `json:"embedding_version"`
}
