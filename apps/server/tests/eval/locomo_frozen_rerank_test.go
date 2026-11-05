package eval

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

// Frozen scores are an offline ablation, never a production cache. A subset is
// allowed only with identical owner, query, model, node identity and text. Missing
// scores fail closed; the replay cannot invoke a model or silently approximate it.
type frozenRecallReranker struct {
	input  domain.RerankInput
	scores map[string]float64
}

func loadFrozenRecallReranker(inputRaw, resultRaw []byte, snapshotHash string) (*frozenRecallReranker, error) {
	var captured struct {
		Input domain.RerankInput `json:"admitted_rerank_input"`
	}
	var saved struct {
		SnapshotHash string                          `json:"snapshot_sha256"`
		Retrieval    memoryservice.DebugSearchResult `json:"retrieval"`
	}
	if snapshotHash == "" || json.Unmarshal(inputRaw, &captured) != nil || json.Unmarshal(resultRaw, &saved) != nil || saved.SnapshotHash != snapshotHash || saved.Retrieval.Rerank.Status != "applied" {
		return nil, errors.New("invalid frozen rerank provenance")
	}
	r := &frozenRecallReranker{input: captured.Input, scores: map[string]float64{}}
	texts := map[string]string{}
	for _, d := range r.input.Documents {
		if d.ID == "" || d.Text == "" || texts[d.ID] != "" {
			return nil, errors.New("invalid frozen rerank documents")
		}
		texts[d.ID] = d.Text
	}
	if r.input.UserID == "" || r.input.Query == "" || r.input.Model == "" || len(texts) < 2 || len(texts) != r.input.TopN {
		return nil, errors.New("invalid frozen rerank identity")
	}
	for _, d := range saved.Retrieval.Candidates {
		component, ok := saved.Retrieval.Components[d.ID]
		score := component.RerankScore
		if !ok || !component.RerankApplied || d.UserID != r.input.UserID || texts[d.NodeID] != "["+string(d.Type)+"] "+strings.TrimSpace(d.Content) || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return nil, errors.New("frozen rerank document or score mismatch")
		}
		if _, exists := r.scores[d.NodeID]; exists {
			return nil, errors.New("duplicate frozen rerank score")
		}
		r.scores[d.NodeID] = score
	}
	if len(r.scores) != len(texts) {
		return nil, errors.New("incomplete frozen rerank scores")
	}
	return r, nil
}

func (r *frozenRecallReranker) Rerank(_ context.Context, input domain.RerankInput) ([]domain.RerankResult, error) {
	if input.UserID != r.input.UserID || input.Query != r.input.Query || input.Model != r.input.Model || input.TopN != len(input.Documents) || len(input.Documents) < 2 {
		return nil, errors.New("frozen rerank request mismatch")
	}
	texts := map[string]string{}
	for _, d := range r.input.Documents {
		texts[d.ID] = d.Text
	}
	seen := map[string]bool{}
	out := make([]domain.RerankResult, 0, len(input.Documents))
	for i, d := range input.Documents {
		score, ok := r.scores[d.ID]
		if !ok || seen[d.ID] || texts[d.ID] != d.Text {
			return nil, errors.New("frozen rerank subset missing, changed or duplicated")
		}
		seen[d.ID] = true
		out = append(out, domain.RerankResult{DocumentID: d.ID, Index: i, Score: score})
	}
	return out, nil
}

func TestFrozenRecallRerankerIdentityAndSubset(t *testing.T) {
	base := domain.RerankInput{UserID: "owner", Query: "question", Model: "configured", TopN: 3, Documents: []domain.RerankDocument{{ID: "a", Text: "alpha"}, {ID: "b", Text: "beta"}, {ID: "c", Text: "gamma"}}}
	r := &frozenRecallReranker{input: base, scores: map[string]float64{"a": 0.3, "b": 0.8, "c": 0.5}}
	subset := base
	subset.TopN, subset.Documents = 2, []domain.RerankDocument{base.Documents[2], base.Documents[0]}
	out, err := r.Rerank(context.Background(), subset)
	if err != nil || len(out) != 2 || out[0].Score != 0.5 || out[1].Score != 0.3 || out[1].Index != 1 {
		t.Fatalf("subset failed: %v %v", out, err)
	}
	for name, mutate := range map[string]func(*domain.RerankInput){
		"owner":     func(i *domain.RerankInput) { i.UserID = "different" },
		"query":     func(i *domain.RerankInput) { i.Query = "changed" },
		"model":     func(i *domain.RerankInput) { i.Model = "changed" },
		"text":      func(i *domain.RerankInput) { i.Documents[0].Text = "changed" },
		"missing":   func(i *domain.RerankInput) { i.Documents[0].ID = "missing" },
		"duplicate": func(i *domain.RerankInput) { i.Documents[0] = i.Documents[1] },
	} {
		t.Run(name, func(t *testing.T) {
			input := subset
			input.Documents = append([]domain.RerankDocument(nil), subset.Documents...)
			mutate(&input)
			if _, err := r.Rerank(context.Background(), input); err == nil {
				t.Fatal("mismatched request accepted")
			}
		})
	}
}

func TestFrozenRecallRerankerRejectsIncompleteArtifacts(t *testing.T) {
	input := []byte(`{"admitted_rerank_input":{"user_id":"owner","query":"question","model":"configured","top_n":2,"documents":[{"id":"node-a","text":"[fact] alpha"},{"id":"node-b","text":"[fact] beta"}]}}`)
	result := []byte(`{"snapshot_sha256":"frozen-source","retrieval":{"rerank":{"status":"applied"},"candidates":[{"id":"a","node_id":"node-a","user_id":"owner","type":"fact","content":"alpha"},{"id":"b","node_id":"node-b","user_id":"owner","type":"fact","content":"beta"}],"components":{"a":{"rerank_applied":true,"rerank_score":0.4},"b":{"rerank_applied":true,"rerank_score":0.7}}}}`)
	if _, err := loadFrozenRecallReranker(input, result, "frozen-source"); err != nil {
		t.Fatal(err)
	}
	for name, changed := range map[string][]byte{
		"wrong snapshot": []byte(strings.Replace(string(result), "frozen-source", "different", 1)),
		"wrong owner":    []byte(strings.Replace(string(result), "owner", "different", 1)),
		"changed text":   []byte(strings.Replace(string(result), "alpha", "changed", 1)),
		"missing score":  []byte(strings.Replace(string(result), `"b":{"rerank_applied":true,"rerank_score":0.7}`, `"b":{}`, 1)),
		"invalid score":  []byte(strings.Replace(string(result), `"rerank_score":0.7`, `"rerank_score":1.2`, 1)),
		"degraded":       []byte(strings.Replace(string(result), "applied", "degraded", 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadFrozenRecallReranker(input, changed, "frozen-source"); err == nil {
				t.Fatal("invalid frozen artifacts accepted")
			}
		})
	}
}
