package eval

import (
	"math"
	"sort"
	"strings"
)

type AggregateMetrics struct {
	QueryCount              int     `json:"query_count"`
	RecallAt1               float64 `json:"recall_at_1"`
	RecallAt3               float64 `json:"recall_at_3"`
	RecallAt5               float64 `json:"recall_at_5"`
	RecallAt10              float64 `json:"recall_at_10"`
	PrecisionAt3            float64 `json:"precision_at_3"`
	PrecisionAt5            float64 `json:"precision_at_5"`
	HitAt1                  float64 `json:"hit_at_1"`
	HitAt3                  float64 `json:"hit_at_3"`
	HitAt5                  float64 `json:"hit_at_5"`
	MRR                     float64 `json:"mrr"`
	NDCGAt5                 float64 `json:"ndcg_at_5"`
	NDCGAt10                float64 `json:"ndcg_at_10"`
	NegativeLeakage         float64 `json:"negative_leakage"`
	WrongUserLeakage        float64 `json:"wrong_user_leakage"`
	StaleFactTop3Rate       float64 `json:"stale_fact_top_3_rate"`
	StaleFactTopKRate       float64 `json:"stale_fact_top_k_rate"`
	RelationPositiveHitRate float64 `json:"relation_positive_hit_rate"`
}

type CaseMetrics struct {
	RecallAt1               float64  `json:"recall_at_1"`
	RecallAt3               float64  `json:"recall_at_3"`
	RecallAt5               float64  `json:"recall_at_5"`
	RecallAt10              float64  `json:"recall_at_10"`
	PrecisionAt3            float64  `json:"precision_at_3"`
	PrecisionAt5            float64  `json:"precision_at_5"`
	HitAt1                  bool     `json:"hit_at_1"`
	HitAt3                  bool     `json:"hit_at_3"`
	HitAt5                  bool     `json:"hit_at_5"`
	MRR                     float64  `json:"mrr"`
	NDCGAt5                 float64  `json:"ndcg_at_5"`
	NDCGAt10                float64  `json:"ndcg_at_10"`
	NegativeLeak            bool     `json:"negative_leak"`
	WrongUserLeak           bool     `json:"wrong_user_leak"`
	StaleFactTop3Rate       float64  `json:"stale_fact_top_3_rate"`
	StaleFactTopKRate       float64  `json:"stale_fact_top_k_rate"`
	RelationPositiveHitRate float64  `json:"relation_positive_hit_rate"`
	MissingPositiveIDs      []string `json:"missing_positive_ids,omitempty"`
	UnexpectedNegativeIDs   []string `json:"unexpected_negative_ids,omitempty"`
}

type VariantComparison struct {
	BaselineVariant        string  `json:"baseline_variant"`
	CandidateVariant       string  `json:"candidate_variant"`
	RecallAt3Delta         float64 `json:"recall_at_3_delta"`
	MRRDelta               float64 `json:"mrr_delta"`
	NDCGAt5Delta           float64 `json:"ndcg_at_5_delta"`
	RelationPositiveGain   float64 `json:"relation_positive_gain"`
	NegativeLeakageDelta   float64 `json:"negative_leakage_delta"`
	WrongUserLeakageDelta  float64 `json:"wrong_user_leakage_delta"`
	StaleFactTop3RateDelta float64 `json:"stale_fact_top_3_rate_delta"`
}

func summarizeCases(cases []CaseReport) AggregateMetrics {
	if len(cases) == 0 {
		return AggregateMetrics{}
	}
	aggregate := AggregateMetrics{QueryCount: len(cases)}
	for _, item := range cases {
		aggregate.RecallAt1 += item.Metrics.RecallAt1
		aggregate.RecallAt3 += item.Metrics.RecallAt3
		aggregate.RecallAt5 += item.Metrics.RecallAt5
		aggregate.RecallAt10 += item.Metrics.RecallAt10
		aggregate.PrecisionAt3 += item.Metrics.PrecisionAt3
		aggregate.PrecisionAt5 += item.Metrics.PrecisionAt5
		aggregate.MRR += item.Metrics.MRR
		aggregate.NDCGAt5 += item.Metrics.NDCGAt5
		aggregate.NDCGAt10 += item.Metrics.NDCGAt10
		aggregate.StaleFactTop3Rate += item.Metrics.StaleFactTop3Rate
		aggregate.StaleFactTopKRate += item.Metrics.StaleFactTopKRate
		aggregate.RelationPositiveHitRate += item.Metrics.RelationPositiveHitRate
		if item.Metrics.NegativeLeak {
			aggregate.NegativeLeakage += 1
		}
		if item.Metrics.HitAt1 {
			aggregate.HitAt1 += 1
		}
		if item.Metrics.HitAt3 {
			aggregate.HitAt3 += 1
		}
		if item.Metrics.HitAt5 {
			aggregate.HitAt5 += 1
		}
		if item.Metrics.WrongUserLeak {
			aggregate.WrongUserLeakage += 1
		}
	}
	divisor := float64(len(cases))
	aggregate.RecallAt1 /= divisor
	aggregate.RecallAt3 /= divisor
	aggregate.RecallAt5 /= divisor
	aggregate.RecallAt10 /= divisor
	aggregate.PrecisionAt3 /= divisor
	aggregate.PrecisionAt5 /= divisor
	aggregate.HitAt1 /= divisor
	aggregate.HitAt3 /= divisor
	aggregate.HitAt5 /= divisor
	aggregate.MRR /= divisor
	aggregate.NDCGAt5 /= divisor
	aggregate.NDCGAt10 /= divisor
	aggregate.NegativeLeakage /= divisor
	aggregate.WrongUserLeakage /= divisor
	aggregate.StaleFactTop3Rate /= divisor
	aggregate.StaleFactTopKRate /= divisor
	aggregate.RelationPositiveHitRate /= divisor
	return aggregate
}

func compareVariants(baseline, candidate VariantReport) VariantComparison {
	return VariantComparison{
		BaselineVariant:        string(baseline.Variant),
		CandidateVariant:       string(candidate.Variant),
		RecallAt3Delta:         candidate.Aggregate.RecallAt3 - baseline.Aggregate.RecallAt3,
		MRRDelta:               candidate.Aggregate.MRR - baseline.Aggregate.MRR,
		NDCGAt5Delta:           candidate.Aggregate.NDCGAt5 - baseline.Aggregate.NDCGAt5,
		RelationPositiveGain:   candidate.Aggregate.RelationPositiveHitRate - baseline.Aggregate.RelationPositiveHitRate,
		NegativeLeakageDelta:   candidate.Aggregate.NegativeLeakage - baseline.Aggregate.NegativeLeakage,
		WrongUserLeakageDelta:  candidate.Aggregate.WrongUserLeakage - baseline.Aggregate.WrongUserLeakage,
		StaleFactTop3RateDelta: candidate.Aggregate.StaleFactTop3Rate - baseline.Aggregate.StaleFactTop3Rate,
	}
}

func computeCaseMetrics(query QueryCase, results []RankedResult) CaseMetrics {
	positives := make(map[string]struct{}, len(query.Positives))
	for _, id := range query.Positives {
		positives[id] = struct{}{}
	}
	negatives := make(map[string]struct{}, len(query.Negatives))
	for _, id := range query.Negatives {
		negatives[id] = struct{}{}
	}
	stale := make(map[string]struct{}, len(query.StaleDocumentIDs))
	for _, id := range query.StaleDocumentIDs {
		stale[id] = struct{}{}
	}
	relationPositives := make(map[string]struct{}, len(query.RelationPositiveIDs))
	for _, id := range query.RelationPositiveIDs {
		relationPositives[id] = struct{}{}
	}

	metrics := CaseMetrics{}
	metrics.RecallAt1 = recallAt(results, positives, 1)
	metrics.RecallAt3 = recallAt(results, positives, 3)
	metrics.RecallAt5 = recallAt(results, positives, 5)
	metrics.RecallAt10 = recallAt(results, positives, 10)
	metrics.PrecisionAt3 = precisionAt(results, positives, 3)
	metrics.PrecisionAt5 = precisionAt(results, positives, 5)
	metrics.HitAt1 = hitAt(results, positives, 1)
	metrics.HitAt3 = hitAt(results, positives, 3)
	metrics.HitAt5 = hitAt(results, positives, 5)
	metrics.MRR = reciprocalRank(results, positives)
	metrics.NDCGAt5 = ndcgAt(results, query.GradedRelevance, positives, 5)
	metrics.NDCGAt10 = ndcgAt(results, query.GradedRelevance, positives, 10)
	metrics.NegativeLeak = hasUnexpectedNegatives(results, negatives)
	metrics.WrongUserLeak = wrongUserLeak(results, query.UserID)
	metrics.StaleFactTop3Rate = staleRateAt(results, stale, 3)
	metrics.StaleFactTopKRate = staleRateAt(results, stale, minInt(maxInt(query.TopN, 5), 10))
	metrics.RelationPositiveHitRate = relationHitRate(results, relationPositives)
	metrics.MissingPositiveIDs = missingPositives(results, positives)
	metrics.UnexpectedNegativeIDs = unexpectedNegatives(results, negatives)
	return metrics
}

func recallAt(results []RankedResult, positives map[string]struct{}, limit int) float64 {
	if len(positives) == 0 {
		return 0
	}
	hits := countMatches(results, positives, limit)
	return float64(hits) / float64(len(positives))
}

func precisionAt(results []RankedResult, positives map[string]struct{}, limit int) float64 {
	if limit <= 0 {
		return 0
	}
	hits := countMatches(results, positives, limit)
	return float64(hits) / float64(limit)
}

func hitAt(results []RankedResult, positives map[string]struct{}, limit int) bool {
	return countMatches(results, positives, limit) > 0
}

func countMatches(results []RankedResult, wanted map[string]struct{}, limit int) int {
	if len(wanted) == 0 || limit <= 0 {
		return 0
	}
	if limit > len(results) {
		limit = len(results)
	}
	hits := 0
	seen := make(map[string]struct{}, limit)
	for idx := 0; idx < limit; idx++ {
		id := results[idx].Document.ID
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		if _, ok := wanted[id]; ok {
			hits++
		}
	}
	return hits
}

func reciprocalRank(results []RankedResult, positives map[string]struct{}) float64 {
	for idx, item := range results {
		if _, ok := positives[item.Document.ID]; ok {
			return 1 / float64(idx+1)
		}
	}
	return 0
}

func ndcgAt(results []RankedResult, graded map[string]float64, positives map[string]struct{}, limit int) float64 {
	if len(positives) == 0 || limit <= 0 {
		return 0
	}
	dcg := 0.0
	seen := make(map[string]struct{})
	for idx := 0; idx < limit && idx < len(results); idx++ {
		id := results[idx].Document.ID
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		rel := relevanceFor(id, graded, positives)
		if rel == 0 {
			continue
		}
		dcg += rel / math.Log2(float64(idx)+2)
	}
	ideal := make([]float64, 0, len(positives))
	for id := range positives {
		ideal = append(ideal, relevanceFor(id, graded, positives))
	}
	sort.Slice(ideal, func(i, j int) bool {
		return ideal[i] > ideal[j]
	})
	idcg := 0.0
	for idx := 0; idx < limit && idx < len(ideal); idx++ {
		idcg += ideal[idx] / math.Log2(float64(idx)+2)
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

func relevanceFor(id string, graded map[string]float64, positives map[string]struct{}) float64 {
	if graded != nil {
		if value, ok := graded[id]; ok {
			return value
		}
	}
	if _, ok := positives[id]; ok {
		return 1
	}
	return 0
}

func wrongUserLeak(results []RankedResult, userID string) bool {
	for _, item := range results {
		// A result without an owner is unsafe too: it cannot be proven to belong
		// to the query principal and would be rejected by the production store.
		if strings.TrimSpace(item.Document.UserID) == "" || item.Document.UserID != userID {
			return true
		}
	}
	return false
}

func staleRateAt(results []RankedResult, stale map[string]struct{}, limit int) float64 {
	if len(stale) == 0 || limit <= 0 {
		return 0
	}
	if limit > len(results) {
		limit = len(results)
	}
	if limit == 0 {
		return 0
	}
	count := 0
	for idx := 0; idx < limit; idx++ {
		if _, ok := stale[results[idx].Document.ID]; ok {
			count++
		}
	}
	return float64(count) / float64(limit)
}

func relationHitRate(results []RankedResult, relationPositives map[string]struct{}) float64 {
	if len(relationPositives) == 0 {
		return 0
	}
	found := countMatches(results, relationPositives, len(results))
	return float64(found) / float64(len(relationPositives))
}

func missingPositives(results []RankedResult, positives map[string]struct{}) []string {
	if len(positives) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for _, item := range results {
		seen[item.Document.ID] = struct{}{}
	}
	missing := make([]string, 0)
	for id := range positives {
		if _, ok := seen[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return missing
}

func unexpectedNegatives(results []RankedResult, negatives map[string]struct{}) []string {
	if len(negatives) == 0 {
		return nil
	}
	found := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range results {
		if _, ok := negatives[item.Document.ID]; ok {
			if _, duplicate := seen[item.Document.ID]; duplicate {
				continue
			}
			seen[item.Document.ID] = struct{}{}
			found = append(found, item.Document.ID)
		}
	}
	sort.Strings(found)
	return found
}

func hasUnexpectedNegatives(results []RankedResult, negatives map[string]struct{}) bool {
	return len(unexpectedNegatives(results, negatives)) > 0
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
