package domain

import "time"

type AICall struct {
	PartitionIDs      []string `json:"partition_ids"`
	ID                string   `json:"id"`
	UserID            string   `json:"-"`
	WorkspaceID       string   `json:"workspace_id"`
	PartitionID       string   `json:"partition_id,omitempty"`
	Role              string   `json:"role"`
	ModelAlias        string   `json:"model_alias"`
	ModelVersion      string   `json:"model_version,omitempty"`
	TraceID           string   `json:"trace_id"`
	JobID             string   `json:"job_id,omitempty"`
	Attempt           int      `json:"attempt"`
	ProviderRequestID string   `json:"provider_request_id,omitempty"`
	BrokerUsageID     string   `json:"broker_usage_id,omitempty"`
	Status            string   `json:"status"`
	InputTokens       int      `json:"input_tokens"`
	OutputTokens      int      `json:"output_tokens"`
	// ReportedCost is a reference to the broker's charge, never an additional charge.
	ReportedCost   string    `json:"reported_cost,omitempty"`
	Currency       string    `json:"currency,omitempty"`
	UsageAuthority string    `json:"usage_authority"`
	FailureCode    string    `json:"failure_code,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type EmbeddingIdentity struct {
	ModelVersion string `json:"model_version"`
	Dimensions   int    `json:"dimensions"`
	Fingerprint  string `json:"fingerprint"`
}
