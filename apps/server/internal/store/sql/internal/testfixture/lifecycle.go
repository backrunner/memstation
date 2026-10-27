package testfixture

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func IsolatedIngestionSchema(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "lifecycle_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema+",public")
	parsed.RawQuery = query.Encode()
	t.Setenv("MEMSTATION_TEST_DATABASE_URL", parsed.String())
	isolated, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, e := os.Stat(filepath.Join(root, "ent", "migrate", "migrations")); e == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("server migration directory not found")
		}
		root = parent
	}
	files, err := filepath.Glob(filepath.Join(root, "ent", "migrate", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("migrations missing")
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = isolated.Exec(string(raw)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	_ = isolated.Close()
}
