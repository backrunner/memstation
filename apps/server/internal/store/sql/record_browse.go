package sql

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
)

func recordBefore(stamp time.Time, id string, position *domain.RecordPosition) bool {
	return position == nil || stamp.Before(position.UpdatedAt) || (stamp.Equal(position.UpdatedAt) && id < position.ID)
}

func (r *HybridConversationRepository) BrowseConversations(ctx context.Context, input domain.RecordBrowseQuery, position *domain.RecordPosition) ([]domain.ConversationListItem, error) {
	if db := currentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).BrowseConversations(ctx, input, position)
	}
	return r.fallback.BrowseConversations(ctx, input, position)
}

func (r *InMemoryConversationRepository) BrowseConversations(ctx context.Context, input domain.RecordBrowseQuery, position *domain.RecordPosition) ([]domain.ConversationListItem, error) {
	workspace, err := security.DataScope(ctx, input.UserID, "")
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []domain.ConversationListItem{}
	query := strings.ToLower(input.Query)
	for _, item := range r.conversations {
		if item.UserID != input.UserID || item.WorkspaceID != workspace || !recordBefore(item.UpdatedAt, item.ConversationID, position) {
			continue
		}
		content := strings.Join([]string{item.ConversationID, string(item.Provider), item.RequestFormat, string(item.Mode), string(item.FinalizeStatus)}, " ")
		if !strings.Contains(strings.ToLower(content), query) {
			continue
		}
		items = append(items, domain.ConversationListItem{ConversationID: item.ConversationID, UserID: item.UserID, Provider: string(item.Provider), Protocol: item.RequestFormat, Mode: string(item.Mode), FinalizeStatus: string(item.FinalizeStatus), SummaryStatus: string(item.SummaryStatus), AnalysisStatus: string(item.AnalysisStatus), MessageCount: len(item.Messages), TotalTokens: item.Usage.TotalTokens, UpdatedAt: item.UpdatedAt, Metadata: cloneMetadata(item.Metadata)})
	}
	sort.Slice(items, func(i, j int) bool {
		return recordBefore(items[j].UpdatedAt, items[j].ConversationID, &domain.RecordPosition{UpdatedAt: items[i].UpdatedAt, ID: items[i].ConversationID})
	})
	if input.Limit > 0 && len(items) > input.Limit {
		items = items[:input.Limit]
	}
	return items, nil
}

func (r *PostgresConversationRepository) BrowseConversations(ctx context.Context, input domain.RecordBrowseQuery, position *domain.RecordPosition) ([]domain.ConversationListItem, error) {
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
	// Limit the parent set first. Only selected conversations are aggregated;
	// neither message content nor a per-record detail request is needed.
	rows, err := r.db.QueryContext(ctx, `
	 WITH page AS MATERIALIZED (
	   SELECT id, user_id, workspace_id, upstream_provider, request_format, mode,
	          finalize_status, summary_status, analysis_status, updated_at, metadata
	   FROM conversations
	   WHERE user_id=$1 AND workspace_id=$2 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND finalize_status<>'invalidated'
	     AND strpos(lower(concat_ws(' ',id,upstream_provider,request_format,mode,finalize_status)),lower($3))>0
	     AND ($4::timestamptz IS NULL OR (updated_at,id COLLATE "C") < ($4,$5 COLLATE "C"))
	   ORDER BY updated_at DESC,id COLLATE "C" DESC LIMIT $6
	 )
	 SELECT c.id,c.user_id,c.upstream_provider,c.request_format,c.mode,c.finalize_status,c.summary_status,c.analysis_status,c.updated_at,c.metadata,
	   (SELECT count(*) FROM messages m WHERE m.user_id=c.user_id AND m.workspace_id=c.workspace_id AND m.conversation_id=c.id),
	   (SELECT coalesce(sum(u.total_tokens),0) FROM token_usages u WHERE u.user_id=c.user_id AND u.workspace_id=c.workspace_id AND u.conversation_id=c.id)
	 FROM page c ORDER BY c.updated_at DESC,c.id COLLATE "C" DESC
	`, input.UserID, workspace, input.Query, stamp, id, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ConversationListItem{}
	for rows.Next() {
		var item domain.ConversationListItem
		var metadata []byte
		if err := rows.Scan(&item.ConversationID, &item.UserID, &item.Provider, &item.Protocol, &item.Mode, &item.FinalizeStatus, &item.SummaryStatus, &item.AnalysisStatus, &item.UpdatedAt, &metadata, &item.MessageCount, &item.TotalTokens); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil && len(metadata) > 0 {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *HybridMemoryRepository) BrowseKeyMemories(ctx context.Context, input domain.RecordBrowseQuery, position *domain.RecordPosition) ([]domain.KeyMemory, error) {
	if db := currentDB(r.db); db != nil {
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
		if item.UserID != input.UserID || !personalMemoryWorkspace(ctx, input.UserID, item.WorkspaceID) || !recordBefore(item.UpdatedAt, item.ID, position) {
			continue
		}
		if !strings.Contains(strings.ToLower(strings.Join([]string{item.ID, item.Key, item.Value, item.Category, item.ReviewStatus}, " ")), query) {
			continue
		}
		item.Metadata = cloneMetadata(item.Metadata)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return recordBefore(items[j].UpdatedAt, items[j].ID, &domain.RecordPosition{UpdatedAt: items[i].UpdatedAt, ID: items[i].ID})
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
