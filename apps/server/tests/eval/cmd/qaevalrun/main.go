package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	eval "memstation/apps/server/tests/eval"
)

func main() {
	var (
		datasetPath    string
		outDir         string
		hypothesesPath string
		judgeModel     string
		judgeBaseURL   string
		judgeAPIKey    string
		judgeTimeout   time.Duration
	)

	flag.StringVar(&datasetPath, "dataset", "", "Path to the LongMemEval reference dataset file.")
	flag.StringVar(&outDir, "out", "", "Output directory for QA auto-eval artifacts.")
	flag.StringVar(&hypothesesPath, "hypotheses", "", "Path to a JSON or JSONL hypotheses file containing question_id and hypothesis.")
	flag.StringVar(&judgeModel, "judge-model", envOrDefault("LONGMEMEVAL_QA_JUDGE_MODEL", ""), "Judge model for LongMemEval QA auto-eval.")
	flag.StringVar(&judgeBaseURL, "judge-base-url", envOrDefault("LONGMEMEVAL_QA_JUDGE_BASE_URL", ""), "OpenAI-compatible base URL for the judge model.")
	flag.StringVar(&judgeAPIKey, "judge-api-key", envOrDefault("LONGMEMEVAL_QA_JUDGE_API_KEY", ""), "API key for the judge model.")
	flag.DurationVar(&judgeTimeout, "judge-timeout", 60*time.Second, "Timeout for judge model requests.")
	flag.Parse()

	if datasetPath == "" {
		datasetPath = filepath.Join("tests", "eval", "testdata", "longmemeval_fixture.json")
	}
	if hypothesesPath == "" {
		fatalf("a hypotheses file is required")
	}
	if outDir == "" {
		outDir = filepath.Join(".artifacts", "eval", "longmemeval-qa")
	}

	evaluation, err := eval.RunLongMemEvalQAAutoEval(context.Background(), datasetPath, hypothesesPath, eval.LongMemEvalQAJudgeOptions{
		Model:   strings.TrimSpace(judgeModel),
		BaseURL: strings.TrimSpace(judgeBaseURL),
		APIKey:  strings.TrimSpace(judgeAPIKey),
		Timeout: judgeTimeout,
	})
	if err != nil {
		fatalf("run LongMemEval QA auto-eval: %v", err)
	}
	if err := eval.WriteLongMemEvalQAArtifacts(evaluation, outDir); err != nil {
		fatalf("write LongMemEval QA artifacts: %v", err)
	}
	fmt.Printf("MemStation LongMemEval QA auto-eval artifacts written to %s\n", outDir)
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
