package eval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	vectorstore "memstation/apps/server/internal/store/vector"
)

// OpenEvaluationDatabase prevents a manual load run from targeting the regular
// application database. The caller must migrate a dedicated loopback database.
func OpenEvaluationDatabase(ctx context.Context, dsn string) (*sql.DB, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || (!strings.HasPrefix(u.Path, "/memstation_eval_") && !strings.HasPrefix(u.Path, "/memstation_quality_")) {
		return nil, fmt.Errorf("requires a dedicated loopback memstation_eval_ or memstation_quality_ database")
	}
	for key := range u.Query() {
		switch key {
		case "sslmode", "connect_timeout", "application_name":
		default:
			return nil, fmt.Errorf("evaluation database URL contains an unsupported connection override")
		}
	}
	return bootstrap.OpenPostgres(ctx, bootstrap.Config{DatabaseURL: dsn})
}

type StoreLoadOptions struct {
	LiveOptions
	Database          *sql.DB
	Embedding         EmbeddingOptions
	Rows              int
	Requests          int
	ConcurrencyLevels []int
	RequestsPerSecond float64
}
type StoreLoadSample struct {
	Query         string  `json:"query"`
	Millis        float64 `json:"millis"`
	Returned      int     `json:"returned"`
	WrongUserHits int     `json:"wrong_user_hits"`
	Error         string  `json:"error,omitempty"`
}
type StoreLoadPhase struct {
	Concurrency    int               `json:"concurrency"`
	CacheMode      string            `json:"cache_mode"`
	PeakInFlight   int               `json:"peak_in_flight"`
	OfferedRPS     float64           `json:"offered_rps"`
	ArrivalMode    string            `json:"arrival_mode"`
	Requests       int               `json:"requests"`
	Errors         int               `json:"errors"`
	EmptyResults   int               `json:"empty_results"`
	WrongUserHits  int               `json:"wrong_user_hits"`
	P50Millis      float64           `json:"p50_millis"`
	P95Millis      float64           `json:"p95_millis"`
	MaxMillis      float64           `json:"max_millis"`
	ElapsedSeconds float64           `json:"elapsed_seconds"`
	CompletedRPS   float64           `json:"completed_rps"`
	Samples        []StoreLoadSample `json:"samples"`
}
type StoreLoadReport struct {
	GeneratedAt     time.Time        `json:"generated_at"`
	Embedding       EmbeddingRunInfo `json:"embedding"`
	Rows            int              `json:"rows"`
	UniqueTexts     int              `json:"unique_texts"`
	Dimensions      int              `json:"dimensions"`
	UserID          string           `json:"user_id"`
	DataShape       string           `json:"data_shape"`
	PostgresVersion string           `json:"postgres_version"`
	Phases          []StoreLoadPhase `json:"phases"`
	Explain         json.RawMessage  `json:"explain"`
}

// RunStoreLoad stores real provider vectors through the production repositories,
// then measures the authenticated HTTP memory endpoint. Repeated source texts are
// deliberate capacity fixtures, not extra independent quality judgments.
func RunStoreLoad(ctx context.Context, dataset Dataset, options StoreLoadOptions) (StoreLoadReport, error) {
	if options.Database == nil || options.Rows < 1 || options.Requests < 1 || !strings.HasPrefix(options.Prefix, "eval-") || len(dataset.Documents) == 0 || len(dataset.Queries) == 0 {
		return StoreLoadReport{}, fmt.Errorf("store load requires a dedicated database, eval- prefix, nonempty fixture and positive row/request counts")
	}
	u, err := url.Parse(options.BaseURL)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || options.AdminToken == "" {
		return StoreLoadReport{}, fmt.Errorf("store load requires a loopback HTTP service and admin credential")
	}
	embedding, err := normalizeEmbeddingOptions(options.Embedding)
	if err != nil {
		return StoreLoadReport{}, err
	}
	if embedding.Mode != EmbeddingModeProvider {
		return StoreLoadReport{}, fmt.Errorf("store capacity measurement requires real provider embeddings")
	}
	embedder, err := newEvalEmbeddingClient(embedding)
	if err != nil {
		return StoreLoadReport{}, err
	}
	owner := options.Prefix + "-capacity"
	report := StoreLoadReport{GeneratedAt: time.Now().UTC(), Embedding: embedding.runInfo(), Rows: options.Rows, UserID: owner, DataShape: "repeated original synthetic fixture texts with reused real model vectors; capacity measurement only, not independent quality cases"}
	_ = options.Database.QueryRowContext(ctx, "SELECT version()").Scan(&report.PostgresVersion)
	jar, err := cookiejar.New(nil)
	if err != nil {
		return report, err
	}
	client := liveClient{client: &http.Client{Jar: jar, Timeout: 3 * time.Minute}, base: strings.TrimRight(options.BaseURL, "/")}
	if _, err := client.call(ctx, "POST", "/api/admin/login", map[string]string{"token": options.AdminToken}, nil, &map[string]any{}); err != nil {
		return report, err
	}
	var key struct {
		APIKey string `json:"api_key"`
		Record struct {
			ID string `json:"id"`
		} `json:"record"`
	}
	if _, err := client.call(ctx, "POST", "/api/admin/users/"+url.PathEscape(owner)+"/api-keys", map[string]string{"name": "Synthetic capacity client"}, nil, &key); err != nil {
		return report, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = client.call(cleanup, "DELETE", "/api/admin/users/"+url.PathEscape(owner)+"/api-keys/"+url.PathEscape(key.Record.ID), nil, nil, nil)
	}()
	var serviceMatches bool
	if err := options.Database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE id=$1 AND user_id=$2)`, key.Record.ID, owner).Scan(&serviceMatches); err != nil {
		return report, err
	}
	if !serviceMatches {
		return report, fmt.Errorf("HTTP service and evaluation database do not match")
	}
	unique := []string{}
	seen := map[string]bool{}
	for _, d := range dataset.Documents {
		if !seen[d.Content] {
			seen[d.Content] = true
			unique = append(unique, d.Content)
		}
	}
	templates := make([]domain.EmbeddingResult, len(unique))
	for i, text := range unique {
		templates[i], err = embedder.Embed(ctx, domain.EmbeddingInput{UserID: owner, Text: text, Model: embedding.Model})
		if err != nil {
			return report, err
		}
		if i == 0 {
			report.Dimensions = len(templates[i].Vector)
		}
		if len(templates[i].Vector) != report.Dimensions {
			return report, fmt.Errorf("inconsistent provider vector dimensions")
		}
	}
	report.UniqueTexts = len(unique)
	repo := memorystore.NewPostgresMemoryRepository(options.Database)
	vectors := vectorstore.NewPGVectorStore(runtimeconfig.NewStaticDBHandle(options.Database))
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	jobs := make(chan int)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				mu.Lock()
				failed := firstErr != nil
				mu.Unlock()
				if failed {
					continue
				}
				index := i % len(unique)
				id := fmt.Sprintf("%s-%05d", owner, i)
				doc := domain.MemoryDocument{ID: id, NodeID: "node:" + id, UserID: owner, Type: domain.MemoryTypeEntity, Content: unique[index], Importance: 1, DecayFactor: 1, Metadata: map[string]any{"status": "active", "confidence": 1, "canonical_name": fmt.Sprintf("Synthetic archive %d", i), "entity_kind": "test_record"}}
				err := repo.StoreDocument(ctx, doc)
				if err == nil {
					err = vectors.Upsert(ctx, domain.VectorUpsertRequest{UserID: owner, NodeID: doc.NodeID, NodeType: string(doc.Type), GenerationID: "gen:current:" + owner, EmbeddingModel: embedding.Model, EmbeddingVersion: 1, Content: doc.Content, Vector: templates[index].Vector})
				}
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for i := range options.Rows {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return report, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return report, firstErr
	}
	var stored, ready int
	err = options.Database.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE n.embedding_status='ready' AND e.node_id IS NOT NULL) FROM memory_nodes n LEFT JOIN memory_embeddings e ON e.node_id=n.id AND e.user_id=n.user_id WHERE n.user_id=$1 AND n.node_type='entity'`, owner).Scan(&stored, &ready)
	if err != nil {
		return report, err
	}
	if stored != options.Rows || ready != options.Rows {
		return report, fmt.Errorf("capacity corpus incomplete: nodes=%d ready=%d expected=%d", stored, ready, options.Rows)
	}
	fmt.Printf("Persistent capacity corpus ready: %d nodes, %d unique texts, %d dimensions\n", ready, report.UniqueTexts, report.Dimensions)
	levels := options.ConcurrencyLevels
	if len(levels) == 0 {
		levels = []int{1, 4, 8}
	}
	for _, cacheMode := range []string{"repeated_queries", "unique_queries"} {
		for _, concurrency := range levels {
			if concurrency < 1 {
				return report, fmt.Errorf("invalid concurrency")
			}
			// Warmups are separate from the measured sample; query embedding is not cached
			// by the evaluator. Each call still traverses auth, retrieval and PostgreSQL.
			for i := 0; i < len(dataset.Queries); i++ {
				var result domain.SearchResult
				if _, err := client.call(ctx, "POST", "/api/v1/memory/search", map[string]any{"query": dataset.Queries[i%len(dataset.Queries)].Query, "top_n": 5}, liveHeaders(key.APIKey, "transparent", ""), &result); err != nil {
					return report, err
				}
			}
			if options.RequestsPerSecond == 0 {
				// Refill the default local token bucket before a bounded burst, so
				// warmup traffic is not mistaken for capacity failure.
				select {
				case <-ctx.Done():
					return report, ctx.Err()
				case <-time.After(2 * time.Second):
				}
			}
			phase := StoreLoadPhase{Concurrency: concurrency, CacheMode: cacheMode, Requests: options.Requests, Samples: make([]StoreLoadSample, options.Requests)}
			phase.OfferedRPS = options.RequestsPerSecond
			phase.ArrivalMode = "closed_loop"
			if options.RequestsPerSecond > 0 {
				phase.ArrivalMode = "paced_with_concurrency_cap"
			}
			var active, peak atomic.Int64
			start := time.Now()
			slots := make(chan struct{}, concurrency)
			var workers sync.WaitGroup
			interval := time.Duration(0)
			if options.RequestsPerSecond > 0 {
				interval = time.Duration(float64(time.Second) / options.RequestsPerSecond)
			}
			for i := range options.Requests {
				target := start.Add(time.Duration(i) * interval)
				if wait := time.Until(target); wait > 0 {
					select {
					case <-time.After(wait):
					case <-ctx.Done():
						return report, ctx.Err()
					}
				}
				slots <- struct{}{}
				workers.Add(1)
				go func(i int) {
					defer workers.Done()
					defer func() { <-slots }()
					now := active.Add(1)
					defer active.Add(-1)
					for previous := peak.Load(); now > previous; previous = peak.Load() {
						if peak.CompareAndSwap(previous, now) {
							break
						}
					}
					q := dataset.Queries[i%len(dataset.Queries)].Query
					if cacheMode == "unique_queries" {
						q += fmt.Sprintf(" [evaluation_request_%d_%d_%d]", report.GeneratedAt.UnixNano(), concurrency, i)
					}
					s := StoreLoadSample{Query: q}
					started := time.Now()
					var result domain.SearchResult
					_, err := client.call(ctx, "POST", "/api/v1/memory/search", map[string]any{"query": q, "top_n": 5}, liveHeaders(key.APIKey, "transparent", ""), &result)
					s.Millis = float64(time.Since(started).Nanoseconds()) / 1e6
					s.Returned = len(result.Memories)
					if err != nil {
						s.Error = err.Error()
					}
					for _, d := range result.Memories {
						if d.UserID != owner {
							s.WrongUserHits++
						}
					}
					phase.Samples[i] = s
				}(i)
			}
			workers.Wait()
			phase.PeakInFlight = int(peak.Load())
			phase.ElapsedSeconds = time.Since(start).Seconds()
			phase.CompletedRPS = float64(options.Requests) / phase.ElapsedSeconds
			latencies := make([]float64, 0, len(phase.Samples))
			for _, s := range phase.Samples {
				latencies = append(latencies, s.Millis)
				if s.Error != "" {
					phase.Errors++
				}
				if s.Returned == 0 {
					phase.EmptyResults++
				}
				phase.WrongUserHits += s.WrongUserHits
			}
			sort.Float64s(latencies)
			phase.P50Millis = percentile(latencies, .5)
			phase.P95Millis = percentile(latencies, .95)
			phase.MaxMillis = percentile(latencies, 1)
			report.Phases = append(report.Phases, phase)
			fmt.Printf("HTTP recall %s concurrency=%d P95=%.1fms errors=%d empty=%d\n", cacheMode, concurrency, phase.P95Millis, phase.Errors, phase.EmptyResults)
		}
	}
	vectorJSON, _ := json.Marshal(templates[0].Vector)
	var plan []byte
	err = options.Database.QueryRowContext(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT e.node_id FROM memory_embeddings e JOIN memory_nodes n ON n.id=e.node_id AND n.user_id=e.user_id AND n.workspace_id=e.workspace_id AND n.embedding_status='ready' AND n.node_type=e.node_type AND n.generation_id=e.generation_id WHERE e.user_id=$1 AND e.embedding_version=1 AND e.workspace_id=$2 AND e.embedding_model=$3 AND e.generation_id=$4 ORDER BY e.vector <=> $5::vector LIMIT 80`, owner, domain.PersonalWorkspaceID(owner), embedding.Model, "gen:current:"+owner, string(vectorJSON)).Scan(&plan)
	if err == nil {
		report.Explain = plan
	}
	if err := os.MkdirAll(options.OutputDir, 0o700); err != nil {
		return report, err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	if err := os.WriteFile(filepath.Join(options.OutputDir, "store-load.json"), raw, 0o600); err != nil {
		return report, err
	}
	return report, nil
}
