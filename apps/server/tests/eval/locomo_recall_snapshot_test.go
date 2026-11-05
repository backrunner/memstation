package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	"memstation/apps/server/internal/service/processing"
	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"
	vectorstore "memstation/apps/server/internal/store/vector"
)

type recallSnapshotRepository struct {
	ports.MemoryRepository
	mu        sync.Mutex
	documents map[string]domain.MemoryDocument
}

type frozenRecallSnapshot struct {
	CapturedAt        time.Time                       `json:"captured_at"`
	Input             domain.SearchInput              `json:"input"`
	Vectors           []domain.VectorSearchResult     `json:"vectors"`
	Entities          domain.QueryEntities            `json:"entities"`
	EntityError       string                          `json:"entity_error"`
	EntityDiagnostics *domain.EntityRecallDiagnostics `json:"entity_diagnostics"`
	Admitted          domain.RerankInput              `json:"admitted_rerank_input"`
}

type frozenRecallVectors struct {
	ports.VectorStore
	snapshot frozenRecallSnapshot
}

func (v frozenRecallVectors) Search(_ context.Context, input domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	if input.Query != v.snapshot.Input.Query || input.UserID != v.snapshot.Input.UserID {
		return nil, errors.New("frozen vector query or owner mismatch")
	}
	return v.snapshot.Vectors, nil
}

type frozenRecallEntities struct{ snapshot frozenRecallSnapshot }

func (e frozenRecallEntities) ExtractQueryEntities(_ context.Context, user, query string) (domain.QueryEntities, error) {
	if query != e.snapshot.Input.Query || user != e.snapshot.Input.UserID || e.snapshot.EntityError != "" {
		return domain.QueryEntities{}, errors.New("invalid frozen entity snapshot")
	}
	return e.snapshot.Entities, nil
}

// Reuses exact query-provider outputs, while exercising production SQL, graph,
// admission, reranking and source hydration. Capture-only mode invokes no model.
// Timings with frozen providers are diagnostic, never live latency evidence.
func TestPrepareLOCOMORecallReplay(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_CASES")
	if path == "" {
		t.Skip("manual bounded recall replay")
	}
	contextBytes, err := strconv.Atoi(os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_CONTEXT_BYTES"))
	if err != nil || contextBytes < 1 || contextBytes > 16000 {
		t.Fatal("explicit frozen context byte budget required")
	}
	maxCandidates, err := strconv.Atoi(os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_MAX_CANDIDATES"))
	if err != nil || maxCandidates < 20 || maxCandidates > 50 {
		t.Fatal("explicit frozen rerank candidate cap between 20 and 50 required")
	}
	raw, err := os.ReadFile(path)
	var rows []packetReplayCase
	if err != nil || json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > 16 {
		t.Fatal("invalid recall cohort", err)
	}
	out := os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_OUT")
	if out == "" || os.Mkdir(out, 0o700) != nil {
		t.Fatal("fresh recall output required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	db, err := OpenEvaluationDatabase(ctx, os.Getenv("MEMSTATION_EVAL_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var readonly string
	if err = db.QueryRowContext(ctx, `SHOW default_transaction_read_only`).Scan(&readonly); err != nil || readonly != "on" {
		t.Fatal("read-only snapshot required", err)
	}
	for _, row := range rows {
		data, err := os.ReadFile(filepath.Join(os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_SNAPSHOTS"), row.Baseline.QuestionID+".json"))
		var snapshot frozenRecallSnapshot
		if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot.CapturedAt.IsZero() || snapshot.Input.Query != row.Baseline.Query || len(row.Documents) == 0 || snapshot.Input.UserID != row.Documents[0].UserID {
			t.Fatal("snapshot identity mismatch", err)
		}
		if snapshot.EntityError != "" || !pipelineEntityRecallComplete(snapshot.EntityDiagnostics) {
			if err := writePipelineJSON(filepath.Join(out, row.Baseline.QuestionID+".json"), map[string]any{"error": "entity_recall_incomplete", "snapshot_sha256": pipelineHash(string(data))}); err != nil {
				t.Fatal(err)
			}
			t.Errorf("case %s has incomplete entity recall; no model call attempted", row.Baseline.QuestionID)
			continue
		}
		handle := runtimeconfig.NewStaticDBHandle(db)
		engine := memoryservice.NewEngine(memorystore.NewPostgresMemoryRepository(db), frozenRecallVectors{snapshot: snapshot})
		engine.SetClock(func() time.Time { return snapshot.CapturedAt })
		engine.SetEntityRecall(frozenRecallEntities{snapshot}, 200*time.Millisecond)
		engine.SetGenerationService(memorygenservice.NewService(handle, os.Getenv("EMBEDDING_MODEL")))
		engine.SetSourceContextReader(conversationstore.NewPostgresConversationRepository(db))
		configurePipelineReranker(engine, PipelineRerankOptions{Model: os.Getenv("RERANK_MODEL"), BaseURL: os.Getenv("RERANK_BASE_URL"), APIKey: os.Getenv("RERANK_API_KEY"), MaxCandidates: maxCandidates, Timeout: 2 * time.Minute})
		cacheInputs := os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_RERANK_INPUTS")
		cacheResults := os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_RERANK_RESULTS")
		cached := cacheInputs != "" || cacheResults != ""
		cacheHashes := map[string]string{}
		if cached {
			if cacheInputs == "" || cacheResults == "" || snapshot.CapturedAt.IsZero() || os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_CAPTURE_ONLY") == "true" {
				t.Fatal("cached rerank requires input, results, capture timestamp and non-capture mode")
			}
			inputRaw, readErr := os.ReadFile(filepath.Join(cacheInputs, row.Baseline.QuestionID+".json"))
			resultRaw, resultErr := os.ReadFile(filepath.Join(cacheResults, row.Baseline.QuestionID+".json"))
			if readErr != nil || resultErr != nil {
				t.Fatal("missing frozen rerank artifacts", readErr, resultErr)
			}
			reranker, err := loadFrozenRecallReranker(inputRaw, resultRaw, pipelineHash(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			engine.SetReranker(reranker)
			cacheHashes["input_sha256"] = pipelineHash(string(inputRaw))
			cacheHashes["result_sha256"] = pipelineHash(string(resultRaw))
		}
		capture := &recallSnapshotReranker{}
		captureOnly := os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_CAPTURE_ONLY") == "true"
		if captureOnly {
			engine.SetReranker(capture)
		}
		// Reserve attempt before any inference; interruption cannot silently retry.
		output := filepath.Join(out, row.Baseline.QuestionID+".json")
		if err := writePipelineJSON(output, map[string]any{"error": "attempt interrupted", "snapshot_sha256": pipelineHash(string(data))}); err != nil {
			t.Fatal(err)
		}
		found, err := engine.SearchWithVariant(ctx, snapshot.Input, memoryservice.SearchVariantFull)
		result := map[string]any{"snapshot_sha256": pipelineHash(string(data)), "clock": snapshot.CapturedAt, "retrieval": found, "capture_only": captureOnly, "context_byte_budget": contextBytes, "rerank_max_candidates": maxCandidates}
		failure := ""
		if err != nil {
			failure = "retrieval_failed"
		} else if !pipelineEntityRecallComplete(found.Result.EntityRecall) {
			failure = "entity_recall_incomplete"
		}
		if cached {
			result["frozen_rerank"] = cacheHashes
			result["scope"] = "offline production replay with frozen per-document scores; not live quality or latency"
		}
		if captureOnly {
			result["admitted_rerank_input"] = capture.Input
		} else if failure == "" {
			if found.Rerank.Status != "applied" {
				failure = "reranker_not_applied"
			} else {
				core, err := engine.PrepareEvidence(ctx, snapshot.Input, found.Candidates, memoryservice.EvidenceOptions{MaxBytes: contextBytes})
				if err != nil {
					failure = "core_evidence_failed"
				} else {
					packet, err := engine.PrepareEvidence(ctx, snapshot.Input, core.Memories, memoryservice.EvidenceOptions{MaxBytes: contextBytes, NeighborTurns: 1})
					if err != nil {
						failure = "source_evidence_failed"
					} else {
						result["packet"] = packet
					}
				}
			}
		}
		if failure != "" {
			result["error"] = failure
		}
		if err := writePipelineJSON(output, result); err != nil {
			t.Fatal(err)
		}
		// Preserve the failed row and finish the frozen cohort without retrying
		// it. An early provider failure must not erase later denominators.
		if failure != "" {
			t.Errorf("case %s failed: %s", row.Baseline.QuestionID, failure)
		}
		t.Logf("prepared %s: %d rerank documents, %d bytes, capture=%t", row.Baseline.QuestionID, found.Rerank.Candidates, found.Rerank.InputBytes, captureOnly)
	}
}

func (r *recallSnapshotRepository) ListDocumentsByNodeIDsInGeneration(ctx context.Context, user, generation string, ids []string) ([]domain.MemoryDocument, error) {
	docs, err := r.MemoryRepository.ListDocumentsByNodeIDsInGeneration(ctx, user, generation, ids)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, doc := range docs {
		r.documents[doc.ID] = doc
	}
	return docs, err
}

type recallSnapshotVectors struct {
	ports.VectorStore
	Results []domain.VectorSearchResult
}

func (v *recallSnapshotVectors) Search(ctx context.Context, input domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	results, err := v.VectorStore.Search(ctx, input)
	v.Results = results
	return results, err
}

type recallSnapshotEntities struct {
	ports.QueryEntityExtractor
	mu     sync.Mutex
	Result domain.QueryEntities
	Err    string
}

func (e *recallSnapshotEntities) ExtractQueryEntities(ctx context.Context, user, query string) (domain.QueryEntities, error) {
	result, err := e.QueryEntityExtractor.ExtractQueryEntities(ctx, user, query)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Result = result
	if err != nil {
		e.Err = err.Error()
	}
	return result, err
}

type recallSnapshotReranker struct{ Input domain.RerankInput }

func (r *recallSnapshotReranker) Rerank(_ context.Context, input domain.RerankInput) ([]domain.RerankResult, error) {
	r.Input = input
	return nil, errors.New("diagnostic capture only: no reranker inference")
}

// This capture observes production collection and admission without invoking a
// reranker, reader or judge. The deliberate reranker error exposes first-stage
// score components; its output is never a quality or fallback benchmark result.
func TestLiveLOCOMORecallSnapshot(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_RECALL_SNAPSHOT_CASES")
	if path == "" {
		t.Skip("manual read-only recall-stage snapshot")
	}
	raw, err := os.ReadFile(path)
	var rows []packetReplayCase
	if err != nil || json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > 16 {
		t.Fatal("invalid small snapshot cohort", err)
	}
	out := os.Getenv("MEMSTATION_EVAL_RECALL_SNAPSHOT_OUT")
	if out == "" || os.Mkdir(out, 0o700) != nil {
		t.Fatal("snapshot requires a fresh output directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := OpenEvaluationDatabase(ctx, os.Getenv("MEMSTATION_EVAL_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var readonly string
	if err = db.QueryRowContext(ctx, `SHOW default_transaction_read_only`).Scan(&readonly); err != nil || readonly != "on" {
		t.Fatal("snapshot must be read only", err)
	}
	var owner string
	var users int
	if err = db.QueryRowContext(ctx, `SELECT min(user_id), count(DISTINCT user_id) FROM conversations`).Scan(&owner, &users); err != nil || users != 1 {
		t.Fatal("invalid snapshot scope", err)
	}
	processor, err := processing.New(pipelineReplayProcessing(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	embedding := EmbeddingOptions{Mode: EmbeddingModeProvider, Model: os.Getenv("EMBEDDING_MODEL"), BaseURL: os.Getenv("EMBEDDING_BASE_URL"), APIKey: os.Getenv("EMBEDDING_API_KEY")}
	client, err := newEvalEmbeddingClient(embedding)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if len(row.Documents) == 0 || row.Documents[0].UserID != owner {
			t.Fatal("case and snapshot scope mismatch")
		}
		handle := runtimeconfig.NewStaticDBHandle(db)
		repo := &recallSnapshotRepository{MemoryRepository: memorystore.NewPostgresMemoryRepository(db), documents: map[string]domain.MemoryDocument{}}
		vectors := vectorstore.NewPGVectorStore(handle)
		observed := &observedEmbeddingClient{EmbeddingClient: client}
		vectors.SetEmbedder(observed)
		vectorCapture := &recallSnapshotVectors{VectorStore: vectors}
		entityCapture := &recallSnapshotEntities{QueryEntityExtractor: processor}
		reranker := &recallSnapshotReranker{}
		engine := memoryservice.NewEngine(repo, vectorCapture)
		engine.SetEntityRecall(entityCapture, 200*time.Millisecond)
		engine.SetGenerationService(memorygenservice.NewService(handle, embedding.Model))
		config := memoryservice.DefaultRerankConfig()
		config.Model = os.Getenv("RERANK_MODEL")
		engine.SetRerankConfig(config)
		engine.SetReranker(reranker)
		input := pipelineSearchInput(owner, row.Baseline.Query, "combined", 20)
		result, err := engine.SearchWithVariant(ctx, input, memoryservice.SearchVariantFull)
		if err != nil {
			t.Fatal(err)
		}
		observed.mu.Lock()
		embedErr := observed.err
		observed.mu.Unlock()
		entityCapture.mu.Lock()
		entities, entityErr := entityCapture.Result, entityCapture.Err
		entityCapture.mu.Unlock()
		if embedErr != nil || len(reranker.Input.Documents) != 50 {
			t.Fatal("snapshot missed query embedding or fixed 50-document admission", embedErr)
		}
		data := map[string]any{"case_id": row.Baseline.QuestionID, "cohort": row.Cohort, "group": row.Group, "input": input, "captured_at": time.Now().UTC(), "documents": repo.documents, "components": result.Components, "base_scores": result.BaseScores, "vectors": vectorCapture.Results, "entities": entities, "entity_error": entityErr, "entity_diagnostics": result.Result.EntityRecall, "admitted_rerank_input": reranker.Input, "capture_policy": "production collection and admission; deliberate no-inference reranker; not a quality result"}
		if err := writePipelineJSON(filepath.Join(out, row.Baseline.QuestionID+".json"), data); err != nil {
			t.Fatal(err)
		}
		if entityErr != "" || !pipelineEntityRecallComplete(result.Result.EntityRecall) {
			t.Errorf("case %s has incomplete entity recall; snapshot retained for diagnosis only", row.Baseline.QuestionID)
		}
		t.Logf("captured %s: %d candidates, %d admitted", row.Baseline.QuestionID, len(result.Components), len(reranker.Input.Documents))
	}
}
