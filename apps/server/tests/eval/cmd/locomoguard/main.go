// locomoguard is an offline evaluation tool, not a public memstation command.
package main

import (
	"flag"
	"fmt"
	"os"

	eval "memstation/apps/server/tests/eval"
)

func main() {
	plan := flag.String("plan", "", "Private candidate acceptance plan frozen before evaluation")
	hash := flag.String("plan-sha256", "", "Independently recorded SHA256 of the frozen plan")
	reports := flag.String("candidate-reports", "", "JSON array of private pipeline report paths")
	out := flag.String("out", "", "Fresh output directory; never overwrite a prior verdict")
	flag.Parse()
	if os.Getenv("MEMSTATION_ENFORCE_BENCHMARKS") != "true" {
		fmt.Fprintln(os.Stderr, "manual acceptance requires MEMSTATION_ENFORCE_BENCHMARKS=true")
		os.Exit(1)
	}
	v, err := eval.RunLOCOMOCandidateGate(*plan, *hash, *reports, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Passed %d frozen cases; gains=%d regressions=%d net=%d p=%.4f noise_flips=%d; replacement_eligible=%t\n", v.Expected, v.Gains, v.Regressions, v.NetGain, v.McNemarP, v.NoiseFlips, v.ReplacementEligible)
}
