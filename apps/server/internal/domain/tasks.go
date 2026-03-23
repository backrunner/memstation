package domain

import "time"

type OutboxEvent struct {
	ID            string         `json:"id"`
	UserID        string         `json:"user_id"`
	EventType     string         `json:"event_type"`
	AggregateType string         `json:"aggregate_type"`
	AggregateID   string         `json:"aggregate_id"`
	Payload       map[string]any `json:"payload"`
	Status        string         `json:"status"`
	AvailableAt   time.Time      `json:"available_at"`
	ProcessedAt   *time.Time     `json:"processed_at,omitempty"`
	RetryCount    int            `json:"retry_count"`
	CreatedAt     time.Time      `json:"created_at"`
}

type TaskMessage struct {
	Type      string         `json:"type"`
	Queue     string         `json:"queue"`
	UniqueKey string         `json:"unique_key"`
	Payload   map[string]any `json:"payload"`
}

type AuditEvent struct {
	ID         string         `json:"id"`
	ActorType  string         `json:"actor_type"`
	ActorID    string         `json:"actor_id"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Payload    map[string]any `json:"payload,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}
