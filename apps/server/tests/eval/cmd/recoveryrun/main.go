package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"memstation/apps/server/internal/worker"
	eval "memstation/apps/server/tests/eval"
)

func main() {
	var (
		outDir            string
		conversations     int
		workerConcurrency int
		messagesPerTurn   int
		queueBuffer       int
		timeout           time.Duration
		maxDrain          time.Duration
	)

	flag.StringVar(&outDir, "out", "", "Output directory for recovery drill artifacts.")
	flag.IntVar(&conversations, "conversations", 64, "Synthetic conversations to enqueue.")
	flag.IntVar(&workerConcurrency, "worker-concurrency", 8, "Concurrent workers used for the drill.")
	flag.IntVar(&messagesPerTurn, "messages-per-turn", 3, "User/assistant turns seeded per conversation.")
	flag.IntVar(&queueBuffer, "queue-buffer", 0, "Optional initial in-memory queue capacity; grows with task fan-out.")
	flag.DurationVar(&timeout, "timeout", 30*time.Second, "Maximum wall clock duration for the recovery drill.")
	flag.DurationVar(&maxDrain, "max-drain", 0, "Optional maximum allowed drain duration.")
	flag.Parse()

	if outDir == "" {
		outDir = filepath.Join(".artifacts", "eval", "recovery")
	}
	ctx := context.Background()
	report, err := worker.RunRecoveryDrill(ctx, worker.RecoveryOptions{
		Conversations:     conversations,
		WorkerConcurrency: workerConcurrency,
		MessagesPerTurn:   messagesPerTurn,
		QueueBuffer:       queueBuffer,
		Timeout:           timeout,
	})
	if err != nil {
		fatalf("run recovery drill: %v", err)
	}
	if err := eval.WriteRecoveryArtifacts(report, outDir); err != nil {
		fatalf("write recovery artifacts: %v", err)
	}
	if err := worker.ValidateRecoveryReport(report, maxDrain); err != nil {
		fatalf("validate recovery report: %v", err)
	}
	fmt.Printf("MemStation recovery drill artifacts written to %s\n", outDir)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
