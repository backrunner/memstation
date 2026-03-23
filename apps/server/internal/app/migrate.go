package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const fallbackMigrationTable = "memstation_schema_migrations"

func applySQLMigrations(ctx context.Context, db *sql.DB, logger *slog.Logger, migrationsDir string) error {
	if db == nil {
		return errors.New("database is not configured")
	}

	if _, err := db.ExecContext(ctx, `
        CREATE TABLE IF NOT EXISTS `+fallbackMigrationTable+` (
            version TEXT PRIMARY KEY,
            applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
    `); err != nil {
		return err
	}

	applied := map[string]struct{}{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM `+fallbackMigrationTable)
	if err != nil {
		return err
	}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			_ = rows.Close()
			return err
		}
		applied[version] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return err
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version := entry.Name()
		if _, ok := applied[version]; ok {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(migrationsDir, version))
		if err != nil {
			return err
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+fallbackMigrationTable+` (version, applied_at) VALUES ($1, $2)`, version, time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if logger != nil {
			logger.InfoContext(ctx, "sql migration applied", "version", version)
		}
	}

	return nil
}
