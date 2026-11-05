package domain

import (
	"errors"
	"strings"
)

var ErrEntityQueryBudget = errors.New("entity query exceeds input budget")

// Names are retrieval keys, never Actor IDs or asserted entity equivalences.
func EntityNameKey(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

type QueryEntities struct {
	Names    []string `json:"names"`
	CacheHit bool     `json:"cache_hit"`
}

type EntityNameMatch struct {
	NodeID      string
	Name        string
	Occurrences int
}

type EntityRecallDiagnostics struct {
	Status        string `json:"status"`
	CacheHit      bool   `json:"cache_hit"`
	QueryEntities int    `json:"query_entities"`
	MatchedNodes  int    `json:"matched_nodes"`
	LatencyMS     int64  `json:"latency_ms"`
}
