package eval

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestLOCOMOEvidenceNormalizationDoesNotInventReferences(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input string
		want  []string
	}{
		{"D9:1 D4:4 D4:06", []string{"D9:1", "D4:4", "D4:6"}},
		{"D:11:26", []string{"D11:26"}},
		{"D30:05", []string{"D30:5"}},
		{"D", []string{"D"}},
		{"untrusted D1:1 commentary", []string{"untrusted D1:1 commentary"}},
	} {
		if got := normalizeLOCOMOEvidenceRefs(test.input); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%q: got %v, want %v", test.input, got, test.want)
		}
	}
}

func TestLOCOMOReportsExcludedAndPartiallyUnresolvedEvidence(t *testing.T) {
	t.Parallel()
	sample := locomoSample{SampleID: "coverage", Conversation: []any{
		map[string]any{"dia_id": "D11:26", "text": "synthetic alpha"},
		map[string]any{"dia_id": "D30:5", "text": "synthetic beta"},
		map[string]any{"dia_id": "D4:4", "text": "synthetic gamma"},
	}, QA: []map[string]any{
		{"question": "q1", "category": 4, "evidence": []any{"D:11:26 D30:05"}},
		{"question": "q2", "category": 4, "evidence": []any{"D4:4", "D99:1"}},
		{"question": "q3", "category": 3, "evidence": []any{"D"}},
		{"question": "q4", "category": 4},
		{"question": "excluded", "category": 5, "evidence": []any{"D4:4"}},
		{"question": "", "category": 4},
	}}
	dataset, err := buildLOCOMODataset([]locomoSample{sample}, LOCOMOOptions{SkipCategoryFive: true})
	if err != nil {
		t.Fatal(err)
	}
	want := DatasetCoverage{SourceQuestions: 6, EligibleQuestions: 4, ScoredQuestions: 2, ExcludedCategoryQuestions: 1, ExcludedEmptyQuestions: 1, NoEvidenceQuestions: 1, UnresolvedOnlyQuestions: 1, PartiallyUnresolvedQuestions: 1, UnresolvedReferenceCount: 2}
	if !reflect.DeepEqual(*dataset.Coverage, want) || len(dataset.Queries[0].Positives) != 2 {
		t.Fatalf("lost coverage or normalized references: %+v", dataset)
	}
	report, err := RunBenchmark(context.Background(), dataset, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := WriteArtifacts(report, out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "aggregate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct{ Coverage DatasetCoverage }
	if err := json.Unmarshal(raw, &artifact); err != nil || artifact.Coverage != want {
		t.Fatalf("artifact lost source coverage: %s (%v)", raw, err)
	}
}

func TestLOCOMOCaptionsAreExplicitAndNeverMutateRawSources(t *testing.T) {
	t.Parallel()
	turn := map[string]any{"speaker": "Alex", "dia_id": "D1:1", "text": "Look at this.", "blip_caption": "synthetic image of a blue bicycle"}
	sample := locomoSample{SampleID: "caption", Conversation: map[string]any{"session_1": []any{turn}}}
	with := collectLOCOMODocumentsWithOptions(sample, LOCOMOOptions{IncludeCaptions: true})
	without := collectLOCOMODocuments(sample)
	if !strings.Contains(with[0].Document.Content, "machine generated, may be inaccurate") || !strings.Contains(with[0].Document.Content, "blue bicycle") || strings.Contains(without[0].Document.Content, "bicycle") || turn["text"] != "Look at this." {
		t.Fatal("caption policy leaked into the raw baseline or mutated source text")
	}
}

func TestLOCOMODiagnosisUsesProviderCacheAndQueryOnlyInstruction(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	inputs := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Input string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		inputs[body.Input]++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "data": []any{map[string]any{"embedding": []float32{1, 0, 0}}}})
	}))
	defer server.Close()
	out, cache := t.TempDir(), t.TempDir()
	options := LOCOMODiagnosisOptions{DatasetPath: filepath.Join("testdata", "locomo_fixture.json"), OutputDir: out, CacheDir: cache, ModelRevision: "fixed-fixture-revision", QueryInstruction: "Find relevant dialogue.", Embedding: EmbeddingOptions{Mode: EmbeddingModeProvider, Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}}
	report, err := RunLOCOMODiagnosis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Modes) != 3 || report.Queries != 2 || report.Coverage == nil {
		t.Fatalf("unexpected diagnosis scope: %+v", report)
	}
	mu.Lock()
	firstInputs := maps.Clone(inputs)
	mu.Unlock()
	before := len(firstInputs)
	dataset, err := LoadLOCOMODataset(options.DatasetPath, LOCOMOOptions{SkipCategoryFive: true})
	if err != nil {
		t.Fatal(err)
	}
	queryInputs := map[string]bool{}
	for _, query := range dataset.Queries {
		queryInputs["Instruct: "+options.QueryInstruction+"\nQuery: "+query.Query] = true
	}
	for input, count := range firstInputs {
		if count != 1 {
			t.Errorf("provider repeated identical input %q (%d)", input, count)
		}
		if strings.HasPrefix(input, "Instruct:") && !queryInputs[input] {
			t.Error("retrieval instruction applied to something other than a query")
		}
	}
	if _, err := RunLOCOMODiagnosis(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	cachedInputs := maps.Clone(inputs)
	mu.Unlock()
	if len(cachedInputs) != before {
		t.Fatal("cached identical experiment made new provider calls")
	}
	for input, count := range cachedInputs {
		if count != 1 {
			t.Errorf("cache missed identical input %q (%d)", input, count)
		}
	}
	// A truncated/corrupt cache fails closed rather than silently changing a run.
	files, err := os.ReadDir(cache)
	if err != nil || len(files) == 0 {
		t.Fatalf("missing diagnostic cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cache, files[0].Name()), []byte(`{"Vector":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunLOCOMODiagnosis(context.Background(), options); err == nil {
		t.Fatal("corrupt cached vector did not fail the experiment")
	}
}
