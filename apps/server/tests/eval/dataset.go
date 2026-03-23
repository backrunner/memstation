package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

type DatasetFormat string

const (
	DatasetFormatRepoNative DatasetFormat = "repo-native"
	DatasetFormatLOCOMO     DatasetFormat = "locomo"
)

type Dataset struct {
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	SmokeExpectations SmokeExpectations `json:"smoke_expectations,omitempty"`
	Documents         []DatasetDocument `json:"documents"`
	Relations         []DatasetRelation `json:"relations,omitempty"`
	Queries           []QueryCase       `json:"queries"`
}

type SmokeExpectations struct {
	MinRecallAt3             float64 `json:"min_recall_at_3,omitempty"`
	MinMRR                   float64 `json:"min_mrr,omitempty"`
	MinNDCGAt5               float64 `json:"min_ndcg_at_5,omitempty"`
	MaxWrongUserLeakage      float64 `json:"max_wrong_user_leakage,omitempty"`
	MaxStaleFactTop3Rate     float64 `json:"max_stale_fact_top_3_rate,omitempty"`
	MinRelationExpansionGain float64 `json:"min_relation_expansion_gain,omitempty"`
}

type DatasetDocument struct {
	ID             string            `json:"id"`
	NodeID         string            `json:"node_id"`
	UserID         string            `json:"user_id"`
	ConversationID string            `json:"conversation_id,omitempty"`
	Type           domain.MemoryType `json:"type"`
	Content        string            `json:"content"`
	Importance     float64           `json:"importance,omitempty"`
	DecayFactor    float64           `json:"decay_factor,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
	CreatedAt      time.Time         `json:"created_at,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at,omitempty"`
}

type DatasetRelation struct {
	ID           string         `json:"id,omitempty"`
	UserID       string         `json:"user_id"`
	SrcNodeID    string         `json:"src_node_id"`
	DstNodeID    string         `json:"dst_node_id"`
	RelationType string         `json:"relation_type"`
	Weight       float64        `json:"weight,omitempty"`
	Source       string         `json:"source,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at,omitempty"`
}

type QueryCase struct {
	CaseID              string              `json:"case_id"`
	UserID              string              `json:"user_id"`
	Query               string              `json:"query"`
	TopN                int                 `json:"top_n,omitempty"`
	MemoryTypes         []domain.MemoryType `json:"memory_types,omitempty"`
	Positives           []string            `json:"positives"`
	Negatives           []string            `json:"negatives,omitempty"`
	GradedRelevance     map[string]float64  `json:"graded_relevance,omitempty"`
	StaleDocumentIDs    []string            `json:"stale_document_ids,omitempty"`
	RelationPositiveIDs []string            `json:"relation_positive_ids,omitempty"`
	Metadata            map[string]any      `json:"metadata,omitempty"`
	Notes               string              `json:"notes,omitempty"`
}

func LoadDataset(path string, format DatasetFormat) (Dataset, error) {
	switch format {
	case DatasetFormatRepoNative:
		return loadRepoNativeDataset(path)
	case DatasetFormatLOCOMO:
		return LoadLOCOMODataset(path, LOCOMOOptions{SkipCategoryFive: true})
	default:
		return Dataset{}, fmt.Errorf("unsupported dataset format %q", format)
	}
}

func loadRepoNativeDataset(path string) (Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Dataset{}, err
	}
	var dataset Dataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		return Dataset{}, err
	}
	if err := dataset.Validate(); err != nil {
		return Dataset{}, err
	}
	return dataset, nil
}

func (d Dataset) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("dataset name is required")
	}
	if len(d.Documents) == 0 {
		return fmt.Errorf("dataset %q must contain at least one document", d.Name)
	}
	if len(d.Queries) == 0 {
		return fmt.Errorf("dataset %q must contain at least one query", d.Name)
	}

	docIDs := map[string]struct{}{}
	nodeIDs := map[string]struct{}{}
	for _, doc := range d.Documents {
		if strings.TrimSpace(doc.ID) == "" {
			return fmt.Errorf("dataset %q contains document without id", d.Name)
		}
		if strings.TrimSpace(doc.NodeID) == "" {
			return fmt.Errorf("dataset %q document %q missing node_id", d.Name, doc.ID)
		}
		if strings.TrimSpace(doc.UserID) == "" {
			return fmt.Errorf("dataset %q document %q missing user_id", d.Name, doc.ID)
		}
		if strings.TrimSpace(doc.Content) == "" {
			return fmt.Errorf("dataset %q document %q missing content", d.Name, doc.ID)
		}
		if _, exists := docIDs[doc.ID]; exists {
			return fmt.Errorf("dataset %q document id %q is duplicated", d.Name, doc.ID)
		}
		docIDs[doc.ID] = struct{}{}
		nodeIDs[doc.NodeID] = struct{}{}
	}

	for _, relation := range d.Relations {
		if _, ok := nodeIDs[relation.SrcNodeID]; !ok {
			return fmt.Errorf("dataset %q relation source node %q does not exist", d.Name, relation.SrcNodeID)
		}
		if _, ok := nodeIDs[relation.DstNodeID]; !ok {
			return fmt.Errorf("dataset %q relation destination node %q does not exist", d.Name, relation.DstNodeID)
		}
	}

	caseIDs := map[string]struct{}{}
	for _, query := range d.Queries {
		if strings.TrimSpace(query.CaseID) == "" {
			return fmt.Errorf("dataset %q contains query without case_id", d.Name)
		}
		if _, exists := caseIDs[query.CaseID]; exists {
			return fmt.Errorf("dataset %q query case_id %q is duplicated", d.Name, query.CaseID)
		}
		caseIDs[query.CaseID] = struct{}{}
		if strings.TrimSpace(query.UserID) == "" {
			return fmt.Errorf("dataset %q query %q missing user_id", d.Name, query.CaseID)
		}
		if strings.TrimSpace(query.Query) == "" {
			return fmt.Errorf("dataset %q query %q missing query text", d.Name, query.CaseID)
		}
		if len(query.Positives) == 0 {
			return fmt.Errorf("dataset %q query %q must define positives", d.Name, query.CaseID)
		}
		for _, id := range append(append([]string{}, query.Positives...), append(query.Negatives, query.StaleDocumentIDs...)...) {
			if _, ok := docIDs[id]; !ok {
				return fmt.Errorf("dataset %q query %q references unknown document %q", d.Name, query.CaseID, id)
			}
		}
		for _, id := range query.RelationPositiveIDs {
			if _, ok := docIDs[id]; !ok {
				return fmt.Errorf("dataset %q query %q references unknown relation-positive document %q", d.Name, query.CaseID, id)
			}
		}
	}

	return nil
}

func (d Dataset) SortedQueries() []QueryCase {
	out := append([]QueryCase{}, d.Queries...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CaseID < out[j].CaseID
	})
	return out
}

func (doc DatasetDocument) ToDomain() domain.MemoryDocument {
	return domain.MemoryDocument{
		ID:             doc.ID,
		NodeID:         doc.NodeID,
		UserID:         doc.UserID,
		ConversationID: doc.ConversationID,
		Type:           doc.Type,
		Content:        doc.Content,
		Importance:     nonZero(doc.Importance, 1),
		DecayFactor:    nonZero(doc.DecayFactor, 1),
		Metadata:       doc.Metadata,
		CreatedAt:      doc.CreatedAt,
		UpdatedAt:      doc.UpdatedAt,
	}
}

func (relation DatasetRelation) ToDomain() domain.MemoryRelation {
	return domain.MemoryRelation{
		ID:           relation.ID,
		UserID:       relation.UserID,
		SrcNodeID:    relation.SrcNodeID,
		DstNodeID:    relation.DstNodeID,
		RelationType: relation.RelationType,
		Weight:       nonZero(relation.Weight, 1),
		Source:       relation.Source,
		Metadata:     relation.Metadata,
		CreatedAt:    relation.CreatedAt,
	}
}

func nonZero(value, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	return value
}
