package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	aicallstore "memstation/apps/server/internal/store/sql/aicall"
	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	ingestionstore "memstation/apps/server/internal/store/sql/ingestion"
	lifecyclestore "memstation/apps/server/internal/store/sql/lifecycle"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	graphresolver "memstation/apps/server/graph/resolver"
	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/aibroker"
	"memstation/apps/server/internal/service/analysis"
	"memstation/apps/server/internal/service/apikey"
	"memstation/apps/server/internal/service/ingestion"
	"memstation/apps/server/internal/service/lifecycle"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/memorycontrol"
	"memstation/apps/server/internal/service/memorygen"
	"memstation/apps/server/internal/service/memoryllm"
	"memstation/apps/server/internal/store/deletionjournal"
	vectorstore "memstation/apps/server/internal/store/vector"
	grpctransport "memstation/apps/server/internal/transport/grpc"
	httptransport "memstation/apps/server/internal/transport/http"
	"memstation/apps/server/internal/worker"
	pb "memstation/apps/server/proto/memstation/v1"
	"memstation/apps/server/tests/testutil/mockbroker"
)

func independentDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "independent_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	isolated, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = isolated.Close()
		_, err := db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	files, err := filepath.Glob("../../ent/migrate/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatal("migrations unavailable", err)
	}
	sort.Strings(files)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = isolated.Exec(string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	return isolated
}

type isolatedQueue struct {
	ports.TaskPublisher
	queue string
}

func (p isolatedQueue) Publish(ctx context.Context, m domain.TaskMessage) error {
	m.Queue = p.queue
	return p.TaskPublisher.Publish(ctx, m)
}

// Exercises real PostgreSQL/outbox/Redis/workers and both data transports. The
// broker is deliberately deterministic: this test makes no quality claim.
func TestIndependentMemoryPipelinePostgresRedisBroker(t *testing.T) {
	testIndependentMemoryPipeline(t, false)
}

func TestIndependentMemorySemanticEpisodesPostgresRedisBroker(t *testing.T) {
	testIndependentMemoryPipeline(t, true)
}

func testIndependentMemoryPipeline(t *testing.T, episodes bool) {
	redisURL := os.Getenv("MEMSTATION_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("Redis required")
	}
	db := independentDatabase(t)
	handle := runtimeconfig.NewStaticDBHandle(db)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	journal, err := deletionjournal.New(filepath.Join(t.TempDir(), "independent.jsonl"), "fixture-journal-key")
	if err != nil {
		t.Fatal(err)
	}
	lifeRepo := lifecyclestore.NewMemoryLifecycleRepository(handle)
	lifeRepo.SetDeletionJournal(journal)
	if err = lifeRepo.EnsureDeletionJournal(ctx); err != nil {
		t.Fatal(err)
	}
	gate := lifecyclestore.NewMemoryGate(lifeRepo)
	ingestRepo := ingestionstore.NewDurableIngestionRepository(handle)
	ingestRepo.SetDeletionJournal(journal)
	ingest := ingestion.NewService(ingestRepo, 8192)
	life := lifecycle.NewService(lifeRepo)
	life.SetReady(gate.Check)
	brokerHTTP := httptest.NewServer(&mockbroker.Handler{Credential: "fixture-broker", FailNext: true})
	defer brokerHTTP.Close()
	broker, err := aibroker.New(aibroker.Config{BaseURL: brokerHTTP.URL + "/v1", Credential: "fixture-broker", Roles: map[string]string{"summary": "memory.summary", "analysis": "memory.analysis", "embedding": "memory.embedding", "rerank": "memory.rerank"}, Embedding: domain.EmbeddingIdentity{ModelVersion: "fixture-embedding-v1", Dimensions: 3, Fingerprint: "fixture-embedding-v1:3"}, Timeout: time.Second}, aicallstore.NewAICallRepository(handle))
	if err != nil {
		t.Fatal(err)
	}
	llm := memoryllm.NewClient(nil, nil, time.Second)
	llm.SetRuntime(broker)
	memories := memorystore.NewPostgresMemoryRepository(db)
	conversations := conversationstore.NewPostgresConversationRepository(db)
	vectors := vectorstore.NewPGVectorStore(handle)
	vectors.SetEmbedder(broker)
	gens := memorygen.NewService(handle, "memory.embedding")
	engine := memoryservice.NewEngine(memories, vectors)
	engine.SetGenerationService(gens)
	memory := memoryservice.NewService(engine, memories)
	scorer := memoryservice.NewScorerWithGenerator(llm)
	memory.SetScorer(scorer)
	memory.SetGenerationService(gens)
	memory.SetVectorStore(vectors)
	memory.SetWatermarks(ingestRepo)
	opt, err := bootstrap.AsynqRedisOpt(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := asynq.NewClient(opt)
	defer client.Close()
	inspector := asynq.NewInspector(opt)
	defer inspector.Close()
	queue := "contract-" + uuid.NewString()
	publisher := isolatedQueue{worker.NewPublisher(logger, client), queue}
	relay := worker.NewRelay(logger, conversationstore.NewPostgresOutboxRepository(db), publisher)
	relay.SetTaskAuthority(ingestRepo)
	var failCleanup atomic.Bool
	processOperation := func(c context.Context, id string) error {
		if failCleanup.Load() {
			return errors.New("fixture cleanup unavailable")
		}
		return lifeRepo.ProcessOperation(c, id)
	}
	mux := worker.NewServeMux(logger, worker.Services{Scorer: scorer, DataReady: gate.Check, TaskAuthority: ingestRepo, Jobs: ingestRepo, AcknowledgeReceipt: ingestRepo.AcknowledgeReceipt, ProcessOperation: processOperation, Conversations: conversations, Memories: memories, MemoryService: memory, Generations: gens, Vectors: vectors, Publisher: publisher, Analysis: analysis.NewServiceWithOptions(llm, analysis.Options{SemanticEpisodes: episodes}), AllowedKeyMemoryKeys: map[string]struct{}{}})
	server := asynq.NewServer(opt, asynq.Config{Concurrency: 4, Queues: map[string]int{queue: 1}, RetryDelayFunc: worker.MemoryRetryDelay, ShutdownTimeout: time.Second, TaskCheckInterval: 100 * time.Millisecond, ErrorHandler: asynq.ErrorHandlerFunc(func(_ context.Context, task *asynq.Task, err error) { t.Log(task.Type(), err) })})
	if err = server.Start(mux); err != nil {
		t.Fatal(err)
	}
	defer func() {
		server.Shutdown()
		if err := inspector.DeleteQueue(queue, true); err != nil && !strings.Contains(err.Error(), "queue not found") {
			t.Error(err)
		}
	}()
	keys := apikey.NewService(handle)
	identity := keys.Identities()
	owner := "contract-owner"
	admin := security.WithAdmin(ctx, true)
	if _, err = db.Exec(`INSERT INTO users(id,name) VALUES($1,'Contract owner')`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = identity.Personal(admin, owner); err != nil {
		t.Fatal(err)
	}
	sessions := security.NewAdminSessionManager("fixture-session", time.Hour)
	auth := security.Authenticator{AdminToken: "fixture-admin", SessionManager: sessions, LookupPrincipal: keys.LookupPrincipalByHash, ResolveAccess: keys.ResolveAccess}
	usage := aibroker.NewUsageService(aicallstore.NewAICallRepository(handle), broker)
	control := memorycontrol.NewService(lifeRepo, inspector.RunTask)
	graph := &graphresolver.Resolver{Identity: identity, Ingestion: ingest, Lifecycle: life, MemoryControlService: control, AIUsage: usage, Generations: gens}
	httpServer := httptest.NewServer(httptransport.NewRouter(httptransport.Dependencies{GraphQL: graph, Generations: gens, MemoryControl: control, Logger: logger, Auth: auth, APIKeys: keys, Ingestion: ingest, Lifecycle: life, Memory: memory, AIUsage: usage, DataReady: gate.Check}))
	defer httpServer.Close()
	grpcServer := grpctransport.NewServer(grpctransport.Dependencies{Identity: identity, Generations: gens, MemoryControl: control, Logger: logger, Auth: auth, Ingestion: ingest, Lifecycle: life, Memory: memory, AIUsage: usage, DataReady: gate.Check})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	rpc := pb.NewMemoryDataServiceClient(connection)
	agents := []domain.Agent{}
	tokens := []string{}
	receipts := []domain.IngestionReceipt{}
	scopes := []context.Context{}
	request := func(token, method, path, key string, body any, out any) int {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, httpServer.URL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if token == "fixture-admin" {
			req.AddCookie(&http.Cookie{Name: security.AdminSessionCookieName, Value: sessions.Issue("admin")})
		}
		req.Header.Set("Idempotency-Key", key)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ = io.ReadAll(response.Body)
		if out != nil && response.StatusCode < 300 {
			if err = json.Unmarshal(raw, out); err != nil {
				t.Fatalf("decode %s %v", path, err)
			}
		}
		if response.StatusCode >= 500 {
			t.Logf("%s %d %s", path, response.StatusCode, raw)
		}
		return response.StatusCode
	}
	for i, city := range []string{"上海", "北京"} {
		w, err := identity.CreateWorkspace(admin, domain.Workspace{UserID: owner, Name: city, Kind: "group"})
		if err != nil {
			t.Fatal(err)
		}
		agent, err := identity.CreateWorkspaceAgent(admin, owner, w.ID, "Mimic", w.Capabilities)
		if err != nil {
			t.Fatal(err)
		}
		agents = append(agents, agent)
		integration, err := identity.PutIntegration(admin, domain.IntegrationConfig{UserID: owner, WorkspaceID: w.ID, Name: "Fixture", Kind: "chat", AgentID: agent.ID, ExternalSpaceID: "room", AllowedEventTypes: []string{"message.created", "message.edited", "message.deleted"}})
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := keys.CreateWithOptions(admin, owner, apikey.CreateOptions{Name: "contract", AgentID: agent.ID, WorkspaceID: w.ID})
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
		scope, err := auth.AuthenticateContext(ctx, token, w.ID)
		if err != nil {
			t.Fatal(err)
		}
		scopes = append(scopes, scope)
		batch := domain.ObserveBatch{WorkspaceID: w.ID, IntegrationID: integration.ID, ExternalSpaceID: "room", Events: []domain.SourceEvent{{EventID: "event1", Type: "message.created", SourceMessageID: "message1", SourceRevision: 1, Speaker: domain.SourceSpeaker{Kind: "human", ExternalID: "alice", DisplayName: "同名"}, OccurredAt: time.Now().UTC(), Parts: []domain.SourcePart{{Type: "text", Text: "我在" + city + "工作。"}}}}}
		var receipt domain.IngestionReceipt
		if code := request(token, "POST", "/api/v1/events/batch", "batch1", batch, &receipt); code != 202 {
			t.Fatalf("observe %d", code)
		}
		receipts = append(receipts, receipt)
		if receipt.IndexedSequence != 0 {
			t.Fatal("accepted reported indexed")
		}
		var duplicate domain.IngestionReceipt
		if code := request(token, "POST", "/api/v1/events/batch", "batch1", batch, &duplicate); code != 202 || duplicate.ID != receipt.ID || duplicate.Events[0].Status != "duplicate" {
			t.Fatal("retry duplicated source")
		}
		batch.Events[0].Parts[0].Text = "conflicting payload"
		if code := request(token, "POST", "/api/v1/events/batch", "batch1", batch, nil); code != 409 {
			t.Fatalf("payload conflict %d", code)
		}
		window, err := ingest.Window(scope, receipt.Events[0].ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		if code := request(token, "POST", "/api/v1/conversations/"+window.ID+"/finalize", "", map[string]any{"expected_revision": window.Revision}, nil); code != 202 {
			t.Fatalf("finalize %d", code)
		}
		if _, err = conversations.GetConversation(scope, owner, window.ID); err != nil {
			t.Fatalf("event conversation projection: %v", err)
		}
		if i == 1 {
			rpcctx := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
			if _, err = rpc.Source(rpcctx, &pb.RecordRequest{Id: receipts[0].Events[0].MessageID}); status.Code(err) != codes.NotFound {
				t.Fatalf("cross-space source %v", err)
			}
		}
	}
	deadline := time.Now().Add(50 * time.Second)
	complete := false
	for time.Now().Before(deadline) {
		if err = relay.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		complete = true
		for i, r := range receipts {
			p, e := ingest.Progress(scopes[i], r.ID)
			if e != nil {
				t.Fatal(e)
			}
			if p.Status != "complete" {
				complete = false
			}
		}
		if complete {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !complete {
		rows, _ := db.Query(`SELECT task_type,status,attempt,failure_code FROM memory_jobs`)
		defer rows.Close()
		for rows.Next() {
			var task, state, code string
			var attempt int
			_ = rows.Scan(&task, &state, &attempt, &code)
			t.Log(task, state, attempt, code)
		}
		t.Fatal("watermarks did not complete")
	}
	for i, scope := range scopes {
		docs, err := memories.ListDocuments(scope, owner, []domain.MemoryType{domain.MemoryTypeFact}, 20)
		if err != nil || len(docs) == 0 {
			t.Fatalf("facts %+v %v", docs, err)
		}
		for _, doc := range docs {
			if len(doc.SubjectActorIDs) != 1 || len(doc.SourceRefs) == 0 || doc.SourceRefs[0].MessageID != receipts[i].Events[0].MessageID {
				t.Fatalf("unbound actor/source %+v", doc)
			}
		}
		if episodes {
			units, e := memories.ListDocuments(scope, owner, []domain.MemoryType{domain.MemoryTypeEpisode}, 20)
			if e != nil || len(units) == 0 {
				t.Fatal("episodes were not stored through worker", e)
			}
			for _, unit := range units {
				if len(unit.SourceRefs) == 0 || unit.WorkspaceID != receipts[i].WorkspaceID || unit.Metadata["semantic_unit_version"] != "episode_v1" {
					t.Fatal("episode lost scoped provenance")
				}
			}
		}
		var recalled domain.SearchResult
		if code := request(tokens[i], "POST", "/api/v1/memory/search", "", map[string]any{"query": "同名的人在哪里工作？", "top_n": 10}, &recalled); code != 200 {
			t.Fatalf("recall %d", code)
		}
		if len(recalled.Memories) == 0 {
			t.Fatal("indexed facts not recalled")
		}
		recalledEpisode := false
		for _, doc := range recalled.Memories {
			if doc.Type == domain.MemoryTypeEpisode {
				recalledEpisode = true
			}
			if doc.WorkspaceID != receipts[i].WorkspaceID {
				t.Fatal("foreign recall", doc.NodeID)
			}
		}
		if episodes && !recalledEpisode {
			t.Fatal("default retrieval omitted stored episode")
		}
	}

	// Private audience must satisfy both the Agent grant and the complete output audience.
	publicFacts, err := memories.ListDocuments(scopes[0], owner, []domain.MemoryType{domain.MemoryTypeFact}, 20)
	if err != nil {
		t.Fatal(err)
	}
	actor := publicFacts[0].SubjectActorIDs[0]
	partition, err := identity.PutPartition(admin, domain.MemoryPartition{UserID: owner, WorkspaceID: receipts[0].WorkspaceID, AgentID: agents[0].ID, AudienceActorIDs: []string{actor}})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := auth.AuthenticateAudienceContext(ctx, tokens[0], receipts[0].WorkspaceID, domain.Audience{Kind: "direct", ActorIDs: []string{actor}})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := life.Propose(direct, domain.MemoryCandidate{Kind: "fact", Content: "仅允许私聊使用", PartitionID: partition.ID, SubjectActorIDs: []string{actor}})
	if err != nil {
		t.Fatal(err)
	}
	privateAdmin, err := identity.AdministrativeDataContext(admin, owner, receipts[0].WorkspaceID, partition.ID)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := life.Decide(privateAdmin, domain.CandidateDecision{ID: candidate.ID, ExpectedRevision: candidate.Revision, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	scopes[0], err = auth.AuthenticateContext(ctx, tokens[0], receipts[0].WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = life.Inspect(scopes[0], approved.ResultNodeID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("group read private record: %v", err)
	}
	if _, err = life.Inspect(direct, approved.ResultNodeID); err != nil {
		t.Fatal(err)
	}
	partition.AudienceActorIDs = []string{}
	if _, err = identity.PutPartition(admin, partition); err != nil {
		t.Fatal(err)
	}
	if _, err = life.Inspect(direct, approved.ResultNodeID); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("cached audience survives revocation: %v", err)
	}
	scopes[0], err = auth.AuthenticateContext(ctx, tokens[0], receipts[0].WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, rebuildIndex := range []int{1, 0} {
		// Rebuild the other complete workspace through the same transactional outbox.
		rebuildCtx := security.WithAdmin(scopes[rebuildIndex], true)
		rebuilt, _, err := gens.CreateRebuildGeneration(rebuildCtx, domain.CreateMemoryRebuildInput{UserID: owner, WorkspaceID: receipts[rebuildIndex].WorkspaceID, Scope: "user", ActivateOnSuccess: true})
		if err != nil {
			t.Fatal(err)
		}
		deadline = time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if err = relay.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			rebuilt, err = gens.GetGeneration(scopes[rebuildIndex], owner, rebuilt.GenerationID)
			if err != nil {
				t.Fatal(err)
			}
			if rebuilt.IsActive {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !rebuilt.IsActive {
			rows, e := db.Query(`SELECT n.node_type,n.source_conversation_id,c.source_revision,n.revision,c.embedding_revision,n.embedding_revision,c.status FROM memory_generation_carries c JOIN memory_nodes n ON n.id=c.node_id WHERE c.generation_id=$1`, rebuilt.GenerationID)
			if e != nil {
				t.Fatal(e)
			}
			for rows.Next() {
				var kind, conv, state string
				var r1, r2, e1, e2 int64
				_ = rows.Scan(&kind, &conv, &r1, &r2, &e1, &e2, &state)
				t.Log(kind, conv, r1, r2, e1, e2, state)
			}
			_ = rows.Close()
			t.Fatalf("generation failed: %+v", rebuilt)
		}
		progress, err := ingest.Progress(scopes[rebuildIndex], receipts[rebuildIndex].ID)
		if err != nil || progress.Status != "complete" || progress.GenerationID != rebuilt.GenerationID {
			t.Fatalf("rebuilt watermark %+v %v", progress, err)
		}
		if episodes {
			carried, e := memories.ListDocuments(scopes[rebuildIndex], owner, []domain.MemoryType{domain.MemoryTypeEpisode}, 20)
			if e != nil || len(carried) == 0 {
				t.Fatal("generation switch lost episodes", e)
			}
		}
		if rebuildIndex == 0 {
			preserved, e := life.Inspect(privateAdmin, approved.ResultNodeID)
			if e != nil || preserved.Content != candidate.Content || preserved.GenerationID != rebuilt.GenerationID {
				t.Fatalf("curated record lost after index switch: %+v %v", preserved, e)
			}
		}
	}

	rpcAdmin := pb.NewWorkspaceAdminServiceClient(connection)
	adminRPC := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-admin-token", "fixture-admin"))
	for i := range receipts {
		list, e := rpcAdmin.Generations(adminRPC, &pb.AdminWorkspaceScope{UserId: owner, WorkspaceId: receipts[i].WorkspaceID})
		if e != nil || len(list.GetItems()) < 2 {
			t.Fatalf("gRPC generation parity %v %v", list, e)
		}
		var response struct {
			Errors []map[string]any `json:"errors"`
			Data   map[string]any   `json:"data"`
		}
		code := request("fixture-admin", "POST", "/graphql", "", map[string]any{"query": `query($scope:WorkspaceScope!,$agent:ID!){workspaceMemoryGenerations(scope:$scope){generation_id workspace_id is_active status metadata}workspaceMemoryCapabilities(scope:$scope){contract_version capabilities runtime{ai_backend role_aliases}}workspaceAgentDetail(scope:$scope,agent:$agent){agent{id}grant{workspace_id}effective_capabilities}}`, "variables": map[string]any{"scope": map[string]any{"user_id": owner, "workspace_id": receipts[i].WorkspaceID}, "agent": agents[i].ID}}, &response)
		if code != 200 || len(response.Errors) > 0 || len(response.Data) != 3 {
			t.Fatalf("GraphQL parity: %d %+v", code, response)
		}
	}
	var charged int
	if err = db.QueryRow(`SELECT count(*) FROM token_usages`).Scan(&charged); err != nil || charged != 0 {
		t.Fatal("broker calls double charged", charged, err)
	}
	var calls int
	if err = db.QueryRow(`SELECT count(*) FROM memory_ai_calls WHERE status='complete' AND broker_usage_id<>''`).Scan(&calls); err != nil || calls < 6 {
		t.Fatal("broker ledger incomplete", calls, err)
	}
	var operation domain.MemoryOperation
	engine.SetRerankConfig(memoryservice.RerankConfig{Model: "memory.rerank"})
	invoked := false
	engine.SetReranker(contractReranker(func(callCtx context.Context, in domain.RerankInput) ([]domain.RerankResult, error) {
		invoked = true
		var e error
		operation, e = life.Forget(scopes[0], domain.ForgetSelector{SourceRefs: []domain.SourceRef{{WorkspaceID: receipts[0].WorkspaceID, MessageID: receipts[0].Events[0].MessageID, Revision: 1}}}, "forget-source")
		if e != nil {
			return nil, e
		}
		scores := []domain.RerankResult{}
		for i, d := range in.Documents {
			scores = append(scores, domain.RerankResult{DocumentID: d.ID, Index: i, Score: .8})
		}
		return scores, nil
	}))
	recalledAfterRetraction, err := memory.Search(scopes[0], domain.SearchInput{UserID: owner, Query: "同名的人在哪里工作", TopN: 10})
	if err != nil || !invoked || len(recalledAfterRetraction.Memories) != 0 {
		t.Fatalf("retraction during rerank leaked: invoked=%v count=%d err=%v", invoked, len(recalledAfterRetraction.Memories), err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if code := request(tokens[0], "GET", "/api/v1/sources/"+receipts[0].Events[0].MessageID, "", nil, nil); code != 404 {
		t.Fatalf("deleted source readable %d", code)
	}
	// Archive a real Redis task, then replay the exact task ID through the
	// administration service. The original failed job must stop blocking progress.
	failCleanup.Store(true)
	jobID := "cleanup-dlq-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"schema_version": 2, "user_id": owner, "workspace_id": receipts[0].WorkspaceID, "operation_id": operation.ID})
	if _, err = client.EnqueueContext(ctx, asynq.NewTask("memory.lifecycle", payload), asynq.Queue(queue), asynq.TaskID(jobID), asynq.MaxRetry(0)); err != nil {
		t.Fatal(err)
	}
	waitJob := func(want string) {
		t.Helper()
		until := time.Now().Add(5 * time.Second)
		for time.Now().Before(until) {
			var state string
			e := db.QueryRow(`SELECT status FROM memory_jobs WHERE id=$1`, jobID).Scan(&state)
			if e == nil && state == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("job did not reach", want)
	}
	waitJob("dead_letter")
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		info, e := inspector.GetTaskInfo(queue, jobID)
		if e == nil && info.State == asynq.TaskStateArchived {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	failCleanup.Store(false)
	controlCtx, err := identity.AdministrativeDataContext(admin, owner, receipts[0].WorkspaceID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = control.Replay(controlCtx, jobID); err != nil {
		t.Fatal(err)
	}
	waitJob("complete")
	operation, err = life.Operation(scopes[0], operation.ID)
	if err != nil || !operation.OnlineCleaned || !operation.JournalPersisted {
		t.Fatalf("erase incomplete %+v %v", operation, err)
	}
	grant, err := identity.WorkspaceGrant(admin, owner, receipts[1].WorkspaceID, agents[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetReranker(contractReranker(func(callCtx context.Context, in domain.RerankInput) ([]domain.RerankResult, error) {
		grant.Capabilities = []string{}
		if _, e := identity.SetGrant(admin, grant); e != nil {
			return nil, e
		}
		scores := []domain.RerankResult{}
		for i, d := range in.Documents {
			scores = append(scores, domain.RerankResult{DocumentID: d.ID, Index: i, Score: .8})
		}
		return scores, nil
	}))
	if _, err = memory.Search(scopes[1], domain.SearchInput{UserID: owner, Query: "工作在哪里", TopN: 10}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("revocation during rerank: %v", err)
	}
}

type contractReranker func(context.Context, domain.RerankInput) ([]domain.RerankResult, error)

func (f contractReranker) Rerank(ctx context.Context, in domain.RerankInput) ([]domain.RerankResult, error) {
	return f(ctx, in)
}
