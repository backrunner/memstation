package domain

import "time"

type SearchInput struct {
	UserID              string       `json:"user_id"`
	GenerationID        string       `json:"generation_id,omitempty"`
	Query               string       `json:"query"`
	TopN                int          `json:"top_n"`
	IncludeConversation bool         `json:"include_conversation"`
	IncludeSegments     bool         `json:"include_segments"`
	MemoryTypes         []MemoryType `json:"memory_types,omitempty"`
	TimeTiers           []TimeTier   `json:"time_tiers,omitempty"`
	DateFrom            *time.Time   `json:"date_from,omitempty"`
	DateTo              *time.Time   `json:"date_to,omitempty"`
	MinScore            float64      `json:"min_score"`
}

type RelatedMemory struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Weight float64 `json:"weight"`
}

type MemoryDocument struct {
	ID             string           `json:"id"`
	NodeID         string           `json:"node_id"`
	GenerationID   string           `json:"generation_id,omitempty"`
	UserID         string           `json:"user_id"`
	ConversationID string           `json:"conversation_id,omitempty"`
	Type           MemoryType       `json:"type"`
	Content        string           `json:"content"`
	Score          float64          `json:"score"`
	EffectiveScore float64          `json:"effective_score"`
	Importance     float64          `json:"importance"`
	DecayFactor    float64          `json:"decay_factor"`
	ActivityAt     time.Time        `json:"activity_at"`
	TimeTier       TimeTier         `json:"time_tier,omitempty"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
	Related        []RelatedMemory  `json:"related,omitempty"`
	Evidences      []MemoryEvidence `json:"evidences,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type SearchResult struct {
	Memories             []MemoryDocument `json:"memories"`
	Total                int              `json:"total"`
	QueryEmbeddingTokens int              `json:"query_embedding_tokens"`
}

type KeywordMatch struct {
	NodeID      string   `json:"node_id"`
	Keywords    []string `json:"keywords,omitempty"`
	TotalWeight float64  `json:"total_weight"`
	Score       float64  `json:"score"`
}

type EmbeddingInput struct {
	UserID string `json:"user_id"`
	Text   string `json:"text"`
	Model  string `json:"model"`
}

type EmbeddingResult struct {
	Vector       []float32 `json:"vector"`
	Model        string    `json:"model"`
	PromptTokens int       `json:"prompt_tokens"`
	Dimensions   int       `json:"dimensions"`
}

type KeyMemory struct {
	ID              string         `json:"id"`
	UserID          string         `json:"user_id"`
	Key             string         `json:"key"`
	Value           string         `json:"value"`
	Category        string         `json:"category,omitempty"`
	Source          string         `json:"source,omitempty"`
	ReviewStatus    string         `json:"review_status,omitempty"`
	LastConfirmedAt *time.Time     `json:"last_confirmed_at,omitempty"`
	Importance      float64        `json:"importance,omitempty"`
	DecayFactor     float64        `json:"decay_factor,omitempty"`
	ActivityAt      time.Time      `json:"activity_at,omitempty"`
	TimeTier        TimeTier       `json:"time_tier,omitempty"`
	EffectiveScore  float64        `json:"effective_score,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type KeyMemoryQuery struct {
	UserID    string     `json:"user_id"`
	Query     string     `json:"query,omitempty"`
	TopK      int        `json:"top_k"`
	TimeTiers []TimeTier `json:"time_tiers,omitempty"`
	DateFrom  *time.Time `json:"date_from,omitempty"`
	DateTo    *time.Time `json:"date_to,omitempty"`
}

type MemoryRelation struct {
	ID           string         `json:"id"`
	GenerationID string         `json:"generation_id,omitempty"`
	UserID       string         `json:"user_id"`
	SrcNodeID    string         `json:"src_node_id"`
	DstNodeID    string         `json:"dst_node_id"`
	RelationType string         `json:"relation_type"`
	Weight       float64        `json:"weight"`
	Source       string         `json:"source"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type InjectionResult struct {
	Query  string       `json:"query"`
	Search SearchResult `json:"search"`
}

type VectorSearchInput struct {
	UserID           string       `json:"user_id"`
	GenerationID     string       `json:"generation_id,omitempty"`
	Query            string       `json:"query"`
	TopK             int          `json:"top_k"`
	AllowedTypes     []MemoryType `json:"allowed_types,omitempty"`
	EmbeddingVersion int          `json:"embedding_version"`
	EmbeddingModel   string       `json:"embedding_model,omitempty"`
}

type VectorSearchResult struct {
	NodeID   string  `json:"node_id"`
	Score    float64 `json:"score"`
	UserID   string  `json:"user_id"`
	NodeType string  `json:"node_type"`
}

type RerankInput struct {
	UserID    string           `json:"user_id"`
	Query     string           `json:"query"`
	Model     string           `json:"model,omitempty"`
	TopN      int              `json:"top_n"`
	Documents []RerankDocument `json:"documents"`
}

type RerankDocument struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type RerankResult struct {
	DocumentID string  `json:"document_id"`
	Index      int     `json:"index"`
	Score      float64 `json:"score"`
}

type VectorUpsertRequest struct {
	UserID           string    `json:"user_id"`
	GenerationID     string    `json:"generation_id,omitempty"`
	NodeID           string    `json:"node_id"`
	NodeType         string    `json:"node_type"`
	EmbeddingVersion int       `json:"embedding_version"`
	EmbeddingModel   string    `json:"embedding_model"`
	Content          string    `json:"content"`
	ContentHash      string    `json:"content_hash"`
	Vector           []float32 `json:"vector"`
}

type MemoryGenerationStatus string

const (
	MemoryGenerationStatusQueued    MemoryGenerationStatus = "queued"
	MemoryGenerationStatusRunning   MemoryGenerationStatus = "running"
	MemoryGenerationStatusCompleted MemoryGenerationStatus = "completed"
	MemoryGenerationStatusFailed    MemoryGenerationStatus = "failed"
	MemoryGenerationStatusActivated MemoryGenerationStatus = "activated"
)

type MemoryGeneration struct {
	GenerationID      string                 `json:"generation_id"`
	UserID            string                 `json:"user_id"`
	Status            MemoryGenerationStatus `json:"status"`
	Scope             string                 `json:"scope"`
	ConversationID    string                 `json:"conversation_id,omitempty"`
	IsActive          bool                   `json:"is_active"`
	StructureVersion  int                    `json:"structure_version"`
	EmbeddingVersion  int                    `json:"embedding_version"`
	EmbeddingModel    string                 `json:"embedding_model"`
	ActivateOnSuccess bool                   `json:"activate_on_success"`
	SourceCount       int                    `json:"source_count"`
	ProcessedCount    int                    `json:"processed_count"`
	EmbeddingCount    int                    `json:"embedding_count"`
	FailureCount      int                    `json:"failure_count"`
	LastError         string                 `json:"last_error,omitempty"`
	Metadata          map[string]any         `json:"metadata,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	ActivatedAt       *time.Time             `json:"activated_at,omitempty"`
}

type MemoryGenerationSourceStatus string

const (
	MemoryGenerationSourceStatusQueued    MemoryGenerationSourceStatus = "queued"
	MemoryGenerationSourceStatusRunning   MemoryGenerationSourceStatus = "running"
	MemoryGenerationSourceStatusCompleted MemoryGenerationSourceStatus = "completed"
	MemoryGenerationSourceStatusFailed    MemoryGenerationSourceStatus = "failed"
)

type MemorySourceSnapshot struct {
	SnapshotID      string           `json:"snapshot_id"`
	UserID          string           `json:"user_id"`
	ConversationID  string           `json:"conversation_id"`
	SnapshotVersion int              `json:"snapshot_version"`
	SourceHash      string           `json:"source_hash"`
	RequestFormat   string           `json:"request_format"`
	Provider        Provider         `json:"provider"`
	Mode            Mode             `json:"mode"`
	FinalizeStatus  FinalizeStatus   `json:"finalize_status"`
	Metadata        map[string]any   `json:"metadata,omitempty"`
	Usage           UsageRecord      `json:"usage"`
	MessageCount    int              `json:"message_count"`
	Messages        []UnifiedMessage `json:"messages,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

type MemoryGenerationSource struct {
	ID             string                       `json:"id"`
	GenerationID   string                       `json:"generation_id"`
	UserID         string                       `json:"user_id"`
	SnapshotID     string                       `json:"snapshot_id"`
	ConversationID string                       `json:"conversation_id"`
	SourceHash     string                       `json:"source_hash"`
	Status         MemoryGenerationSourceStatus `json:"status"`
	LastError      string                       `json:"last_error,omitempty"`
	CreatedAt      time.Time                    `json:"created_at"`
	UpdatedAt      time.Time                    `json:"updated_at"`
	ProcessedAt    *time.Time                   `json:"processed_at,omitempty"`
}
