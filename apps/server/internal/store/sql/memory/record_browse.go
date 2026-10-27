package memory

import (
	"context"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"
)

func (r *HybridMemoryRepository) BrowseKeyMemories(ctx context.Context, input domain.RecordBrowseQuery, position *domain.RecordPosition) ([]domain.KeyMemory, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).BrowseKeyMemories(ctx, input, position)
	}
	return r.fallback.BrowseKeyMemories(ctx, input, position)
}

func (r *InMemoryMemoryRepository) BrowseKeyMemories(ctx context.Context, input domain.RecordBrowseQuery, position *domain.RecordPosition) ([]domain.KeyMemory, error) {
	if _, err := security.DataScope(ctx, input.UserID, ""); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []domain.KeyMemory{}
	query := strings.ToLower(input.Query)
	for _, item := range r.keyMemories {
		if item.UserID != input.UserID || !scopestore.PersonalMemoryWorkspace(ctx, input.UserID, item.WorkspaceID) || !recordutilstore.RecordBefore(item.UpdatedAt, item.ID, position) {
			continue
		}
		if !strings.Contains(strings.ToLower(strings.Join([]string{item.ID, item.Key, item.Value, item.Category, item.ReviewStatus}, " ")), query) {
			continue
		}
		item.Metadata = recordutilstore.CloneMetadata(item.Metadata)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return recordutilstore.RecordBefore(items[j].UpdatedAt, items[j].ID, &domain.RecordPosition{UpdatedAt: items[i].UpdatedAt, ID: items[i].ID})
	})
	if input.Limit > 0 && len(items) > input.Limit {
		items = items[:input.Limit]
	}
	return items, nil
}

func (r *PostgresMemoryRepository) BrowseKeyMemories(ctx context.Context, input domain.RecordBrowseQuery, position *domain.RecordPosition) ([]domain.KeyMemory, error) {
	workspace, err := security.DataScope(ctx, input.UserID, "")
	if err != nil {
		return nil, err
	}
	var stamp *time.Time
	id := ""
	if position != nil {
		stamp = &position.UpdatedAt
		id = position.ID
	}
	rows, err := r.db.QueryContext(ctx, `
	 SELECT k.id,k.node_id,k.key,k.value,k.category,k.source,k.review_status,k.last_confirmed_at,k.created_at,k.updated_at,
	        n.importance,n.decay_factor,coalesce(n.activity_at,k.updated_at),coalesce(n.time_tier,''),n.partition_id
	 FROM key_memories k JOIN memory_nodes n ON n.user_id=k.user_id AND n.id=k.node_id AND n.workspace_id=$2 AND `+storeaccess.NodeSQL(ctx, "n")+`
	 WHERE k.user_id=$1 AND k.deleted_at IS NULL
	   AND strpos(lower(concat_ws(' ',k.id,k.key,k.value,k.category,k.review_status)),lower($3))>0
	   AND ($4::timestamptz IS NULL OR (k.updated_at,k.id COLLATE "C") < ($4,$5 COLLATE "C"))
	 ORDER BY k.updated_at DESC,k.id COLLATE "C" DESC LIMIT $6
	`, input.UserID, workspace, input.Query, stamp, id, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.KeyMemory{}
	for rows.Next() {
		item, err := scanKeyMemoryRow(ctx, rows, input.UserID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
