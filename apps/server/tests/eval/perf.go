package eval

import (
	"context"
	"fmt"
	"sort"
	"time"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

type PerformanceOptions struct {
	Variant    memoryservice.SearchVariant `json:"variant,omitempty"`
	Iterations int                         `json:"iterations,omitempty"`
	TopN       int                         `json:"top_n,omitempty"`
	Embedding  EmbeddingOptions            `json:"embedding,omitempty"`
}

type PerformanceReport struct {
	Dataset    string                      `json:"dataset"`
	Embedding  EmbeddingRunInfo            `json:"embedding"`
	Variant    memoryservice.SearchVariant `json:"variant"`
	Iterations int                         `json:"iterations"`
	QueryCount int                         `json:"query_count"`
	Samples    int                         `json:"samples"`
	P50Millis  float64                     `json:"p50_millis"`
	P95Millis  float64                     `json:"p95_millis"`
	MeanMillis float64                     `json:"mean_millis"`
	MaxMillis  float64                     `json:"max_millis"`
}

func RunPerformanceSmoke(ctx context.Context, dataset Dataset, options PerformanceOptions) (PerformanceReport, error) {
	if err := dataset.Validate(); err != nil {
		return PerformanceReport{}, err
	}
	variant := options.Variant
	if variant == "" {
		variant = memoryservice.SearchVariantFull
	}
	iterations := options.Iterations
	if iterations <= 0 {
		iterations = 25
	}
	bench, err := newHarness(ctx, dataset, options.Embedding)
	if err != nil {
		return PerformanceReport{}, err
	}
	queries := dataset.SortedQueries()
	samples := make([]float64, 0, iterations*len(queries))
	topN := options.TopN
	for i := 0; i < iterations; i++ {
		for _, query := range queries {
			limit := query.TopN
			if limit <= 0 && topN > 0 {
				limit = topN
			}
			if limit <= 0 {
				limit = 10
			}
			start := time.Now()
			if _, err := bench.engine.SearchWithVariant(ctx, domain.SearchInput{
				UserID:       query.UserID,
				GenerationID: query.GenerationID,
				Query:        query.Query,
				TopN:         limit,
				MemoryTypes:  query.MemoryTypes,
			}, variant); err != nil {
				return PerformanceReport{}, fmt.Errorf("performance case %s: %w", query.CaseID, err)
			}
			samples = append(samples, float64(time.Since(start).Microseconds())/1000)
		}
	}
	if len(samples) == 0 {
		return PerformanceReport{}, fmt.Errorf("no performance samples were collected")
	}
	sort.Float64s(samples)
	mean := 0.0
	for _, item := range samples {
		mean += item
	}
	mean /= float64(len(samples))
	return PerformanceReport{
		Dataset:    dataset.Name,
		Embedding:  bench.embedding,
		Variant:    variant,
		Iterations: iterations,
		QueryCount: len(queries),
		Samples:    len(samples),
		P50Millis:  percentile(samples, 0.50),
		P95Millis:  percentile(samples, 0.95),
		MeanMillis: mean,
		MaxMillis:  samples[len(samples)-1],
	}, nil
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return values[0]
	}
	if p >= 1 {
		return values[len(values)-1]
	}
	index := p * float64(len(values)-1)
	lower := int(index)
	upper := lower + 1
	if upper >= len(values) {
		return values[lower]
	}
	weight := index - float64(lower)
	return values[lower] + (values[upper]-values[lower])*weight
}
