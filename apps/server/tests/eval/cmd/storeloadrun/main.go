package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	eval "memstation/apps/server/tests/eval"
)

func main() {
	out := flag.String("out", ".artifacts/eval/store-load", "Private report directory")
	base := flag.String("base-url", "http://127.0.0.1:8080", "Loopback MemStation service")
	prefix := flag.String("prefix", "eval-"+time.Now().UTC().Format("20060102T150405"), "Unique eval- principal prefix")
	rows := flag.Int("rows", 10000, "Capacity corpus rows (repeated synthetic source texts)")
	requests := flag.Int("requests", 100, "Requests per concurrency level")
	rps := flag.Float64("rps", 12, "Offered requests per second; zero uses closed-loop concurrency without pacing")
	maxP95 := flag.Float64("max-p95-ms", 0, "Explicit optional P95 target; write the report before returning failure")
	datasetPath := flag.String("dataset", "tests/eval/testdata/repo_native_golden.json", "Source fixture")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	db, err := eval.OpenEvaluationDatabase(ctx, os.Getenv("MEMSTATION_EVAL_DATABASE_URL"))
	if err != nil {
		fail(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(12)
	dataset, err := eval.LoadDataset(*datasetPath, eval.DatasetFormatRepoNative)
	if err != nil {
		fail(err)
	}
	report, err := eval.RunStoreLoad(ctx, dataset, eval.StoreLoadOptions{LiveOptions: eval.LiveOptions{BaseURL: *base, AdminToken: os.Getenv("MEMSTATION_ADMIN_TOKEN"), Prefix: *prefix, OutputDir: *out}, Database: db, Embedding: eval.EmbeddingOptions{Mode: eval.EmbeddingModeProvider, Model: os.Getenv("EMBEDDING_MODEL"), BaseURL: os.Getenv("EMBEDDING_BASE_URL"), APIKey: os.Getenv("EMBEDDING_API_KEY")}, Rows: *rows, Requests: *requests, RequestsPerSecond: *rps})
	if err != nil {
		fail(err)
	}
	for _, phase := range report.Phases {
		if phase.Errors > 0 || phase.EmptyResults > 0 || phase.WrongUserHits > 0 {
			fail(fmt.Errorf("load phase failed; inspect store-load.json"))
		}
		if *maxP95 > 0 && phase.P95Millis > *maxP95 {
			fail(fmt.Errorf("recall %s concurrency=%d p95 %.1fms exceeds %.1fms; inspect store-load.json", phase.CacheMode, phase.Concurrency, phase.P95Millis, *maxP95))
		}
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
