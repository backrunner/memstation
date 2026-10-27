package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	eval "memstation/apps/server/tests/eval"
)

func main() {
	dataset := flag.String("dataset", "", "External LOCOMO data file (never committed)")
	out := flag.String("out", "", "Private output directory")
	cache := flag.String("cache", "", "Private model-vector cache; not used for latency tests")
	revision := flag.String("model-revision", "", "Pinned embedding digest/revision; part of cache identity")
	sample := flag.String("sample-id", "", "Optional predefined development sample for a pilot")
	instruction := flag.String("query-instruction", "Given a question about previous conversations, retrieve relevant dialogue passages that answer it.", "One fixed retrieval task instruction")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	_, err := eval.RunLOCOMODiagnosis(ctx, eval.LOCOMODiagnosisOptions{DatasetPath: *dataset, OutputDir: *out, CacheDir: *cache, ModelRevision: *revision, SampleID: *sample, QueryInstruction: *instruction, Embedding: eval.EmbeddingOptions{Mode: eval.EmbeddingModeProvider, Model: os.Getenv("EMBEDDING_MODEL"), BaseURL: os.Getenv("EMBEDDING_BASE_URL"), APIKey: os.Getenv("EMBEDDING_API_KEY")}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
