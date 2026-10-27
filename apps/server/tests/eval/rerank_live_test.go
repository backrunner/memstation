package eval

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"slices"
	"sort"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	reranksvc "memstation/apps/server/internal/service/rerank"
)

// This manual diagnostic isolates the provider on frozen raw-dialogue candidates.
// It is not a MemoryEngine ranking ablation or an end-to-end LOCOMO score.
// Default CI does not call a model or impose a benchmark quality threshold.
func TestLiveRerankFrozenCandidates(t *testing.T) {
	source := os.Getenv("MEMSTATION_RERANK_PILOT_SOURCE")
	if source == "" {
		t.Skip("manual frozen-candidate diagnostic is not configured")
	}
	endpoint, model, output := os.Getenv("MEMSTATION_RERANK_TEST_URL"), os.Getenv("MEMSTATION_RERANK_TEST_MODEL"), os.Getenv("MEMSTATION_RERANK_TEST_OUTPUT")
	if endpoint == "" || model == "" || output == "" || source == output {
		t.Fatal("set a model, endpoint, and separate output artifact")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var parent LOCOMOPipelineReport
	if err := json.Unmarshal(raw, &parent); err != nil {
		t.Fatal(err)
	}
	service := reranksvc.NewService(provider.StaticConfigResolver{domain.ProviderRerankCompatible: {BaseURL: endpoint, APIKey: os.Getenv("MEMSTATION_RERANK_TEST_KEY")}}, "rerank_compatible:"+model, 60*time.Second)
	type metrics struct {
		Hit1    float64 `json:"hit_at_1"`
		Hit3    float64 `json:"hit_at_3"`
		Hit5    float64 `json:"hit_at_5"`
		MRR     float64 `json:"mrr"`
		Recall5 float64 `json:"recall_at_5"`
	}
	measure := func(docs []domain.MemoryDocument, refs []string) metrics {
		result := metrics{}
		seen := map[string]bool{}
		for i, d := range docs {
			ref, _ := d.Metadata["ref"].(string)
			if !slices.Contains(refs, ref) {
				continue
			}
			if result.MRR == 0 {
				result.MRR = 1 / float64(i+1)
			}
			if i < 1 {
				result.Hit1 = 1
			}
			if i < 3 {
				result.Hit3 = 1
			}
			if i < 5 {
				result.Hit5 = 1
				seen[ref] = true
			}
		}
		result.Recall5 = float64(len(seen)) / float64(len(refs))
		return result
	}
	add := func(total *metrics, value metrics) {
		total.Hit1 += value.Hit1
		total.Hit3 += value.Hit3
		total.Hit5 += value.Hit5
		total.MRR += value.MRR
		total.Recall5 += value.Recall5
	}
	before, after := metrics{}, metrics{}
	cases := []map[string]any{}
	durations := []float64{}
	for _, answer := range parent.Answers {
		if answer.Mode != "raw" || len(answer.EvidenceRefs) == 0 {
			continue
		}
		blob, err := json.Marshal(answer.Retrieval)
		if err != nil {
			t.Fatal(err)
		}
		var retrieval struct {
			Result domain.SearchResult `json:"result"`
		}
		if err := json.Unmarshal(blob, &retrieval); err != nil {
			t.Fatal(err)
		}
		docs := retrieval.Result.Memories
		input := domain.RerankInput{Query: answer.Query, TopN: len(docs)}
		for _, d := range docs {
			input.Documents = append(input.Documents, domain.RerankDocument{ID: d.NodeID, Text: "[" + string(d.Type) + "] " + d.Content})
		}
		start := time.Now()
		scores, err := service.Rerank(context.Background(), input)
		millis := float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			t.Fatal(err)
		}
		if len(scores) != len(docs) {
			t.Fatal("incomplete rerank response")
		}
		values := map[string]float64{}
		for _, score := range scores {
			values[score.DocumentID] = score.Score
		}
		sorted := slices.Clone(docs)
		sort.SliceStable(sorted, func(i, j int) bool { return values[sorted[i].NodeID] > values[sorted[j].NodeID] })
		baseline, ranked := measure(docs, answer.EvidenceRefs), measure(sorted, answer.EvidenceRefs)
		add(&before, baseline)
		add(&after, ranked)
		durations = append(durations, millis)
		cases = append(cases, map[string]any{"question_id": answer.QuestionID, "candidates": len(docs), "before": baseline, "after": ranked, "millis": millis, "scores": scores})
	}
	if len(cases) == 0 {
		t.Fatal("no eligible raw-dialogue cases")
	}
	count := float64(len(cases))
	for _, m := range []*metrics{&before, &after} {
		m.Hit1 /= count
		m.Hit3 /= count
		m.Hit5 /= count
		m.MRR /= count
		m.Recall5 /= count
	}
	slices.Sort(durations)
	report := map[string]any{"kind": "frozen_raw_candidates_provider_only", "parent_sha256": pipelineHash(string(raw)), "model": model, "queries": len(cases), "before": before, "after": after, "p95_millis": durations[int(math.Ceil(count*.95))-1], "cases": cases}
	if err := writePipelineJSON(output, report); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d frozen raw queries: before=%+v after=%+v p95=%.2fms", len(cases), before, after, report["p95_millis"])
}
