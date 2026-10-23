package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

type DatasetFormat string

const (
	DatasetFormatRepoNative  DatasetFormat = "repo-native"
	DatasetFormatLOCOMO      DatasetFormat = "locomo"
	DatasetFormatLongMemEval DatasetFormat = "longmemeval"
)

type DatasetLoadOptions struct {
	LOCOMO      LOCOMOOptions      `json:"locomo,omitempty"`
	LongMemEval LongMemEvalOptions `json:"longmemeval,omitempty"`
}

type Dataset struct {
	Aliases           []domain.EntityAlias `json:"aliases,omitempty"`
	Name              string               `json:"name"`
	Description       string               `json:"description,omitempty"`
	SmokeExpectations SmokeExpectations    `json:"smoke_expectations,omitempty"`
	Documents         []DatasetDocument    `json:"documents"`
	Relations         []DatasetRelation    `json:"relations,omitempty"`
	Queries           []QueryCase          `json:"queries"`
}

type SmokeExpectations struct {
	MinRecallAt3             float64 `json:"min_recall_at_3,omitempty"`
	MinMRR                   float64 `json:"min_mrr,omitempty"`
	MinNDCGAt5               float64 `json:"min_ndcg_at_5,omitempty"`
	MaxNegativeLeakage       float64 `json:"max_negative_leakage,omitempty"`
	MaxWrongUserLeakage      float64 `json:"max_wrong_user_leakage,omitempty"`
	MaxStaleFactTop3Rate     float64 `json:"max_stale_fact_top_3_rate,omitempty"`
	MinRelationExpansionGain float64 `json:"min_relation_expansion_gain,omitempty"`
}

type DatasetDocument struct {
	ID             string            `json:"id"`
	NodeID         string            `json:"node_id"`
	GenerationID   string            `json:"generation_id,omitempty"`
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
	GenerationID string         `json:"generation_id,omitempty"`
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
	AsOf                *time.Time          `json:"as_of,omitempty"`
	RelationMode        string              `json:"relation_mode,omitempty"`
	EntityScope         string              `json:"entity_scope,omitempty"`
	CaseID              string              `json:"case_id"`
	UserID              string              `json:"user_id"`
	GenerationID        string              `json:"generation_id,omitempty"`
	Query               string              `json:"query"`
	TopN                int                 `json:"top_n,omitempty"`
	IncludeConversation bool                `json:"include_conversation,omitempty"`
	IncludeSegments     bool                `json:"include_segments,omitempty"`
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
	return LoadDatasetWithOptions(path, format, DatasetLoadOptions{
		LOCOMO: LOCOMOOptions{
			SkipCategoryFive: true,
		},
		LongMemEval: LongMemEvalOptions{
			Granularity: LongMemEvalGranularitySession,
		},
	})
}

func LoadDatasetWithOptions(path string, format DatasetFormat, options DatasetLoadOptions) (Dataset, error) {
	switch format {
	case DatasetFormatRepoNative:
		return loadRepoNativeDataset(path)
	case DatasetFormatLOCOMO:
		return LoadLOCOMODataset(path, options.LOCOMO)
	case DatasetFormatLongMemEval:
		return LoadLongMemEvalDataset(path, options.LongMemEval)
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
	docUsers := map[string]string{}
	nodeUsers := map[string]string{}
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
		docUsers[doc.ID] = doc.UserID
		if _, exists := nodeIDs[doc.NodeID]; exists {
			return fmt.Errorf("dataset %q node_id %q is duplicated", d.Name, doc.NodeID)
		}
		nodeIDs[doc.NodeID] = struct{}{}
		nodeUsers[doc.NodeID] = doc.UserID
	}

	for _, relation := range d.Relations {
		if strings.TrimSpace(relation.UserID) == "" {
			return fmt.Errorf("dataset %q relation is missing user_id", d.Name)
		}
		if _, ok := nodeIDs[relation.SrcNodeID]; !ok {
			return fmt.Errorf("dataset %q relation source node %q does not exist", d.Name, relation.SrcNodeID)
		}
		if _, ok := nodeIDs[relation.DstNodeID]; !ok {
			return fmt.Errorf("dataset %q relation destination node %q does not exist", d.Name, relation.DstNodeID)
		}
		// Relations are tenant scoped. Refusing cross-user edges here prevents
		// a graph benchmark from masking a production isolation defect.
		if relation.UserID != nodeUsers[relation.SrcNodeID] || relation.UserID != nodeUsers[relation.DstNodeID] {
			return fmt.Errorf("dataset %q relation %q crosses user boundary", d.Name, relation.ID)
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
		if query.GradedRelevance != nil {
			for id, relevance := range query.GradedRelevance {
				if _, ok := docIDs[id]; !ok || relevance < 0 || math.IsNaN(relevance) || math.IsInf(relevance, 0) {
					return fmt.Errorf("dataset %q query %q has invalid graded relevance for %q", d.Name, query.CaseID, id)
				}
			}
		}
		seenPositive := map[string]struct{}{}
		for _, id := range query.Positives {
			if _, duplicate := seenPositive[id]; duplicate {
				return fmt.Errorf("dataset %q query %q repeats positive %q", d.Name, query.CaseID, id)
			}
			seenPositive[id] = struct{}{}
			if docUsers[id] != query.UserID {
				return fmt.Errorf("dataset %q query %q positive %q belongs to another user", d.Name, query.CaseID, id)
			}
		}
		for id, relevance := range query.GradedRelevance {
			_, positive := seenPositive[id]
			if (relevance > 0) != positive {
				return fmt.Errorf("dataset %q query %q has inconsistent positive and graded relevance for %q", d.Name, query.CaseID, id)
			}
		}
		for _, id := range query.Negatives {
			if _, positive := seenPositive[id]; positive {
				return fmt.Errorf("dataset %q query %q marks %q as both positive and negative", d.Name, query.CaseID, id)
			}
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
		GenerationID:   doc.GenerationID,
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
		GenerationID: relation.GenerationID,
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
