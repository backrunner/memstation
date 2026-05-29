package sql

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

var registerUpdateCaptureDriverOnce sync.Once

func registerUpdateCaptureDriver(t *testing.T, rows [][]any) *updateCaptureDriver {
	t.Helper()

	driver := &updateCaptureDriver{rows: rows}
	registerUpdateCaptureDriverOnce.Do(func() {
		sql.Register(updateCaptureDriverName, driver)
	})
	return driver
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
func (c *updateCaptureConn) Close() error                           { return nil }
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
	t.Parallel()

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
	if len(driver.execQueries) != 2 {
		t.Fatalf("expected two exec queries, got %d", len(driver.execQueries))
	}
	if !strings.Contains(driver.execQueries[1], "UPDATE memory_nodes") {
		t.Fatalf("expected node update query, got %q", driver.execQueries[1])
	}
	if len(driver.execArgs[1]) != 5 {
		t.Fatalf("expected node update args, got %#v", driver.execArgs[1])
	}
	if _, ok := driver.execArgs[1][0].(time.Time); !ok {
		t.Fatalf("expected activity_at time arg, got %#v", driver.execArgs[1][0])
	}
	if driver.execArgs[1][1] != "active" {
		t.Fatalf("expected active time tier, got %#v", driver.execArgs[1][1])
	}
}
