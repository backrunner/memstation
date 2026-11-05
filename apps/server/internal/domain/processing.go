package domain

// ProcessingPolicy is public runtime configuration; credentials and endpoints
// are never included in capability responses.
type ProcessingPolicy struct {
	EntityRecallTimeoutMS int     `json:"entity_recall_timeout_ms"`
	Strategy              string  `json:"strategy"`
	Model                 string  `json:"model"`
	Revision              string  `json:"revision"`
	Splitter              string  `json:"splitter"`
	Threshold             float64 `json:"threshold"`
	AuditRate             float64 `json:"audit_rate"`
	MaxSemanticBytes      int     `json:"max_semantic_bytes"`
	Identity              string  `json:"identity"`
}
