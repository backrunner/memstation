package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"
)

func entityLookupNames(names []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		name = domain.EntityNameKey(name)
		if name == "" || len(name) > 256 || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) == 8 {
			break
		}
	}
	return out
}

func (r *PostgresMemoryRepository) MatchEntityNames(ctx context.Context, user, generation, scope string, names []string) ([]domain.EntityNameMatch, error) {
	if _, err := security.DataScope(ctx, user, ""); err != nil {
		return nil, err
	}
	names = entityLookupNames(names)
	if len(names) == 0 {
		return []domain.EntityNameMatch{}, nil
	}
	args := []any{user, generation, domain.NormalizeEntityAlias(scope)}
	positions := []string{}
	for _, name := range names {
		args = append(args, name)
		positions = append(positions, fmt.Sprintf("$%d", len(args)))
	}
	// Match only names with an explicit canonical field. This is a lexical index,
	// never a semantic alias merge. Count only currently authorized occurrences.
	rows, err := r.db.QueryContext(ctx, `WITH matched AS (
 SELECT n.id,i.updated_at,LOWER(BTRIM(i.metadata->>'canonical_name')) AS name,
 COUNT(*) OVER(PARTITION BY LOWER(BTRIM(i.metadata->>'canonical_name'))) AS occurrences,
 ROW_NUMBER() OVER(PARTITION BY LOWER(BTRIM(i.metadata->>'canonical_name')) ORDER BY i.updated_at DESC,n.id) AS recent_position,
 ROW_NUMBER() OVER(PARTITION BY LOWER(BTRIM(i.metadata->>'canonical_name')),n.source_conversation_id ORDER BY i.updated_at DESC,n.id) AS source_position
 FROM memory_items i JOIN memory_nodes n ON n.id=i.node_id AND n.user_id=i.user_id
 WHERE i.user_id=$1 AND i.generation_id=$2 AND n.generation_id=$2
 AND i.item_type='entity' AND i.deleted_at IS NULL AND `+storeaccess.NodeSQL(ctx, "n")+`
 AND ($3='' OR LOWER(BTRIM(i.metadata->>'entity_scope'))=$3 OR i.metadata->>'interpretation'='source_mention')
 AND LOWER(BTRIM(i.metadata->>'canonical_name')) IN (`+strings.Join(positions, ",")+`)
 ), covered AS (
 SELECT id,name,occurrences,ROW_NUMBER() OVER(PARTITION BY name
 ORDER BY CASE WHEN recent_position<=16 THEN 0 ELSE source_position END,updated_at DESC,id) AS position
 FROM matched
 ) SELECT id,name,occurrences FROM covered WHERE position<=48 ORDER BY occurrences,name,position LIMIT 128`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.EntityNameMatch{}
	for rows.Next() {
		var item domain.EntityNameMatch
		if err = rows.Scan(&item.NodeID, &item.Name, &item.Occurrences); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *InMemoryMemoryRepository) MatchEntityNames(ctx context.Context, user, generation, scope string, names []string) ([]domain.EntityNameMatch, error) {
	if _, err := security.DataScope(ctx, user, ""); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names = entityLookupNames(names)
	scope = domain.NormalizeEntityAlias(scope)
	r.mu.RLock()
	defer r.mu.RUnlock()
	groups := map[string][]domain.MemoryDocument{}
	for _, doc := range r.documents {
		if doc.UserID != user || doc.GenerationID != generation || doc.Type != domain.MemoryTypeEntity || !scopestore.DocumentVisible(ctx, doc) {
			continue
		}
		entityScope, _ := doc.Metadata["entity_scope"].(string)
		// A source mention records text, so it has no scope and cannot be scoped out.
		if scope != "" && domain.NormalizeEntityAlias(entityScope) != scope && doc.Metadata["interpretation"] != "source_mention" {
			continue
		}
		name, _ := doc.Metadata["canonical_name"].(string)
		name = domain.EntityNameKey(name)
		if slices.Contains(names, name) {
			groups[name] = append(groups[name], doc)
		}
	}
	out := []domain.EntityNameMatch{}
	selectedPositions := map[string]int{}
	for name, docs := range groups {
		sort.Slice(docs, func(i, j int) bool {
			if !docs[i].UpdatedAt.Equal(docs[j].UpdatedAt) {
				return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
			}
			return docs[i].NodeID < docs[j].NodeID
		})
		// Retain recent occurrences, then give other source conversations a
		// chance before spending the remaining budget on repeated mentions.
		positions := map[string]int{}
		priority := map[string]int{}
		for i, doc := range docs {
			positions[doc.ConversationID]++
			priority[doc.NodeID] = positions[doc.ConversationID]
			if i < 16 {
				priority[doc.NodeID] = 0
			}
		}
		sort.SliceStable(docs, func(i, j int) bool { return priority[docs[i].NodeID] < priority[docs[j].NodeID] })
		for position, doc := range docs[:min(len(docs), 48)] {
			selectedPositions[doc.NodeID] = position
			out = append(out, domain.EntityNameMatch{NodeID: doc.NodeID, Name: name, Occurrences: len(docs)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Occurrences != out[j].Occurrences {
			return out[i].Occurrences < out[j].Occurrences
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return selectedPositions[out[i].NodeID] < selectedPositions[out[j].NodeID]
	})
	return out[:min(len(out), 128)], nil
}

func (r *HybridMemoryRepository) MatchEntityNames(ctx context.Context, user, generation, scope string, names []string) ([]domain.EntityNameMatch, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).MatchEntityNames(ctx, user, generation, scope, names)
	}
	return r.fallback.MatchEntityNames(ctx, user, generation, scope, names)
}
