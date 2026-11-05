package eval

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

func auditAdmittedDocuments(input domain.RerankInput, documents map[string]domain.MemoryDocument, owner, query string) ([]domain.MemoryDocument, error) {
	if input.UserID != owner || input.Query != query || input.TopN != len(input.Documents) || len(input.Documents) == 0 {
		return nil, errors.New("capture identity or count mismatch")
	}
	byNode := map[string]domain.MemoryDocument{}
	for _, doc := range documents {
		byNode[doc.NodeID] = doc
	}
	seen := map[string]bool{}
	out := make([]domain.MemoryDocument, 0, len(input.Documents))
	for _, d := range input.Documents {
		doc, ok := byNode[d.ID]
		if !ok || seen[d.ID] || doc.UserID != owner || "["+string(doc.Type)+"] "+strings.TrimSpace(doc.Content) != d.Text {
			return nil, errors.New("captured document missing, changed, duplicated or outside owner")
		}
		seen[d.ID] = true
		out = append(out, doc)
	}
	return out, nil
}

func TestAuditAdmissionRequiresExactSourceIdentity(t *testing.T) {
	docs := map[string]domain.MemoryDocument{"a": {ID: "a", NodeID: "node", UserID: "owner", Type: domain.MemoryTypeFact, Content: "original text"}}
	input := domain.RerankInput{UserID: "owner", Query: "question", TopN: 1, Documents: []domain.RerankDocument{{ID: "node", Text: "[fact] original text"}}}
	if got, err := auditAdmittedDocuments(input, docs, "owner", "question"); err != nil || len(got) != 1 {
		t.Fatalf("valid capture failed: %v %v", got, err)
	}
	for name, mutate := range map[string]func(*domain.RerankInput){
		"owner":     func(i *domain.RerankInput) { i.UserID = "other" },
		"query":     func(i *domain.RerankInput) { i.Query = "other" },
		"count":     func(i *domain.RerankInput) { i.TopN++ },
		"text":      func(i *domain.RerankInput) { i.Documents[0].Text = "changed" },
		"missing":   func(i *domain.RerankInput) { i.Documents[0].ID = "missing" },
		"duplicate": func(i *domain.RerankInput) { i.Documents = append(i.Documents, i.Documents[0]); i.TopN++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := input
			changed.Documents = append([]domain.RerankDocument(nil), input.Documents...)
			mutate(&changed)
			if _, err := auditAdmittedDocuments(changed, docs, "owner", "question"); err == nil {
				t.Fatal("invalid capture accepted")
			}
		})
	}
}

// Posthoc provenance is not answer correctness: a cited turn may support only
// part of a derived record. Labels are loaded only in this separate offline
// observer, after all retrieval/packing artifacts have already been produced.
func TestAuditLOCOMORecallCoverage(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_COVERAGE_AUDIT_PLAN")
	if path == "" {
		t.Skip("manual posthoc source coverage audit; no model calls")
	}
	var plan struct {
		Dataset       string                       `json:"dataset"`
		DatasetSHA256 string                       `json:"dataset_sha256"`
		Cases         string                       `json:"cases"`
		Stages        map[string]map[string]string `json:"stages"`
		Snapshots     map[string]string            `json:"snapshots"`
		Output        string                       `json:"output"`
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &plan) != nil || len(plan.Stages) == 0 {
		t.Fatal("invalid posthoc audit plan", err)
	}
	dataset, err := os.ReadFile(plan.Dataset)
	if err != nil || pipelineHash(string(dataset)) != plan.DatasetSHA256 {
		t.Fatal("dataset hash mismatch", err)
	}
	samples, err := loadPipelineSamples(plan.Dataset, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(plan.Cases)
	var cases []packetReplayCase
	if err != nil || json.Unmarshal(raw, &cases) != nil || len(cases) == 0 || len(cases) > 16 {
		t.Fatal("invalid posthoc cohort", err)
	}
	var results []map[string]any
	for _, row := range cases {
		var sample pipelineSample
		for _, s := range samples {
			if s.ID == row.Baseline.SampleID {
				sample = s
			}
		}
		if len(sample.Sessions) == 0 || len(row.Documents) == 0 {
			t.Fatal("missing source sample")
		}
		evidence := row.Baseline.EvidenceRefs
		if len(evidence) == 0 {
			t.Fatal("missing diagnostic source labels")
		}
		owner := row.Documents[0].UserID
		for name, dirs := range plan.Stages {
			raw, err := os.ReadFile(filepath.Join(dirs[row.Cohort], row.Baseline.QuestionID+".json"))
			var replay struct {
				Error        string                          `json:"error"`
				Retrieval    memoryservice.DebugSearchResult `json:"retrieval"`
				Packet       memoryservice.EvidencePacket    `json:"packet"`
				CaptureOnly  bool                            `json:"capture_only"`
				SnapshotHash string                          `json:"snapshot_sha256"`
				Admitted     domain.RerankInput              `json:"admitted_rerank_input"`
			}
			if err != nil || json.Unmarshal(raw, &replay) != nil {
				t.Fatal("invalid completed retrieval artifact", err)
			}
			if replay.Error != "" {
				results = append(results, map[string]any{"case_id": row.Baseline.QuestionID, "arm": name, "artifact_sha256": pipelineHash(string(raw)), "execution_error": replay.Error})
				continue
			}
			if !replay.CaptureOnly && (replay.Retrieval.Rerank.Status != "applied" || replay.Packet.Text == "") {
				t.Fatal("successful retrieval artifact requires applied reranking and a prepared packet")
			}
			stages := map[string][]domain.MemoryDocument{"reranked": replay.Retrieval.Candidates, "packed": replay.Packet.Memories}
			if replay.CaptureOnly {
				snapshotRaw, err := os.ReadFile(filepath.Join(plan.Snapshots[row.Cohort], row.Baseline.QuestionID+".json"))
				var snapshot struct {
					Documents map[string]domain.MemoryDocument `json:"documents"`
				}
				if err != nil || pipelineHash(string(snapshotRaw)) != replay.SnapshotHash || json.Unmarshal(snapshotRaw, &snapshot) != nil || replay.Retrieval.Rerank.Status != "degraded" || replay.Retrieval.Rerank.Reason != "provider_error" {
					t.Fatal("invalid capture provenance", err)
				}
				admitted, err := auditAdmittedDocuments(replay.Admitted, snapshot.Documents, owner, row.Baseline.Query)
				if err != nil {
					t.Fatal(err)
				}
				stages = map[string][]domain.MemoryDocument{"admitted": admitted}
			} else if replay.Retrieval.Rerank.Status != "applied" || replay.Packet.Text == "" {
				t.Fatal("completed inference artifact required")
			}
			coverage := map[string]any{}
			for stage, docs := range stages {
				refs := pipelineSourceRefs(docs, owner, sample)
				records := make([]map[string]any, 0, len(docs))
				for _, doc := range docs {
					records = append(records, map[string]any{"id": doc.ID, "node_id": doc.NodeID, "type": doc.Type, "content_sha256": pipelineHash(doc.Content)})
				}
				covered, missing := []string{}, []string{}
				for _, ref := range evidence {
					if slices.Contains(refs, ref) {
						covered = append(covered, ref)
					} else {
						missing = append(missing, ref)
					}
				}
				coverage[stage] = map[string]any{"documents": len(docs), "records": records, "provenance_refs": refs, "annotated_refs_present": covered, "annotated_refs_missing": missing}
			}
			var sourceRows []map[string]any
			for _, session := range sample.Sessions {
				for i, ref := range session.Refs {
					if slices.Contains(evidence, ref) {
						text := session.Messages[i].Text
						sourceRow := map[string]any{"ref": ref, "text": text}
						if !replay.CaptureOnly {
							sourceRow["verbatim_turn_in_packet"] = strings.Contains(replay.Packet.Text, text)
						}
						sourceRows = append(sourceRows, sourceRow)
					}
				}
			}
			results = append(results, map[string]any{"case_id": row.Baseline.QuestionID, "arm": name, "capture_only": replay.CaptureOnly, "artifact_sha256": pipelineHash(string(raw)), "stages": coverage, "annotated_text": sourceRows, "context_bytes": len(replay.Packet.Text), "rerank_input_bytes": replay.Retrieval.Rerank.InputBytes})
		}
	}
	f, err := os.OpenFile(plan.Output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal("fresh output required", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(map[string]any{"scope": "posthoc provenance and exact text coverage, not QA accuracy or semantic entailment", "dataset_sha256": plan.DatasetSHA256, "results": results}); err != nil {
		t.Fatal(err)
	}
}
