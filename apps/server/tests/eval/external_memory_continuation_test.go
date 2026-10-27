package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type externalSeal struct {
	Protocol       string            `json:"protocol"`
	ManifestSHA256 string            `json:"manifest_sha256"`
	Files          map[string]string `json:"files"`
	ProcessExit    int               `json:"process_exit_code"`
	FinishedAt     string            `json:"process_finished_at"`
}

func externalJSONEqual(a, b any) bool {
	decode := func(v any) any {
		raw, _ := json.Marshal(v)
		var value any
		_ = json.Unmarshal(raw, &value)
		return value
	}
	return reflect.DeepEqual(decode(a), decode(b))
}

// A continuation is an append-only execution journal, not a second draw for
// failed cases. Every attempted outcome survives, including errors. The parent
// must be sealed by its supervisor after that process has stopped.
func loadExternalContinuation(parent string, jobs []*externalJob, manifest map[string]any) (map[string]any, error) {
	sealed, err := os.ReadFile(filepath.Join(parent, "sealed.json"))
	if err != nil {
		return nil, err
	}
	var seal externalSeal
	decoder := json.NewDecoder(strings.NewReader(string(sealed)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&seal); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF || seal.Protocol != "memstation.external.memory.v1" || seal.FinishedAt == "" || len(seal.Files) != len(jobs) {
		return nil, fmt.Errorf("invalid completed-process seal")
	}
	raw, err := os.ReadFile(filepath.Join(parent, "manifest.json"))
	if err != nil || pipelineHash(string(raw)) != seal.ManifestSHA256 {
		return nil, fmt.Errorf("parent manifest digest mismatch")
	}
	var previous map[string]any
	if err = json.Unmarshal(raw, &previous); err != nil {
		return nil, err
	}
	for _, key := range []string{"plan_sha256", "plan", "reader", "judge", "embedding", "embedding_revision", "rerank", "rerank_max_inflight", "reader_format", "source_ingestion", "storage", "arena_agent_prompt_sha256", "longmemeval_reader_prompt_sha256", "judge_policy", "scope"} {
		if !externalJSONEqual(previous[key], manifest[key]) {
			return nil, fmt.Errorf("continuation protocol changed: %s", key)
		}
	}
	for _, job := range jobs {
		name := job.ID + "-" + job.Mode + ".json"
		data, err := os.ReadFile(filepath.Join(parent, name))
		if err != nil || pipelineHash(string(data)) != seal.Files[name] {
			return nil, fmt.Errorf("sealed case digest mismatch: %s", name)
		}
		var prior externalJob
		if err = json.Unmarshal(data, &prior); err != nil {
			return nil, err
		}
		if prior.ID != job.ID || prior.Dataset != job.Dataset || prior.Mode != job.Mode || prior.Type != job.Type || len(prior.Answers) != len(job.Answers) {
			return nil, fmt.Errorf("sealed case identity mismatch: %s", name)
		}
		pending := false
		for i, answer := range prior.Answers {
			expected := job.Answers[i]
			if answer.Index != i || answer.Question != expected.Question || answer.Reference != expected.Reference {
				return nil, fmt.Errorf("sealed question changed: %s/%d", name, i)
			}
			if answer.ContextHash != "" && answer.ContextHash != pipelineHash(answer.Context) || answer.ContextHash == "" && answer.Context != "" || job.Mode == "none" && answer.Context != "" {
				return nil, fmt.Errorf("sealed context changed: %s/%d", name, i)
			}
			if answer.Error == "not executed" || answer.Error == "attempt interrupted before completion" {
				if answer.Judged || answer.Correct || answer.Answer != "" || answer.RawJudge != "" {
					return nil, fmt.Errorf("pending case has completed model outputs: %s/%d", name, i)
				}
			}
			if answer.Error == "not executed" {
				pending = true
			} else if pending {
				return nil, fmt.Errorf("nonsequential checkpoint: %s/%d", name, i)
			}
			if answer.Correct && !answer.Judged || answer.Error == "" && !answer.Judged {
				return nil, fmt.Errorf("invalid completed outcome: %s/%d", name, i)
			}
		}
		job.Answers, job.SetupError, job.Resume = prior.Answers, prior.SetupError, true
	}
	return map[string]any{"parent_manifest_sha256": seal.ManifestSHA256, "parent_seal_sha256": pipelineHash(string(sealed)), "parent_code_revision": previous["code_revision"], "parent_case_sha256": seal.Files, "parent_process_exit_code": seal.ProcessExit, "policy": "all attempted outcomes retained, including errors; interrupted attempts fail without retry; only not-executed steps run; own raw history reconstructed without grades"}, nil
}
