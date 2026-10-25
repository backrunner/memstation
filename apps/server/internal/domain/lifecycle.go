package domain

import "time"

type MemoryCandidate struct {
	ID              string      `json:"id"`
	WorkspaceID     string      `json:"workspace_id"`
	PartitionID     string      `json:"partition_id,omitempty"`
	AgentID         string      `json:"agent_id"`
	Kind            string      `json:"kind"`
	Content         string      `json:"content"`
	SubjectActorIDs []string    `json:"subject_actor_ids"`
	SourceRefs      []SourceRef `json:"source_refs"`
	Origin          string      `json:"origin"`
	Status          string      `json:"status"`
	Revision        int64       `json:"revision"`
	TargetNodeID    string      `json:"target_node_id,omitempty"`
	TargetRevision  int64       `json:"target_revision,omitempty"`
	ResultNodeID    string      `json:"result_node_id,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}
type CandidateDecision struct {
	ConfirmedKind    string `json:"confirmed_kind,omitempty"`
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Decision         string `json:"decision"`
}
type ForgetSelector struct {
	PartitionID string      `json:"partition_id,omitempty"`
	SourceRefs  []SourceRef `json:"source_refs,omitempty"`
	NodeIDs     []string    `json:"node_ids,omitempty"`
	ActorID     string      `json:"actor_id,omitempty"`
	Workspace   bool        `json:"workspace,omitempty"`
}
type MemoryOperation struct {
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	PartitionID      string     `json:"partition_id,omitempty"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	Revision         int64      `json:"revision"`
	ReadBlocked      bool       `json:"read_blocked"`
	OnlineCleaned    bool       `json:"online_cleaned"`
	JournalPersisted bool       `json:"journal_persisted"`
	BackupStatus     string     `json:"backup_status"`
	FailureCode      string     `json:"failure_code,omitempty"`
	Result           any        `json:"result,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
type PublicationInput struct {
	ReplacesPublicationID       string            `json:"replaces_publication_id,omitempty"`
	ExpectedPublicationRevision int64             `json:"expected_publication_revision,omitempty"`
	SourceNodeID                string            `json:"source_node_id"`
	SourceRevision              int64             `json:"source_revision"`
	TargetWorkspaceID           string            `json:"target_workspace_id"`
	TargetPartitionID           string            `json:"target_partition_id,omitempty"`
	ActorMapping                map[string]string `json:"actor_mapping"`
}
type MemoryPublication struct {
	OperationID       string `json:"operation_id,omitempty"`
	ID                string `json:"id"`
	SourceWorkspaceID string `json:"source_workspace_id"`
	SourceNodeID      string `json:"source_node_id"`
	SourceRevision    int64  `json:"source_revision"`
	TargetWorkspaceID string `json:"target_workspace_id"`
	TargetPartitionID string `json:"target_partition_id,omitempty"`
	TargetNodeID      string `json:"target_node_id"`
	Status            string `json:"status"`
	Revision          int64  `json:"revision"`
}

type DeletionIntent struct {
	RequestHash     string         `json:"request_hash"`
	OperationID     string         `json:"operation_id"`
	UserID          string         `json:"user_id"`
	WorkspaceID     string         `json:"workspace_id"`
	PartitionID     string         `json:"partition_id"`
	Selector        ForgetSelector `json:"selector"`
	SourceIDs       []string       `json:"source_ids"`
	BlobIDs         []string       `json:"blob_ids"`
	NodeIDs         []string       `json:"node_ids"`
	ConversationIDs []string       `json:"conversation_ids"`
	Cutoff          time.Time      `json:"cutoff"`
}
type DeletionEntry struct {
	Sequence int64          `json:"sequence"`
	Digest   string         `json:"digest"`
	Intent   DeletionIntent `json:"intent"`
}

type MemoryRuntime struct {
	AIBackend      string            `json:"ai_backend"`
	RoleAliases    map[string]string `json:"role_aliases"`
	Embedding      EmbeddingIdentity `json:"embedding"`
	UsageAuthority string            `json:"usage_authority"`
}

type MemoryCapabilities struct {
	Runtime         MemoryRuntime   `json:"runtime"`
	ContractVersion string          `json:"contract_version"`
	ServerVersion   string          `json:"server_version"`
	Status          string          `json:"status"`
	Access          AccessContext   `json:"access"`
	Capabilities    map[string]bool `json:"capabilities"`
	Limitations     []string        `json:"limitations"`
}
