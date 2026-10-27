package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	eval "memstation/apps/server/tests/eval"
)

func main() {
	judgePolicy := flag.String("rescore-judge-policy", "reference_v2", "Rescore only: reference_v2 or alternatives_v3; never changes generated answers")
	rescore := flag.String("rescore-report", "", "Rejudge a complete private pipeline report without ingestion or new answers; -out is a new report file")
	dataset := flag.String("dataset", "", "External LOCOMO dataset")
	out := flag.String("out", "", "Private checkpoint/report directory")
	prefix := flag.String("prefix", "", "Unique eval- run prefix; reuse only to resume identical configuration")
	sample := flag.String("sample-id", "", "Optional development sample; empty runs all")
	count := flag.Int("questions-per-category", 0, "SHA256-ID sampled questions per category; zero evaluates all")
	top := flag.Int("top-n", 20, "Maximum retrieved documents before byte-budget packing")
	readerPolicy := flag.String("reader-policy", "baseline", "Reader instructions: baseline, coverage or experimental coverage_inference; judge unchanged")
	refine := flag.Bool("refine-evidence", false, "LLM evidence gap plan with at most two scoped follow-up searches")
	contextFormat := flag.String("context-format", "plain", "Reader context: plain baseline or shared production evidence packet")
	episodes := flag.Bool("semantic-episodes", os.Getenv("MEMSTATION_EVAL_SEMANTIC_EPISODES") == "true", "Enable experimental source-grounded semantic episodes during worker ingestion")
	sourceNeighbors := flag.Int("source-neighbor-turns", 0, "Evidence packet source radius, 0 disables, maximum 2; independent of gold diagnostics")
	budget := flag.Int("context-bytes", 16000, "Fixed UTF-8 context byte budget; actual model token usage is also recorded")
	revision := flag.String("code-revision", "", "Exact tested source revision/fingerprint")
	embeddingRevision := flag.String("embedding-revision", "", "Embedding model digest")
	modes := flag.String("modes", "raw,derived,combined", "Comma-separated raw,derived,combined,question_only,full_history,gold_evidence")
	oracleBudget := flag.Int("oracle-context-bytes", 512000, "Diagnostic full/gold context safety limit; exceeding it fails, never truncates")
	neighbors := flag.Int("oracle-neighbor-turns", 0, "Gold-only adjacent turns within the same session")
	rerankModel := flag.String("rerank-model", os.Getenv("RERANK_MODEL"), "Configured production reranker model reference; empty disables")
	rerankRevision := flag.String("rerank-revision", os.Getenv("RERANK_REVISION"), "Pinned reranker weights/service revision")
	rerankSelection := flag.String("rerank-candidate-selection", "balanced", "Rerank admission: balanced channel reservations or preliminary score only")
	rerankMultiplier := flag.Int("rerank-candidate-multiplier", 6, "Production rerank candidate multiplier")
	rerankMin := flag.Int("rerank-min-candidates", 20, "Production rerank candidate minimum")
	rerankMax := flag.Int("rerank-max-candidates", 50, "Production rerank candidate maximum")
	rerankTimeout := flag.Duration("rerank-timeout", 60*time.Second, "Production reranker request timeout")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg := func(role string) eval.LongMemEvalQAJudgeOptions {
		return eval.LongMemEvalQAJudgeOptions{ReasoningEffort: os.Getenv(role + "_REASONING_EFFORT"), Model: os.Getenv(role + "_MODEL"), BaseURL: os.Getenv(role + "_BASE_URL"), APIKey: os.Getenv(role + "_API_KEY"), Timeout: 3 * time.Minute}
	}
	if *rescore != "" {
		if _, err := eval.RescoreLOCOMOPipelineWithPolicy(ctx, *rescore, *out, *revision, cfg("JUDGE"), *judgePolicy); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *judgePolicy != "reference_v2" {
		fmt.Fprintln(os.Stderr, "rescore-judge-policy requires rescore-report")
		os.Exit(1)
	}
	_, err := eval.RunLOCOMOPipeline(ctx, eval.LOCOMOPipelineOptions{SemanticEpisodes: *episodes, ReaderPolicy: *readerPolicy, RefineEvidence: *refine, Planner: cfg("PLANNER"), ContextFormat: *contextFormat, SourceNeighborTurns: *sourceNeighbors, Modes: strings.Split(*modes, ","), OracleContextBytes: *oracleBudget, OracleNeighborTurns: *neighbors, Rerank: eval.PipelineRerankOptions{Model: *rerankModel, CandidateSelection: *rerankSelection, ModelRevision: *rerankRevision, BaseURL: os.Getenv("RERANK_BASE_URL"), APIKey: os.Getenv("RERANK_API_KEY"), Timeout: *rerankTimeout, CandidateMultiplier: *rerankMultiplier, MinCandidates: *rerankMin, MaxCandidates: *rerankMax}, DatasetPath: *dataset, OutputDir: *out, Prefix: *prefix, SampleID: *sample, QuestionsPerCategory: *count, TopN: *top, ContextBytes: *budget, CodeRevision: *revision, EmbeddingRevision: *embeddingRevision, DatabaseURL: os.Getenv("MEMSTATION_EVAL_DATABASE_URL"), Embedding: eval.EmbeddingOptions{Mode: eval.EmbeddingModeProvider, Model: os.Getenv("EMBEDDING_MODEL"), BaseURL: os.Getenv("EMBEDDING_BASE_URL"), APIKey: os.Getenv("EMBEDDING_API_KEY")}, Extraction: cfg("EXTRACTION"), Answerer: cfg("ANSWERER"), Judge: cfg("JUDGE")})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
