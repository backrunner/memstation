package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
	eval "memstation/apps/server/tests/eval"
)

func main() {
	var (
		datasetPath                 string
		format                      string
		outDir                      string
		variantsValue               string
		topN                        int
		enforceSmoke                bool
		perfSmoke                   bool
		perfIterations              int
		perfMaxP95                  float64
		embeddingModeValue          string
		embeddingModel              string
		embeddingBaseURL            string
		embeddingAPIKey             string
		embeddingTimeout            time.Duration
		locomoSkipCategoryFive      bool
		locomoIncludeCaptions       bool
		longMemEvalGranularityValue string
	)

	flag.StringVar(&datasetPath, "dataset", "", "Path to the benchmark dataset file.")
	flag.StringVar(&format, "format", string(eval.DatasetFormatRepoNative), "Dataset format: repo-native, locomo, or longmemeval.")
	flag.StringVar(&outDir, "out", "", "Output directory for benchmark artifacts.")
	flag.StringVar(&variantsValue, "variants", "full,full_no_relations,semantic_only,semantic_importance", "Comma-separated search variants.")
	flag.IntVar(&topN, "top-n", 10, "Default top-N for each query.")
	flag.BoolVar(&enforceSmoke, "enforce-smoke", false, "Fail when repo-native smoke expectations are not met.")
	flag.BoolVar(&perfSmoke, "perf-smoke", false, "Run retrieval latency smoke and export perf.json.")
	flag.IntVar(&perfIterations, "perf-iterations", 50, "Iterations used when performance smoke is enabled.")
	flag.Float64Var(&perfMaxP95, "perf-max-p95-ms", 0, "Optional maximum allowed p95 latency in milliseconds.")
	flag.StringVar(&embeddingModeValue, "embedding-mode", envOrDefault("EVAL_EMBEDDING_MODE", string(eval.EmbeddingModeDeterministic)), "Embedding mode: deterministic or provider.")
	flag.StringVar(&embeddingModel, "embedding-model", envOrDefault("EMBEDDING_MODEL", ""), "Embedding model used for provider-backed evals.")
	flag.StringVar(&embeddingBaseURL, "embedding-base-url", envOrDefault("EMBEDDING_BASE_URL", ""), "OpenAI-compatible embeddings endpoint base URL.")
	flag.StringVar(&embeddingAPIKey, "embedding-api-key", envOrDefault("EMBEDDING_API_KEY", ""), "API key used for provider-backed embeddings.")
	flag.DurationVar(&embeddingTimeout, "embedding-timeout", 60*time.Second, "Timeout for embedding requests in provider mode.")
	flag.BoolVar(&locomoSkipCategoryFive, "locomo-skip-category-five", true, "Skip LOCOMO category-5 questions for retrieval/ranking runs.")
	flag.BoolVar(&locomoIncludeCaptions, "locomo-include-captions", false, "Include marked machine-generated image captions in a separate LOCOMO input track.")
	flag.StringVar(&longMemEvalGranularityValue, "longmemeval-granularity", string(eval.LongMemEvalGranularitySession), "LongMemEval retrieval granularity: session or turn.")
	flag.Parse()

	formatValue := eval.DatasetFormat(strings.TrimSpace(format))
	if datasetPath == "" {
		switch formatValue {
		case eval.DatasetFormatRepoNative:
			datasetPath = filepath.Join("tests", "eval", "testdata", "repo_native_golden.json")
		case eval.DatasetFormatLOCOMO:
			datasetPath = filepath.Join("tests", "eval", "testdata", "locomo_fixture.json")
		case eval.DatasetFormatLongMemEval:
			datasetPath = filepath.Join("tests", "eval", "testdata", "longmemeval_fixture.json")
		default:
			fatalf("unsupported dataset format %q", format)
		}
	}
	if outDir == "" {
		outDir = filepath.Join(".artifacts", "eval", strings.ReplaceAll(string(formatValue), " ", "-"))
	}

	dataset, err := eval.LoadDatasetWithOptions(datasetPath, formatValue, eval.DatasetLoadOptions{
		LOCOMO: eval.LOCOMOOptions{
			SkipCategoryFive: locomoSkipCategoryFive,
			IncludeCaptions:  locomoIncludeCaptions,
		},
		LongMemEval: eval.LongMemEvalOptions{
			Granularity: eval.LongMemEvalGranularity(strings.TrimSpace(longMemEvalGranularityValue)),
		},
	})
	if err != nil {
		fatalf("load dataset: %v", err)
	}
	variants, err := parseVariants(variantsValue)
	if err != nil {
		fatalf("parse variants: %v", err)
	}

	ctx := context.Background()
	embeddingOptions := eval.EmbeddingOptions{
		Mode:     eval.EmbeddingMode(strings.TrimSpace(embeddingModeValue)),
		Provider: string(domain.ProviderOpenAICompatible),
		Model:    strings.TrimSpace(embeddingModel),
		BaseURL:  strings.TrimSpace(embeddingBaseURL),
		APIKey:   strings.TrimSpace(embeddingAPIKey),
		Timeout:  embeddingTimeout,
	}
	report, err := eval.RunBenchmark(ctx, dataset, eval.RunOptions{
		Variants:  variants,
		TopN:      topN,
		Embedding: embeddingOptions,
	})
	if err != nil {
		fatalf("run benchmark: %v", err)
	}
	if perfSmoke {
		perf, err := eval.RunPerformanceSmoke(ctx, dataset, eval.PerformanceOptions{
			Variant:    memoryservice.SearchVariantFull,
			Iterations: perfIterations,
			TopN:       topN,
			Embedding:  embeddingOptions,
		})
		if err != nil {
			fatalf("run performance smoke: %v", err)
		}
		report.Performance = &perf
	}
	if err := eval.WriteArtifacts(report, outDir); err != nil {
		fatalf("write artifacts: %v", err)
	}
	if report.Performance != nil && perfMaxP95 > 0 && report.Performance.P95Millis > perfMaxP95 {
		fatalf("performance smoke failed: p95 %.3fms exceeds %.3fms", report.Performance.P95Millis, perfMaxP95)
	}
	if enforceSmoke {
		if err := eval.ValidateSmokeExpectations(report); err != nil {
			fatalf("validate smoke expectations: %v", err)
		}
	}
	fmt.Printf("MemStation benchmark artifacts written to %s\n", outDir)
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseVariants(raw string) ([]memoryservice.SearchVariant, error) {
	parts := strings.Split(raw, ",")
	variants := make([]memoryservice.SearchVariant, 0, len(parts))
	for _, part := range parts {
		variant := memoryservice.SearchVariant(strings.TrimSpace(part))
		if variant == "" {
			continue
		}
		switch variant {
		case memoryservice.SearchVariantFull,
			memoryservice.SearchVariantFullNoRelations,
			memoryservice.SearchVariantSemanticOnly,
			memoryservice.SearchVariantSemanticImportance:
			variants = append(variants, variant)
		default:
			return nil, fmt.Errorf("unknown variant %q", variant)
		}
	}
	if len(variants) == 0 {
		return nil, fmt.Errorf("at least one search variant is required")
	}
	return variants, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
