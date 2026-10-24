package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	eval "memstation/apps/server/tests/eval"
)

func main() {
	dataset := flag.String("dataset", "tests/eval/testdata/source_replay_zh.json", "Synthetic source replay cases")
	out := flag.String("out", ".artifacts/eval/live", "Private report directory")
	base := flag.String("base-url", "http://127.0.0.1:8080", "Loopback MemStation service")
	prefix := flag.String("prefix", "eval-"+time.Now().UTC().Format("20060102T150405"), "Unique eval- principal prefix")
	concurrency := flag.Int("concurrency", 2, "Concurrent principals")
	flag.Parse()
	raw, err := os.ReadFile(*dataset)
	if err != nil {
		fail(err)
	}
	var fixtures eval.LiveDataset
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		fail(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	report, err := eval.RunLiveEvaluation(ctx, fixtures, eval.LiveOptions{BaseURL: *base, AdminToken: os.Getenv("MEMSTATION_ADMIN_TOKEN"), Model: os.Getenv("CHAT_MODEL"), Prefix: *prefix, OutputDir: *out, Concurrency: *concurrency})
	if err != nil {
		fail(err)
	}
	fmt.Printf("Source replay: %d questions, default injection %.3f, derived-only %.3f, without memory %.3f, wrong-user hits %d\n", report.Questions, report.Accuracy, report.DerivedAccuracy, report.BaselineAccuracy, report.WrongUserHits)
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
