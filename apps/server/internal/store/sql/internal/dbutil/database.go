package dbutil

import (
	sqlpkg "database/sql"
	"fmt"
	"strings"
	"time"

	"memstation/apps/server/internal/runtimeconfig"
)

func ReadIDs(rows *sqlpkg.Rows) ([]string, error) {
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func CurrentDB(handle runtimeconfig.DBHandle) *sqlpkg.DB {
	if handle == nil {
		return nil
	}
	return handle.Current()
}

func Rollback(tx *sqlpkg.Tx) {
	_ = tx.Rollback()
}

func NullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func ZeroTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value
}

func NullableTimeValue(metadata map[string]any, key string) any {
	if metadata == nil {
		return nil
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return nil
		}
		return typed.UTC()
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return parsed.UTC()
		}
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return parsed.UTC()
		}
	}
	return nil
}

func Placeholders(start, count int) string {
	items := make([]string, 0, count)
	for idx := 0; idx < count; idx++ {
		items = append(items, fmt.Sprintf("$%d", start+idx))
	}
	return strings.Join(items, ",")
}
