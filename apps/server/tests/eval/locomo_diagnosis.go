package eval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	memoryservice "memstation/apps/server/internal/service/memory"
)

type LOCOMODiagnosisOptions struct {
	DatasetPath      string
	OutputDir        string
	CacheDir         string
	ModelRevision    string
	SampleID         string
	QueryInstruction string
	Embedding        EmbeddingOptions
}

type LOCOMODiagnosisMode struct {
	Name                string                      `json:"name"`
	QueryInstruction    string                      `json:"query_instruction"`
	IncludeCaptions     bool                        `json:"include_captions"`
	Aggregate           AggregateMetrics            `json:"aggregate"`
	CandidateRecallAt40 float64                     `json:"candidate_recall_at_40"`
	ByCategory          map[string]AggregateMetrics `json:"by_category"`
}

type LOCOMODiagnosisReport struct {
	Coverage      *DatasetCoverage      `json:"coverage,omitempty"`
	GeneratedAt   time.Time             `json:"generated_at"`
	Embedding     EmbeddingRunInfo      `json:"embedding"`
	ModelRevision string                `json:"model_revision"`
	SampleID      string                `json:"sample_id,omitempty"`
	Documents     int                   `json:"documents"`
	Queries       int                   `json:"queries"`
	Modes         []LOCOMODiagnosisMode `json:"modes"`
}

// RunLOCOMODiagnosis isolates input-policy effects while keeping the production
// engine, evidence judgments and ranking unchanged. Cached vectors only reduce
// repeated model calls; these runs make no latency claims.
func RunLOCOMODiagnosis(ctx context.Context, options LOCOMODiagnosisOptions) (LOCOMODiagnosisReport, error) {
	if options.DatasetPath == "" || options.OutputDir == "" || options.CacheDir == "" || strings.TrimSpace(options.ModelRevision) == "" || strings.TrimSpace(options.QueryInstruction) == "" {
		return LOCOMODiagnosisReport{}, fmt.Errorf("diagnosis requires dataset, private output/cache directories, pinned model revision and query instruction")
	}
	embedding, err := normalizeEmbeddingOptions(options.Embedding)
	if err != nil {
		return LOCOMODiagnosisReport{}, err
	}
	if embedding.Mode != EmbeddingModeProvider {
		return LOCOMODiagnosisReport{}, fmt.Errorf("LOCOMO diagnosis requires real provider embeddings")
	}
	delegate, err := newEvalEmbeddingClient(embedding)
	if err != nil {
		return LOCOMODiagnosisReport{}, err
	}
	if err := os.MkdirAll(options.CacheDir, 0o700); err != nil {
		return LOCOMODiagnosisReport{}, err
	}
	if err := os.MkdirAll(options.OutputDir, 0o700); err != nil {
		return LOCOMODiagnosisReport{}, err
	}
	cache := &diagnosticEmbeddingCache{delegate: delegate, dir: options.CacheDir, scope: embedding.BaseURL + "\n" + embedding.Model + "\n" + options.ModelRevision}
	report := LOCOMODiagnosisReport{GeneratedAt: time.Now().UTC(), Embedding: embedding.runInfo(), ModelRevision: options.ModelRevision, SampleID: options.SampleID}
	var bench *harness
	for _, mode := range []struct {
		name        string
		instruction string
		captions    bool
	}{{"raw", "", false}, {"query_instruction", options.QueryInstruction, false}, {"query_instruction_and_captions", options.QueryInstruction, true}} {
		dataset, err := LoadLOCOMODataset(options.DatasetPath, LOCOMOOptions{SkipCategoryFive: true, IncludeCaptions: mode.captions})
		if err != nil {
			return report, err
		}
		if options.SampleID != "" {
			docs := dataset.Documents[:0]
			for _, d := range dataset.Documents {
				if d.UserID == locomoUserID(options.SampleID, "") {
					docs = append(docs, d)
				}
			}
			dataset.Documents = docs
			queries := dataset.Queries[:0]
			for _, q := range dataset.Queries {
				if q.Metadata["sample_id"] == options.SampleID {
					queries = append(queries, q)
				}
			}
			dataset.Queries = queries
		}
		if err := dataset.Validate(); err != nil || len(dataset.Queries) == 0 {
			return report, fmt.Errorf("invalid diagnosis selection: %v", err)
		}
		for i := range dataset.Queries {
			// Export the wider candidate pool as a diagnostic reranking ceiling.
			// All headline metrics still use their original k=1/3/5/10.
			dataset.Queries[i].TopN = 40
		}
		report.Documents, report.Queries = len(dataset.Documents), len(dataset.Queries)
		if options.SampleID == "" {
			report.Coverage = dataset.Coverage
		}
		if bench == nil || mode.captions {
			fmt.Printf("Indexing %s: %d documents, %d queries\n", mode.name, len(dataset.Documents), len(dataset.Queries))
			bench, err = newHarnessWithClient(ctx, dataset, embedding, cache)
			if err != nil {
				return report, err
			}
		}
		observed := &observedEmbeddingClient{EmbeddingClient: diagnosticQueryClient{delegate: cache, instruction: mode.instruction}}
		bench.observed = observed
		bench.vectors.SetEmbedder(observed)
		variant, _, err := runVariant(ctx, bench, dataset, memoryservice.SearchVariantFull, 40)
		if err != nil {
			return report, err
		}
		queries := dataset.SortedQueries()
		var failures []FailureSample
		for i := range variant.Cases {
			c := &variant.Cases[i]
			// Keep MRR, missing-positive and top-k exposure comparable with the
			// original top-10 run while separately exporting all 40 candidates.
			c.Metrics = computeCaseMetrics(queries[i], c.Results[:min(10, len(c.Results))])
			if failure := buildFailure(*c); failure != nil {
				failures = append(failures, *failure)
			}
		}
		variant.Aggregate = summarizeCases(variant.Cases)
		artifact := BenchmarkReport{Coverage: report.Coverage, Dataset: dataset.Name, Description: "Input-policy diagnosis; exported top-40 candidates, metrics limited to top-10, unchanged evidence-turn judgments; not a latency measurement", GeneratedAt: report.GeneratedAt, Embedding: report.Embedding, Variants: []VariantReport{variant}, Failures: failures}
		if err := WriteArtifacts(artifact, filepath.Join(options.OutputDir, mode.name)); err != nil {
			return report, err
		}
		groups := map[string][]CaseReport{}
		recall := 0.0
		for i, c := range variant.Cases {
			category := fmt.Sprint(c.Metadata["category"])
			groups[category] = append(groups[category], c)
			hits := 0
			for _, r := range c.Results {
				if r.Relevant {
					hits++
				}
			}
			recall += float64(hits) / float64(len(queries[i].Positives))
		}
		byCategory := map[string]AggregateMetrics{}
		for category, cases := range groups {
			byCategory[category] = summarizeCases(cases)
		}
		report.Modes = append(report.Modes, LOCOMODiagnosisMode{Name: mode.name, QueryInstruction: mode.instruction, IncludeCaptions: mode.captions, Aggregate: variant.Aggregate, CandidateRecallAt40: recall / float64(len(variant.Cases)), ByCategory: byCategory})
		if err := writeDiagnosisReport(options.OutputDir, report); err != nil {
			return report, err
		}
		fmt.Printf("%s: R@3=%.4f R@10=%.4f candidate R@40=%.4f\n", mode.name, variant.Aggregate.RecallAt3, variant.Aggregate.RecallAt10, recall/float64(len(variant.Cases)))
	}
	return report, nil
}

func writeDiagnosisReport(dir string, report LOCOMODiagnosisReport) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "diagnosis.json"), raw, 0o600)
}

type diagnosticQueryClient struct {
	delegate    ports.EmbeddingClient
	instruction string
}

func (c diagnosticQueryClient) Embed(ctx context.Context, input domain.EmbeddingInput) (domain.EmbeddingResult, error) {
	if c.instruction != "" {
		input.Text = "Instruct: " + c.instruction + "\nQuery: " + input.Text
	}
	return c.delegate.Embed(ctx, input)
}

type diagnosticEmbeddingCache struct {
	delegate ports.EmbeddingClient
	dir      string
	scope    string
	calls    int
}

func (c *diagnosticEmbeddingCache) Embed(ctx context.Context, input domain.EmbeddingInput) (domain.EmbeddingResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.EmbeddingResult{}, err
	}
	key := sha256.Sum256([]byte(c.scope + "\n" + input.Model + "\n" + input.Text))
	path := filepath.Join(c.dir, fmt.Sprintf("%x.json", key))
	if raw, err := os.ReadFile(path); err == nil {
		var result domain.EmbeddingResult
		if err := json.Unmarshal(raw, &result); err != nil || len(result.Vector) == 0 {
			return domain.EmbeddingResult{}, fmt.Errorf("invalid cached diagnostic vector")
		}
		return result, nil
	} else if !os.IsNotExist(err) {
		return domain.EmbeddingResult{}, err
	}
	result, err := c.delegate.Embed(ctx, input)
	if err != nil {
		return domain.EmbeddingResult{}, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return domain.EmbeddingResult{}, err
	}
	file, err := os.CreateTemp(c.dir, ".vector-*")
	if err != nil {
		return domain.EmbeddingResult{}, err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return domain.EmbeddingResult{}, err
	}
	if err := file.Close(); err != nil {
		return domain.EmbeddingResult{}, err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return domain.EmbeddingResult{}, err
	}
	c.calls++
	if c.calls%250 == 0 {
		fmt.Printf("Provider vectors computed: %d\n", c.calls)
	}
	return result, nil
}
