package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedPerformanceThresholdPreservesArtifacts(t *testing.T) {
	dir := t.TempDir()
	dataset := filepath.Join(dir, "dataset.json")
	if err := os.WriteFile(dataset, []byte(`{"name":"threshold-artifacts","documents":[{"id":"d","node_id":"n","user_id":"u","type":"fact","content":"alpha"}],"queries":[{"case_id":"q","user_id":"u","query":"alpha","positives":["d"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "report")
	cmd := exec.Command(os.Args[0], "-test.run=^TestEvalCLIHelper$", "--", "-dataset", dataset, "-out", out, "-embedding-mode", "deterministic", "-perf-smoke", "-perf-iterations", "1", "-perf-max-p95-ms", "0.000000001")
	cmd.Env = append(os.Environ(), "MEMSTATION_EVAL_CLI_HELPER=1")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "performance smoke failed") {
		t.Fatalf("expected threshold failure, got %v: %s", err, output)
	}
	for _, name := range []string{"aggregate.json", "results.json", "failures.json", "perf.json", "report.md"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("lost diagnostic %s after failure: %v", name, err)
		}
	}
}

func TestEvalCLIHelper(t *testing.T) {
	if os.Getenv("MEMSTATION_EVAL_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			flag.CommandLine = flag.NewFlagSet("evalrun", flag.ExitOnError)
			main()
			return
		}
	}
	t.Fatal("missing helper arguments")
}
