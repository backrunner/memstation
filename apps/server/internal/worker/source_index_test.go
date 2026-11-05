package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/processing"
	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"
)

func TestSourceIndexPreservesRawOnExtractorFailureAndCachesValidatedMentions(t *testing.T) {
	for _, variant := range []string{"lightweight", "hybrid", "generative_entities", "deferred_budget", "outage"} {
		t.Run(variant, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if variant == "outage" {
					w.WriteHeader(503)
					return
				}
				var body struct {
					Texts []string `json:"texts"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				result := processing.Response{Version: 1, Model: "encoder", Revision: "pinned", Splitter: "char", Observations: []processing.Observation{}}
				for _, text := range body.Texts {
					runes := []rune(text)
					score := 0.
					if variant != "lightweight" {
						score = 1
					}
					result.Observations = append(result.Observations, processing.Observation{Supported: true, NeedsSemantic: score, Spans: []processing.Span{{Start: 0, End: 2, Text: string(runes[:2]), Label: "location", Confidence: .9}}})
				}
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			strategy := "hybrid"
			if variant == "lightweight" {
				strategy = "lightweight"
			}
			if variant == "generative_entities" {
				strategy = "generative"
			}
			budget := 4096
			if variant == "deferred_budget" {
				budget = 1
			}
			processor, err := processing.New(processing.Config{Strategy: strategy, EntityRecallTimeoutMS: 200, Endpoint: server.URL, Token: "fixture-local-token", Model: "encoder", Revision: "pinned", Splitter: "char", Threshold: .75, MaxSemanticBytes: budget}, nil)
			if err != nil {
				t.Fatal(err)
			}
			repo, memories, publisher := sourceIndexFixture(t)
			services := Services{Conversations: repo, Memories: memories, Publisher: publisher, TaskAuthority: fixtureTaskAuthority{}, Processing: processor}
			task := newTask(t, TypeSourceIndex, SummaryPayload{ProcessingPolicyID: processor.Config().Identity(), UserID: "owner", ConversationID: "conversation"})
			err = handleSourceIndex(nilLogger(), services)(context.Background(), task)
			if variant == "outage" && err == nil {
				t.Fatal("extractor outage hidden")
			}
			if variant != "outage" && err != nil {
				t.Fatal(err)
			}
			docs, err := memories.ListDocuments(context.Background(), "owner", []domain.MemoryType{domain.MemoryTypeSegment}, 20)
			if err != nil || len(docs) == 0 {
				t.Fatalf("raw sources unavailable after processing: %v", err)
			}
			c, err := repo.GetConversation(context.Background(), "owner", "conversation")
			if err != nil {
				t.Fatal(err)
			}
			if variant == "outage" {
				if c.Metadata["source_index_status"] != "stored" {
					t.Fatal("lost source indexing status")
				}
				return
			}
			if (variant == "hybrid" || variant == "generative_entities") && c.Metadata["semantic_status"] != "queued" {
				t.Fatal("semantic signal not promoted")
			}
			if variant == "deferred_budget" && c.Metadata["semantic_status"] != "deferred_budget" {
				t.Fatal("budget exhaustion reported as completed")
			}
			entities, err := memories.ListDocuments(context.Background(), "owner", []domain.MemoryType{domain.MemoryTypeEntity}, 20)
			if err != nil || len(entities) == 0 {
				t.Fatalf("mentions not persisted: %v", err)
			}
			facts, _ := memories.ListDocuments(context.Background(), "owner", []domain.MemoryType{domain.MemoryTypeFact}, 20)
			if len(facts) != 0 {
				t.Fatal("mentions promoted into unsupported facts")
			}
			if err = handleSourceIndex(nilLogger(), services)(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("retry repeated validated local inference")
			}
		})
	}
}

func sourceIndexFixture(t *testing.T) (*conversationstore.InMemoryConversationRepository, *memorystore.InMemoryMemoryRepository, *recordingPublisher) {
	t.Helper()
	repo := conversationstore.NewInMemoryConversationRepository()
	if err := repo.PersistExchange(context.Background(), domain.ConversationRecord{UserID: "owner", ConversationID: "conversation", AgentID: "fixture-agent", FinalizeStatus: domain.FinalizeStatusEnded, UpdatedAt: time.Now().UTC(), Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "上海是本次项目会议的地点。"}}}); err != nil {
		t.Fatal(err)
	}
	return repo, memorystore.NewInMemoryMemoryRepository(), &recordingPublisher{}
}

func TestLiveLightweightSourceIndex(t *testing.T) {
	endpoint := os.Getenv("MEMSTATION_TEST_EXTRACTOR_ENDPOINT")
	if endpoint == "" {
		t.Skip("explicit local model endpoint required")
	}
	processor, err := processing.New(processing.Config{Strategy: "lightweight", EntityRecallTimeoutMS: 5000, Endpoint: endpoint, Token: os.Getenv("MEMSTATION_TEST_EXTRACTOR_TOKEN"), Model: os.Getenv("MEMSTATION_TEST_EXTRACTOR_MODEL"), Revision: os.Getenv("MEMSTATION_TEST_EXTRACTOR_REVISION"), Splitter: "char", Threshold: .75, MaxSemanticBytes: 4096}, nil)
	if err != nil {
		t.Fatal(err)
	}
	repo, memories, publisher := sourceIndexFixture(t)
	services := Services{Conversations: repo, Memories: memories, Publisher: publisher, TaskAuthority: fixtureTaskAuthority{}, Processing: processor}
	if err = handleSourceIndex(nilLogger(), services)(context.Background(), newTask(t, TypeSourceIndex, SummaryPayload{ProcessingPolicyID: processor.Config().Identity(), UserID: "owner", ConversationID: "conversation"})); err != nil {
		t.Fatal(err)
	}
	entities, err := memories.ListDocuments(context.Background(), "owner", []domain.MemoryType{domain.MemoryTypeEntity}, 20)
	if err != nil || len(entities) == 0 {
		t.Fatalf("real encoder produced no stored observations: %v", err)
	}
	engine := memoryservice.NewEngine(memories, nil)
	engine.SetEntityRecall(processor, 5*time.Second)
	input := domain.SearchInput{UserID: "owner", Query: "关于上海的项目会议，有哪些信息？", IncludeSegments: true, TopN: 20}
	result, err := engine.SearchWithVariant(context.Background(), input, memoryservice.SearchVariantFull)
	if err != nil || result.Result.EntityRecall == nil || result.Result.EntityRecall.Status != "complete" || result.Result.EntityRecall.MatchedNodes == 0 {
		t.Fatalf("live query index: %+v %v", result, err)
	}
	observed := false
	for _, components := range result.Components {
		if components.EntityScore > 0 {
			observed = true
		}
	}
	if !observed {
		t.Fatal("live entity relevance not recorded")
	}
	cached, err := engine.Search(context.Background(), input)
	if err != nil || !cached.EntityRecall.CacheHit {
		t.Fatalf("live query cache: %+v %v", cached, err)
	}
	for _, doc := range entities {
		if doc.Metadata["extraction_method"] != "lightweight" || len(doc.SubjectActorIDs) != 0 {
			t.Fatal("unverified identity or wrong engine")
		}
	}
}
