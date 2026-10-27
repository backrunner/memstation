package eval

import (
	"math"
	"os"
	"strconv"
	"testing"
	"time"
)

func pipelineReplayRoleConfig(t *testing.T, role string) LongMemEvalQAJudgeOptions {
	t.Helper()
	config := LongMemEvalQAJudgeOptions{Model: os.Getenv(role + "_MODEL"), BaseURL: os.Getenv(role + "_BASE_URL"), APIKey: os.Getenv(role + "_API_KEY"), ReasoningEffort: os.Getenv(role + "_REASONING_EFFORT"), Timeout: 3 * time.Minute}
	if raw := os.Getenv(role + "_TEMPERATURE"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 2 {
			t.Fatal("invalid replay sampling temperature for", role)
		}
		config.Temperature = &value
	}
	return config
}
