package eval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	identitystore "memstation/apps/server/internal/store/sql/identity"
	ingestionstore "memstation/apps/server/internal/store/sql/ingestion"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
	openaiprovider "memstation/apps/server/internal/provider/openai"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	analysisservice "memstation/apps/server/internal/service/analysis"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	"memstation/apps/server/internal/service/memoryllm"
	settingsservice "memstation/apps/server/internal/service/settings"
	vectorstore "memstation/apps/server/internal/store/vector"
	"memstation/apps/server/internal/worker"

	"github.com/hibiken/asynq"
)

type pipelineRuntime struct {
	drainMu         sync.Mutex
	taskQueue       *pipelineQueue
	taskFailures    map[string]pipelineTaskFailure
	db              *sql.DB
	lease           *sql.Conn
	options         LOCOMOPipelineOptions
	conversations   *conversationstore.PostgresConversationRepository
	memories        *memorystore.PostgresMemoryRepository
	outbox          *conversationstore.PostgresOutboxRepository
	engine          *memoryservice.Engine
	services        worker.Services
	logger          *slog.Logger
	observed        *observedEmbeddingClient
	rawSources      *conversationstore.InMemoryConversationRepository
	rawSourceOwners map[string]bool
	raw             *harness
}

func pipelineAdapter(cfg LongMemEvalQAJudgeOptions) *openaiprovider.Adapter {
	return openaiprovider.NewCompatibleAdapter(provider.StaticConfigResolver{domain.ProviderOpenAICompatible: {BaseURL: cfg.BaseURL, APIKey: cfg.APIKey}}, cfg.Timeout)
}

func newPipelineRuntime(ctx context.Context, o LOCOMOPipelineOptions) (*pipelineRuntime, error) {
	if len(o.Modes) == 0 {
		o.Modes = []string{"raw", "derived", "combined"}
	}
	if !pipelineNeedsIngestion(o.Modes) {
		return &pipelineRuntime{options: o}, nil
	}
	db, err := OpenEvaluationDatabase(ctx, o.DatabaseURL)
	if err != nil {
		return nil, err
	}
	ok := false
	var lease *sql.Conn
	defer func() {
		if !ok {
			if lease != nil {
				_ = lease.Close()
			}
			_ = db.Close()
		}
	}()
	lease, err = db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var locked bool
	if err = lease.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended('memstation-locomo-eval:' || current_database(),0))`).Scan(&locked); err != nil {
		return nil, err
	}
	if !locked {
		return nil, fmt.Errorf("another LOCOMO pipeline owns this evaluation database")
	}
	// Refuse mixed databases; a resumed run may only see its own principal prefix.
	var foreign bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM (SELECT user_id FROM conversations UNION ALL SELECT user_id FROM memory_nodes UNION ALL SELECT user_id FROM outbox_events) records WHERE left(user_id,length($1))<>$1)`, o.Prefix+"-").Scan(&foreign); err != nil {
		return nil, err
	}
	if foreign {
		return nil, fmt.Errorf("evaluation database contains another run's conversations")
	}
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversations WHERE COALESCE(metadata->>'eval_manifest','')<>$1)`, o.manifestHash).Scan(&foreign); err != nil {
		return nil, err
	}
	if foreign {
		return nil, fmt.Errorf("database manifest mismatch; use a fresh evaluation database")
	}
	db.SetMaxOpenConns(8)
	handle := runtimeconfig.NewStaticDBHandle(db)
	memories := memorystore.NewPostgresMemoryRepository(db)
	conversations := conversationstore.NewPostgresConversationRepository(db)
	outbox := conversationstore.NewPostgresOutboxRepository(db)
	embedder, err := newEvalEmbeddingClient(o.Embedding)
	if err != nil {
		return nil, err
	}
	observed := &observedEmbeddingClient{EmbeddingClient: embedder}
	vectors := vectorstore.NewPGVectorStore(handle)
	vectors.SetEmbedder(observed)
	generations := memorygenservice.NewService(handle, o.Embedding.Model)
	engine := memoryservice.NewEngine(memories, vectors)
	engine.SetSourceContextReader(conversations)
	configurePipelineReranker(engine, o.Rerank)
	engine.SetGenerationService(generations)
	settings := settingsservice.NewService(nil, nil, o.Answerer.Model, o.Extraction.Model, o.Extraction.Model, o.Embedding.Model, 2048)
	var extractionAdapter ports.ProviderAdapter = pipelineAdapter(o.Extraction)
	if o.extractionAdapter != nil {
		extractionAdapter = o.extractionAdapter
	}
	registry := provider.NewRegistry(domain.ProviderOpenAICompatible, extractionAdapter)
	llm := memoryllm.NewClientWithOptions(registry, settings, o.Extraction.Timeout, memoryllm.Options{ReasoningEffort: o.Extraction.ReasoningEffort, MaxOutputTokens: o.Extraction.outputTokenLimit()})
	scorer := memoryservice.NewScorerWithGenerator(llm)
	service := memoryservice.NewService(engine, memories)
	service.SetScorer(scorer)
	service.SetGenerationService(generations)
	r := &pipelineRuntime{db: db, lease: lease, options: o, conversations: conversations, memories: memories, outbox: outbox, engine: engine, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), observed: observed}
	r.services = worker.Services{TaskAuthority: ingestionstore.NewPostgresIngestionRepository(db), Conversations: conversations, Memories: memories, MemoryService: service, Generations: generations, Vectors: vectors, Analysis: analysisservice.NewServiceWithOptions(llm, analysisservice.Options{SemanticEpisodes: o.SemanticEpisodes}), Scorer: scorer, EmbeddingModel: o.Embedding.Model, AllowedKeyMemoryKeys: map[string]struct{}{}}
	ok = true
	return r, nil
}

func (r *pipelineRuntime) close() {
	if r.lease != nil {
		_ = r.lease.Close()
	}
	if r.db != nil {
		_ = r.db.Close()
	}
}

type pipelineTaskState struct {
	Attempts  []pipelineTaskAttempt `json:"attempts,omitempty"`
	Pending   []domain.TaskMessage  `json:"pending"`
	Completed map[string]bool       `json:"completed"`
}

type pipelineTaskAttempt struct {
	TaskID    string    `json:"task_id"`
	UserID    string    `json:"user_id"`
	Type      string    `json:"type"`
	StartedAt time.Time `json:"started_at"`
	Seconds   float64   `json:"seconds"`
	Error     string    `json:"error,omitempty"`
}
type pipelineQueue struct {
	mu          sync.Mutex
	concurrency int
	path        string
	state       pipelineTaskState
}

func taskFingerprint(t domain.TaskMessage) string {
	raw, _ := json.Marshal(t)
	return pipelineHash(string(raw))
}

func (q *pipelineQueue) Publish(ctx context.Context, t domain.TaskMessage) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	user, _ := t.Payload["user_id"].(string)
	explicit, _ := t.Payload["workspace_id"].(string)
	workspace, err := security.DataScope(ctx, user, explicit)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(t.Payload)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	payload["workspace_id"] = workspace
	if version, ok := payload["schema_version"]; ok && version != float64(2) {
		return domain.ErrAccessDenied
	}
	payload["schema_version"] = 2
	payload["partition_id"] = security.WritePartition(ctx)
	t.Payload = payload
	key := taskFingerprint(t)
	if q.state.Completed[key] {
		return nil
	}
	for _, p := range q.state.Pending {
		if taskFingerprint(p) == key {
			return nil
		}
	}
	q.state.Pending = append(q.state.Pending, t)
	return writePipelineJSON(q.path, q.state)
}

func (q *pipelineQueue) process(ctx context.Context, mux *asynq.ServeMux, task domain.TaskMessage) error {
	raw, err := json.Marshal(task.Payload)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		q.mu.Lock()
		index := len(q.state.Attempts)
		start := time.Now()
		user, _ := task.Payload["user_id"].(string)
		q.state.Attempts = append(q.state.Attempts, pipelineTaskAttempt{TaskID: taskFingerprint(task), UserID: user, Type: task.Type, StartedAt: start.UTC(), Error: "attempt interrupted"})
		err = writePipelineJSON(q.path, q.state)
		q.mu.Unlock()
		if err != nil {
			return err
		}
		err = mux.ProcessTask(ctx, asynq.NewTask(task.Type, raw))
		q.mu.Lock()
		q.state.Attempts[index].Seconds = time.Since(start).Seconds()
		q.state.Attempts[index].Error = ""
		if err != nil {
			q.state.Attempts[index].Error = err.Error()
		}
		saveErr := writePipelineJSON(q.path, q.state)
		q.mu.Unlock()
		if saveErr != nil {
			return saveErr
		}
		if err == nil || errors.Is(err, asynq.SkipRetry) {
			break
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	if err != nil {
		return fmt.Errorf("%s failed after bounded retries: %w", task.Type, err)
	}
	return nil
}

type pipelineTaskFailure struct {
	Task domain.TaskMessage
	Err  error
}
type pipelineTaskFailures map[string]pipelineTaskFailure

func (f pipelineTaskFailures) Error() string {
	errs := []error{}
	for _, failure := range f {
		errs = append(errs, failure.Err)
	}
	return errors.Join(errs...).Error()
}

func (f pipelineTaskFailures) forOwner(owner string) error {
	errs := []error{}
	for _, failure := range f {
		if failure.Task.Payload["user_id"] == owner {
			errs = append(errs, failure.Err)
		}
	}
	return errors.Join(errs...)
}

func (q *pipelineQueue) drain(ctx context.Context, mux *asynq.ServeMux) error {
	failures := map[string]pipelineTaskFailure{}
	if err := q.drainPending(ctx, mux, failures); err != nil {
		return err
	}
	if len(failures) > 0 {
		return pipelineTaskFailures(failures)
	}
	return nil
}

func (q *pipelineQueue) drainPending(ctx context.Context, mux *asynq.ServeMux, failures map[string]pipelineTaskFailure) error {
	for {
		q.mu.Lock()
		batch := []domain.TaskMessage{}
		for _, task := range q.state.Pending {
			if _, failed := failures[taskFingerprint(task)]; !failed {
				batch = append(batch, task)
			}
			if len(batch) == max(q.concurrency, 1) {
				break
			}
		}
		q.mu.Unlock()
		if len(batch) == 0 {
			return nil
		}
		results := make([]error, len(batch))
		var wg sync.WaitGroup
		for i, task := range batch {
			wg.Go(func() { results[i] = q.process(ctx, mux, task) })
		}
		wg.Wait()
		completed := map[string]bool{}
		for i, task := range batch {
			if results[i] == nil {
				completed[taskFingerprint(task)] = true
			} else {
				failures[taskFingerprint(task)] = pipelineTaskFailure{Task: task, Err: results[i]}
			}
		}
		q.mu.Lock()
		pending := q.state.Pending[:0]
		for _, task := range q.state.Pending {
			key := taskFingerprint(task)
			if completed[key] {
				q.state.Completed[key] = true
			} else {
				pending = append(pending, task)
			}
		}
		q.state.Pending = pending
		saveErr := writePipelineJSON(q.path, q.state)
		q.mu.Unlock()
		if saveErr != nil {
			return saveErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// One run owns one outbox and one durable task journal. Infrastructure starts
// with no request principal; production handlers validate and bind each envelope.
func (r *pipelineRuntime) drainOutbox(ctx context.Context) error {
	r.drainMu.Lock()
	defer r.drainMu.Unlock()
	if r.taskQueue == nil {
		q := &pipelineQueue{path: filepath.Join(r.options.OutputDir, "worker-tasks.json"), concurrency: r.options.TaskConcurrency, state: pipelineTaskState{Completed: map[string]bool{}}}
		if raw, err := os.ReadFile(q.path); err == nil {
			if err = json.Unmarshal(raw, &q.state); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if q.state.Completed == nil {
			q.state.Completed = map[string]bool{}
		}
		r.taskQueue = q
	}
	services := r.services
	services.Publisher = r.taskQueue
	mux := worker.NewServeMux(r.logger, services)
	relay := worker.NewRelay(r.logger, r.outbox, r.taskQueue)
	relay.SetTaskAuthority(services.TaskAuthority)
	for {
		if err := relay.Flush(ctx); err != nil {
			return err
		}
		if r.taskFailures == nil {
			r.taskFailures = map[string]pipelineTaskFailure{}
		}
		if err := r.taskQueue.drainPending(ctx, mux, r.taskFailures); err != nil {
			return err
		}
		pending, err := r.outbox.Pending(ctx, 1)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			if len(r.taskFailures) > 0 {
				return pipelineTaskFailures(r.taskFailures)
			}
			return nil
		}
	}
}

func (r *pipelineRuntime) ingest(ctx context.Context, owner string, s pipelineSample) ([]PipelineStage, error) {
	stages := []PipelineStage{}
	scoped, err := security.WithPersonalDataScope(ctx, owner, "")
	if err != nil {
		return stages, err
	}
	identities := identitystore.NewIdentityRepository(runtimeconfig.NewStaticDBHandle(r.db))
	workspace, err := identities.EnsurePersonal(scoped, owner)
	if err != nil {
		return stages, err
	}
	agentID := domain.AccessID("eval-agent", owner)
	if _, err = identities.GetAgent(scoped, owner, agentID); errors.Is(err, domain.ErrAccessNotFound) {
		now := time.Now().UTC()
		agent := domain.Agent{ID: agentID, UserID: owner, ActorID: domain.AccessID("eval-actor", owner), Name: "Benchmark ingestion", Status: "active", DefaultWorkspaceID: workspace.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
		err = identities.CreateAgent(scoped, agent, domain.WorkspaceGrant{UserID: owner, WorkspaceID: workspace.ID, AgentID: agent.ID, Capabilities: domain.PersonalCapabilities(), Revision: 1})
	}
	if err != nil {
		return stages, err
	}
	if _, err = r.services.Generations.EnsureActiveGeneration(scoped, owner); err != nil {
		return stages, err
	}
	for _, session := range s.Sessions {
		stage := PipelineStage{SessionID: session.ID, ConversationID: owner + "-" + session.ID}
		stagePath := filepath.Join(r.options.OutputDir, stage.ConversationID+".json")
		if raw, e := os.ReadFile(stagePath); e == nil {
			if err = json.Unmarshal(raw, &stage); err != nil {
				return stages, err
			}
			if stage.Error == "" {
				stored, lookupErr := r.conversations.GetConversation(scoped, owner, stage.ConversationID)
				if lookupErr != nil || stored.SummaryStatus != domain.ProcessingStatusCompleted || stored.AnalysisStatus != domain.ProcessingStatusCompleted {
					return stages, fmt.Errorf("source checkpoint no longer matches database")
				}
				stages = append(stages, stage)
				continue
			}
		} else if !os.IsNotExist(e) {
			return stages, e
		}
		stage.Error = ""
		start := time.Now()
		fmt.Printf("Ingesting %s %s (%d human turns)\n", s.ID, session.ID, len(session.Messages))
		record, lookup := r.conversations.GetConversation(scoped, owner, stage.ConversationID)
		if errors.Is(lookup, domain.ErrConversationNotFound) {
			record = domain.ConversationRecord{AgentID: agentID, UserID: owner, ConversationID: stage.ConversationID, RequestFormat: string(domain.ProtocolOpenAIChat), Provider: domain.ProviderOpenAICompatible, Mode: domain.ModeInject, Messages: session.Messages, CreatedAt: session.At, UpdatedAt: session.At, FinalizeStatus: domain.FinalizeStatusActive, SummaryStatus: domain.ProcessingStatusPending, AnalysisStatus: domain.ProcessingStatusPending, Metadata: map[string]any{"memory_persist_enabled": true, "retain_source_materials": true, "source_session_time": session.Date, "eval_manifest": r.options.manifestHash}}
			err = r.conversations.PersistExchange(scoped, record)
		} else {
			err = lookup
		}
		if err == nil {
			err = r.conversations.RequestFinalization(scoped, domain.ConversationFinalization{UserID: owner, ConversationID: stage.ConversationID, Status: domain.FinalizeStatusEnded, Reason: "explicit"}, r.outbox)
		}
		if err == nil {
			err = r.drainOutbox(ctx)
			var failures pipelineTaskFailures
			if errors.As(err, &failures) {
				err = failures.forOwner(owner)
			}
		}
		if err == nil {
			record, err = r.conversations.GetConversation(scoped, owner, stage.ConversationID)
			if err == nil && (record.SummaryStatus != domain.ProcessingStatusCompleted || record.AnalysisStatus != domain.ProcessingStatusCompleted) {
				err = fmt.Errorf("memory generation incomplete")
			}
		}
		if err == nil {
			stage.Documents, err = r.memories.ListDocumentsByConversation(scoped, owner, stage.ConversationID)
		}
		stage.Seconds = time.Since(start).Seconds()
		if err != nil {
			stage.Error = err.Error()
		}
		if saveErr := writePipelineJSON(stagePath, stage); saveErr != nil {
			return stages, saveErr
		}
		stages = append(stages, stage)
		if err != nil {
			return stages, err
		}
	}
	return stages, nil
}
