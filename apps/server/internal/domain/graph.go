package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// RelationQuery budgets apply while fetching adjacency lists, before hydration.
type RelationQuery struct {
	WorkspaceID     string `json:"workspace_id,omitempty"`
	UserID          string
	GenerationID    string
	NodeIDs         []string
	MaxHops         int
	MaxEdgesPerNode int
	MaxEdges        int
	MaxNodes        int
	Timeout         time.Duration
	MinWeight       float64
	Mode            string
}

func (q RelationQuery) Normalized() RelationQuery {
	q.MaxHops = boundedGraphInt(q.MaxHops, 2, 4)
	q.MaxEdgesPerNode = boundedGraphInt(q.MaxEdgesPerNode, 24, 64)
	q.MaxEdges = boundedGraphInt(q.MaxEdges, 256, 1024)
	q.MaxNodes = boundedGraphInt(q.MaxNodes, 128, 512)
	if q.Timeout <= 0 || q.Timeout > time.Second {
		q.Timeout = 150 * time.Millisecond
	}
	if q.MinWeight <= 0 {
		q.MinWeight = 0.05
	}
	if q.Mode == "" {
		q.Mode = "neighborhood"
	}
	return q
}

func boundedGraphInt(value, fallback, ceiling int) int {
	if value <= 0 {
		return fallback
	}
	return min(value, ceiling)
}

func ValidateRelationMode(mode string) error {
	switch mode {
	case "", "neighborhood", "evidence", "dependencies", "dependents", "conflicts":
		return nil
	}
	return fmt.Errorf("invalid relation_mode %q", mode)
}

func RelationTypesForMode(mode string) []string {
	switch mode {
	case "evidence":
		return []string{MemoryRelationSupports, MemoryRelationDerivedFrom, "summarized_into"}
	case "dependencies", "dependents":
		return []string{MemoryRelationDependsOn}
	case "conflicts":
		return []string{MemoryRelationContradicts}
	default:
		return []string{MemoryRelationDerivedFrom, MemoryRelationMentionsEntity, MemoryRelationUsedTool, MemoryRelationProducedArtifact, MemoryRelationResultedIn, MemoryRelationDependsOn, MemoryRelationSupports, "summarized_into", "promoted_to_key_memory", "related_to"}
	}
}

func RelationDestination(r MemoryRelation, from, mode string) string {
	if mode == "dependencies" && from == r.SrcNodeID {
		return r.DstNodeID
	}
	if mode == "dependents" && from == r.DstNodeID {
		return r.SrcNodeID
	}
	if mode == "dependencies" || mode == "dependents" {
		return ""
	}
	// Causal and dependency relations retain their semantic direction.
	if (mode == "neighborhood" || mode == "") && (r.RelationType == MemoryRelationDependsOn || r.RelationType == MemoryRelationResultedIn) {
		if from == r.SrcNodeID {
			return r.DstNodeID
		}
		return ""
	}
	if from == r.SrcNodeID {
		return r.DstNodeID
	}
	if from == r.DstNodeID {
		return r.SrcNodeID
	}
	return ""
}

func RelationConfidence(r MemoryRelation) float64 {
	value, ok := r.Metadata["confidence"]
	if !ok {
		return 1
	}
	confidence, ok := value.(float64)
	if !ok || math.IsNaN(confidence) || math.IsInf(confidence, 0) {
		return 0
	}
	return max(0, min(1, confidence))
}

type RecallPath struct {
	SeedNodeID    string   `json:"seed_node_id"`
	NodeIDs       []string `json:"node_ids"`
	RelationIDs   []string `json:"relation_ids"`
	RelationTypes []string `json:"relation_types"`
	Contribution  float64  `json:"contribution"`
}

// EntityAlias resolves names or joins two entities without rewriting either
// entity's source records. Revoking the mapping restores the original graph.
type EntityAlias struct {
	WorkspaceID  string    `json:"workspace_id,omitempty"`
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	GenerationID string    `json:"generation_id"`
	Scope        string    `json:"scope"`
	Alias        string    `json:"alias"`
	SourceNodeID string    `json:"source_node_id,omitempty"`
	TargetNodeID string    `json:"target_node_id"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func NormalizeEntityAlias(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func (a EntityAlias) Validate() error {
	if a.UserID == "" || a.GenerationID == "" || a.TargetNodeID == "" || NormalizeEntityAlias(a.Alias) == "" || strings.TrimSpace(a.Reason) == "" {
		return fmt.Errorf("entity alias requires user, generation, alias, target and reason")
	}
	if a.SourceNodeID == a.TargetNodeID {
		return fmt.Errorf("entity cannot merge into itself")
	}
	if len(a.Alias) > 256 || len(a.Scope) > 256 || len(a.Reason) > 2000 {
		return fmt.Errorf("entity alias exceeds field limit")
	}
	switch a.Status {
	case "candidate", "confirmed", "revoked":
		return nil
	}
	return fmt.Errorf("invalid entity alias status")
}
