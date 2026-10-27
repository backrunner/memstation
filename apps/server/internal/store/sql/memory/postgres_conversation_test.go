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
)

const captureConversationDriverName = "memstation-capture-conversation"

var registerCaptureConversationDriverOnce sync.Once

func registerCaptureConversationDriver(t *testing.T, rows [][]any) *captureConversationDriver {
	t.Helper()

	driver := &captureConversationDriver{rows: rows}
	registerCaptureConversationDriverOnce.Do(func() {
		sql.Register(captureConversationDriverName, driver)
	})
	return driver
}

type captureConversationDriver struct {
	mu       sync.Mutex
	query    string
	args     []any
	rowCount int
	rows     [][]any
}

func (d *captureConversationDriver) Open(string) (sqldriver.Conn, error) {
	return &captureConversationConn{driver: d}, nil
}

type captureConversationConn struct {
	driver *captureConversationDriver
}

func (c *captureConversationConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, io.EOF
}

func (c *captureConversationConn) Close() error { return nil }

func (c *captureConversationConn) Begin() (sqldriver.Tx, error) {
	return nil, io.EOF
}

func (c *captureConversationConn) QueryContext(_ context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	values := make([]any, 0, len(args))
	for _, arg := range args {
		values = append(values, arg.Value)
	}
	c.driver.mu.Lock()
	c.driver.query = query
	c.driver.args = values
	c.driver.rowCount++
	rows := append([][]any(nil), c.driver.rows...)
	c.driver.mu.Unlock()
	return &captureConversationRows{rows: rows}, nil
}

func (c *captureConversationConn) CheckNamedValue(*sqldriver.NamedValue) error { return nil }

type captureConversationRows struct {
	rows [][]any
	idx  int
}

func (r *captureConversationRows) Columns() []string {
	return []string{
		"id", "node_id", "generation_id", "conversation_id", "content",
		"importance", "decay_factor", "activity_at", "time_tier", "created_at",
		"updated_at", "metadata", "memory_type",
	}
}

func (r *captureConversationRows) Close() error { return nil }

func (r *captureConversationRows) Next(dest []sqldriver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	for i, value := range r.rows[r.idx] {
		dest[i] = value
	}
	r.idx++
	return nil
}

func TestPostgresMemoryRepositoryListConversationDocumentsInGenerationUsesSingleUnionQuery(t *testing.T) {
	t.Parallel()

	metadata, err := json.Marshal(map[string]any{"topic_label": "rollout"})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	activityAt := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	createdAt := activityAt.Add(-time.Hour)
	updatedAt := activityAt

	driver := registerCaptureConversationDriver(t, [][]any{{
		"summary-1",
		"node-summary-1",
		"gen-1",
		"conv-1",
		"summary content",
		1.0,
		1.0,
		activityAt,
		"active",
		createdAt,
		updatedAt,
		metadata,
		"summary",
	}})

	db, err := sql.Open(captureConversationDriverName, "")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := NewPostgresMemoryRepository(db)
	docs, err := repo.ListConversationDocumentsInGeneration(context.Background(), "user-1", "gen-1", "conv-1", true)
	if err != nil {
		t.Fatalf("list conversation documents: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Type != "" && docs[0].Type != "summary" {
		t.Fatalf("unexpected document type: %s", docs[0].Type)
	}
	if docs[0].ID != "summary-1" || docs[0].NodeID != "node-summary-1" {
		t.Fatalf("unexpected document: %+v", docs[0])
	}

	driver.mu.Lock()
	query := driver.query
	args := append([]any(nil), driver.args...)
	rowCount := driver.rowCount
	driver.mu.Unlock()

	if rowCount != 1 {
		t.Fatalf("expected one query call, got %d", rowCount)
	}
	if !strings.Contains(query, "UNION ALL") {
		t.Fatalf("expected union query, got %q", query)
	}
	if !strings.Contains(query, "conversation_segments cs") {
		t.Fatalf("expected segment branch when includeSegments=true, got %q", query)
	}
	if len(args) != 4 {
		t.Fatalf("expected 4 args with generation filter, got %d: %#v", len(args), args)
	}
	if args[0] != "user-1" || args[1] != "conv-1" || args[2] != 500 || args[3] != "gen-1" {
		t.Fatalf("unexpected args: %#v", args)
	}
}
