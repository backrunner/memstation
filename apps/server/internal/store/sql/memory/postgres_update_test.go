package memory

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

const updateCaptureDriverName = "memstation-capture-update"

var (
	registerUpdateCaptureDriverOnce sync.Once
	registeredUpdateCaptureDriver   *updateCaptureDriver
)

func registerUpdateCaptureDriver(t *testing.T, rows [][]any) *updateCaptureDriver {
	t.Helper()

	registerUpdateCaptureDriverOnce.Do(func() {
		registeredUpdateCaptureDriver = &updateCaptureDriver{}
		sql.Register(updateCaptureDriverName, registeredUpdateCaptureDriver)
	})
	registeredUpdateCaptureDriver.mu.Lock()
	defer registeredUpdateCaptureDriver.mu.Unlock()
	registeredUpdateCaptureDriver.beginCount = 0
	registeredUpdateCaptureDriver.commitCount = 0
	registeredUpdateCaptureDriver.rollbackCount = 0
	registeredUpdateCaptureDriver.execQueries = nil
	registeredUpdateCaptureDriver.execArgs = nil
	registeredUpdateCaptureDriver.query = ""
	registeredUpdateCaptureDriver.queryArgs = nil
	registeredUpdateCaptureDriver.queryCount = 0
	registeredUpdateCaptureDriver.rows = rows
	return registeredUpdateCaptureDriver
}

type updateCaptureDriver struct {
	mu            sync.Mutex
	beginCount    int
	commitCount   int
	rollbackCount int
	execQueries   []string
	execArgs      [][]any
	query         string
	queryArgs     []any
	queryCount    int
	rows          [][]any
}

func (d *updateCaptureDriver) Open(string) (sqldriver.Conn, error) {
	return &updateCaptureConn{driver: d}, nil
}

type updateCaptureConn struct {
	driver *updateCaptureDriver
}

func (c *updateCaptureConn) Prepare(string) (sqldriver.Stmt, error) { return nil, io.EOF }

func (c *updateCaptureConn) Close() error { return nil }

func (c *updateCaptureConn) Begin() (sqldriver.Tx, error) {
	return c.BeginTx(context.Background(), sqldriver.TxOptions{})
}

func (c *updateCaptureConn) BeginTx(context.Context, sqldriver.TxOptions) (sqldriver.Tx, error) {
	c.driver.mu.Lock()
	c.driver.beginCount++
	c.driver.mu.Unlock()
	return &updateCaptureTx{driver: c.driver}, nil
}

func (c *updateCaptureConn) CheckNamedValue(*sqldriver.NamedValue) error { return nil }

func (c *updateCaptureConn) ExecContext(_ context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	values := make([]any, 0, len(args))
	for _, arg := range args {
		values = append(values, arg.Value)
	}
	c.driver.mu.Lock()
	c.driver.execQueries = append(c.driver.execQueries, query)
	c.driver.execArgs = append(c.driver.execArgs, values)
	c.driver.mu.Unlock()
	return updateCaptureResult(1), nil
}

func (c *updateCaptureConn) QueryContext(_ context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	values := make([]any, 0, len(args))
	for _, arg := range args {
		values = append(values, arg.Value)
	}
	c.driver.mu.Lock()
	c.driver.query = query
	c.driver.queryArgs = values
	c.driver.queryCount++
	rows := append([][]any(nil), c.driver.rows...)
	if strings.Contains(query, "SELECT id,user_id,workspace_id,node_type,entity_id,partition_id,access_status FROM memory_nodes") || strings.Contains(query, "SELECT context_mode,partition_id,finalize_status FROM conversations") {
		rows = nil
	}
	if strings.Contains(query, "FROM memory_generations WHERE generation_id=$1 FOR SHARE") {
		rows = [][]any{{"user-1", domain.PersonalWorkspaceID("user-1"), "activated", true}}
	}
	c.driver.mu.Unlock()
	return &updateCaptureRows{rows: rows}, nil
}

type updateCaptureTx struct {
	driver *updateCaptureDriver
}

func (tx *updateCaptureTx) Commit() error {
	tx.driver.mu.Lock()
	tx.driver.commitCount++
	tx.driver.mu.Unlock()
	return nil
}

func (tx *updateCaptureTx) Rollback() error {
	tx.driver.mu.Lock()
	tx.driver.rollbackCount++
	tx.driver.mu.Unlock()
	return nil
}

type updateCaptureResult int64

func (r updateCaptureResult) LastInsertId() (int64, error) { return int64(r), nil }

func (r updateCaptureResult) RowsAffected() (int64, error) { return int64(r), nil }

type updateCaptureRows struct {
	rows [][]any
	idx  int
}

func (r *updateCaptureRows) Columns() []string {
	if len(r.rows) > 0 {
		columns := make([]string, len(r.rows[0]))
		for idx := range columns {
			columns[idx] = "col"
		}
		return columns
	}
	return []string{
		"id", "node_id", "generation_id", "conversation_id", "content",
		"importance", "decay_factor", "activity_at", "time_tier",
		"created_at", "updated_at", "metadata", "memory_type",
	}
}

func (r *updateCaptureRows) Close() error { return nil }

func (r *updateCaptureRows) Next(dest []sqldriver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	for i, value := range r.rows[r.idx] {
		dest[i] = value
	}
	r.idx++
	return nil
}

func TestPostgresMemoryRepositoryUpdateDocumentUpdatesNodeState(t *testing.T) {
	now := time.Date(2026, 5, 29, 13, 0, 0, 0, time.UTC)
	metadata, err := json.Marshal(map[string]any{"fact_key": "home_location"})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	driver := registerUpdateCaptureDriver(t, [][]any{{
		"fact-1",
		"node-fact-1",
		"gen-1",
		"conv-1",
		"updated fact content",
		1.0,
		1.0,
		now,
		"active",
		now.Add(-time.Hour),
		now,
		metadata,
		"fact",
	}})

	db, err := sql.Open(updateCaptureDriverName, "")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := NewPostgresMemoryRepository(db)
	doc, err := repo.UpdateDocument(context.Background(), "user-1", "fact-1", domain.MemoryTypeFact, "updated fact content")
	if err != nil {
		t.Fatalf("update document: %v", err)
	}
	if doc.Content != "updated fact content" {
		t.Fatalf("unexpected updated content: %+v", doc)
	}

	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.beginCount != 1 {
		t.Fatalf("expected one transaction begin, got %d", driver.beginCount)
	}
	if driver.commitCount != 1 {
		t.Fatalf("expected one transaction commit, got %d", driver.commitCount)
	}
	if len(driver.execQueries) != 5 {
		t.Fatalf("expected identity locks, document, node, and outbox writes, got %d", len(driver.execQueries))
	}
	if !strings.Contains(driver.execQueries[3], "UPDATE memory_nodes") {
		t.Fatalf("expected node update query, got %q", driver.execQueries[3])
	}
	if len(driver.execArgs[3]) != 6 {
		t.Fatalf("expected node update args, got %#v", driver.execArgs[3])
	}
	if _, ok := driver.execArgs[3][0].(time.Time); !ok {
		t.Fatalf("expected activity_at time arg, got %#v", driver.execArgs[3][0])
	}
	if driver.execArgs[3][1] != "active" {
		t.Fatalf("expected active time tier, got %#v", driver.execArgs[3][1])
	}
	if !strings.Contains(driver.execQueries[4], "INSERT INTO outbox_events") {
		t.Fatalf("expected transactional embedding outbox write, got %q", driver.execQueries[4])
	}
}

func TestPostgresMemoryRepositoryUpdateGenericDocumentUsesMemoryItems(t *testing.T) {
	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	metadata, err := json.Marshal(map[string]any{
		"item_type":     "task",
		"title":         "Rollout follow-up",
		"status":        "pending",
		"confidence":    0.82,
		"review_status": "auto",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	driver := registerUpdateCaptureDriver(t, [][]any{{
		"task-1",
		"node-task-1",
		"gen-1",
		"conv-1",
		"updated task content",
		1.0,
		1.0,
		now,
		"active",
		now.Add(-time.Hour),
		now,
		metadata,
		"task",
	}})

	db, err := sql.Open(updateCaptureDriverName, "")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := NewPostgresMemoryRepository(db)
	doc, err := repo.UpdateDocument(context.Background(), "user-1", "task-1", domain.MemoryTypeTask, "updated task content")
	if err != nil {
		t.Fatalf("update generic document: %v", err)
	}
	if doc.Type != domain.MemoryTypeTask || doc.Content != "updated task content" {
		t.Fatalf("unexpected updated generic document: %+v", doc)
	}

	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.execQueries) != 5 {
		t.Fatalf("expected identity locks, document, node, and outbox writes, got %d", len(driver.execQueries))
	}
	if !strings.Contains(driver.execQueries[2], "UPDATE memory_items") {
		t.Fatalf("expected memory_items update query, got %q", driver.execQueries[2])
	}
	if len(driver.execArgs[2]) != 5 || driver.execArgs[2][4] != "task" {
		t.Fatalf("expected generic update args to include item type, got %#v", driver.execArgs[2])
	}
	if !strings.Contains(driver.execQueries[4], "INSERT INTO outbox_events") {
		t.Fatalf("expected transactional embedding outbox write, got %q", driver.execQueries[4])
	}
}

func TestPostgresMemoryRepositoryListDocumentsByNodeIDsKeepsKeyMemoryGenerationless(t *testing.T) {
	now := time.Date(2026, 7, 9, 9, 0, 0, 0, time.UTC)
	metadata, err := json.Marshal(map[string]any{
		"key":    "timezone",
		"value":  "Asia/Singapore",
		"source": "manual",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	driver := registerUpdateCaptureDriver(t, [][]any{{
		"key-1",
		"node:key:user-1:timezone",
		"",
		"",
		"timezone: Asia/Singapore",
		1.0,
		1.0,
		now,
		"active",
		now.Add(-time.Hour),
		now,
		metadata,
		"key_memory",
	}})

	db, err := sql.Open(updateCaptureDriverName, "")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := NewPostgresMemoryRepository(db)
	docs, err := repo.ListDocumentsByNodeIDsInGeneration(context.Background(), "user-1", "gen:rebuilt:user-1", []string{"node:key:user-1:timezone"})
	if err != nil {
		t.Fatalf("list documents by node ids: %v", err)
	}
	if len(docs) != 1 || docs[0].Type != domain.MemoryTypeKeyMemory {
		t.Fatalf("expected generationless key memory to be returned, got %+v", docs)
	}

	driver.mu.Lock()
	query := driver.query
	driver.mu.Unlock()
	keyBranchStart := strings.Index(query, "JOIN key_memories")
	if keyBranchStart < 0 {
		t.Fatalf("expected key memory branch in query: %s", query)
	}
	keyBranch := query[keyBranchStart:]
	if end := strings.Index(keyBranch, ") docs"); end >= 0 {
		keyBranch = keyBranch[:end]
	}
	if strings.Contains(keyBranch, "generation_id") {
		t.Fatalf("key memory branch should remain generationless, got: %s", keyBranch)
	}
}

func TestPostgresMemoryRepositoryStoreDocumentDerivesStableNodeID(t *testing.T) {
	driver := registerUpdateCaptureDriver(t, nil)

	db, err := sql.Open(updateCaptureDriverName, "")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := NewPostgresMemoryRepository(db)
	if err := repo.StoreDocument(context.Background(), domain.MemoryDocument{
		ID:      "task-1",
		UserID:  "user-1",
		Type:    domain.MemoryTypeTask,
		Content: "Task: follow up on benchmark artifacts",
	}); err != nil {
		t.Fatalf("store document: %v", err)
	}

	expectedNodeID := "node:task:user-1:task-1"
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.execArgs) < 2 {
		t.Fatalf("expected memory node and item writes, got %d exec calls", len(driver.execArgs))
	}
	foundNode, foundItem := false, false
	for i, query := range driver.execQueries {
		if strings.Contains(query, "INSERT INTO memory_nodes") && driver.execArgs[i][0] == expectedNodeID {
			foundNode = true
		}
		if strings.Contains(query, "INSERT INTO memory_items") && driver.execArgs[i][4] == expectedNodeID {
			foundItem = true
		}
	}
	if !foundNode || !foundItem {
		t.Fatal("memory node/item writes did not use the stable node ID")
	}
}

func TestPostgresMemoryRepositoryDeleteDocumentDeletesEmbeddings(t *testing.T) {
	driver := registerUpdateCaptureDriver(t, [][]any{{"node-task-1"}})

	db, err := sql.Open(updateCaptureDriverName, "")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := NewPostgresMemoryRepository(db)
	if err := repo.DeleteDocument(context.Background(), "user-1", "task-1", domain.MemoryTypeTask); err != nil {
		t.Fatalf("delete generic document: %v", err)
	}

	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.beginCount != 1 {
		t.Fatalf("expected one transaction begin, got %d", driver.beginCount)
	}
	if driver.commitCount != 1 {
		t.Fatalf("expected one transaction commit, got %d", driver.commitCount)
	}
	foundEmbeddingDelete := false
	for _, query := range driver.execQueries {
		if strings.Contains(query, "DELETE FROM memory_embeddings") {
			foundEmbeddingDelete = true
			break
		}
	}
	if !foundEmbeddingDelete {
		t.Fatalf("expected delete document to remove memory embeddings, got queries %#v", driver.execQueries)
	}
}

func TestPostgresMemoryRepositoryDeleteSegmentsByConversationDeletesEmbeddings(t *testing.T) {
	driver := registerUpdateCaptureDriver(t, [][]any{{"node-segment-stale", "segment-stale"}})

	db, err := sql.Open(updateCaptureDriverName, "")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := NewPostgresMemoryRepository(db)
	deletedNodeIDs, err := repo.DeleteSegmentsByConversation(context.Background(), "user-1", "conv-1", nil)
	if err != nil {
		t.Fatalf("delete segments by conversation: %v", err)
	}
	if len(deletedNodeIDs) != 1 || deletedNodeIDs[0] != "node-segment-stale" {
		t.Fatalf("expected deleted stale node, got %+v", deletedNodeIDs)
	}

	driver.mu.Lock()
	defer driver.mu.Unlock()
	foundEmbeddingDelete := false
	for _, query := range driver.execQueries {
		if strings.Contains(query, "DELETE FROM memory_embeddings") {
			foundEmbeddingDelete = true
			break
		}
	}
	if !foundEmbeddingDelete {
		t.Fatalf("expected stale segment cleanup to remove memory embeddings, got queries %#v", driver.execQueries)
	}
}
