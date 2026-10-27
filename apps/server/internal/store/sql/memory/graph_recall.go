package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
)

// walkRelations shares deterministic breadth/width limits across SQL and local stores.
func walkRelations(ctx context.Context, q domain.RelationQuery, fetch func(context.Context, []string) ([]domain.MemoryRelation, error)) ([]domain.MemoryRelation, error) {
	q = q.Normalized()
	ctx, cancel := context.WithTimeout(ctx, q.Timeout)
	defer cancel()
	frontier := dbutilstore.UniqueNonEmptyStrings(q.NodeIDs)
	if len(frontier) > q.MaxNodes {
		frontier = frontier[:q.MaxNodes]
	}
	seenNodes := map[string]bool{}
	seenEdges := map[string]bool{}
	for _, id := range frontier {
		seenNodes[id] = true
	}
	out := []domain.MemoryRelation{}
	for hop := 0; hop < q.MaxHops && len(frontier) > 0; hop++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		edges, err := fetch(ctx, frontier)
		if err != nil {
			return nil, err
		}
		sort.Slice(edges, func(i, j int) bool {
			a, b := edges[i], edges[j]
			av, bv := a.Weight*domain.RelationConfidence(a), b.Weight*domain.RelationConfidence(b)
			if av != bv {
				return av > bv
			}
			return relationIdentity(a) < relationIdentity(b)
		})
		counts := map[string]int{}
		next := map[string]bool{}
		for _, edge := range edges {
			if seenEdges[relationIdentity(edge)] {
				continue
			}
			for _, node := range frontier {
				dst := domain.RelationDestination(edge, node, q.Mode)
				if dst == "" || counts[node] >= q.MaxEdgesPerNode {
					continue
				}
				if !seenNodes[dst] && len(seenNodes) >= q.MaxNodes {
					continue
				}
				seenEdges[relationIdentity(edge)] = true
				counts[node]++
				out = append(out, edge)
				if !seenNodes[dst] {
					seenNodes[dst] = true
					next[dst] = true
				}
				break
			}
			if len(out) >= q.MaxEdges {
				return out, nil
			}
		}
		frontier = frontier[:0]
		for id := range next {
			frontier = append(frontier, id)
		}
		sort.Strings(frontier)
	}
	return out, nil
}

func relationIdentity(r domain.MemoryRelation) string {
	if r.ID != "" {
		return r.ID
	}
	return r.SrcNodeID + "\x1f" + r.DstNodeID + "\x1f" + r.RelationType
}

func (r *PostgresMemoryRepository) RecallRelations(ctx context.Context, q domain.RelationQuery) ([]domain.MemoryRelation, error) {
	if _, err := security.DataScope(ctx, q.UserID, q.WorkspaceID); err != nil {
		return nil, err
	}
	q.WorkspaceID = security.WorkspaceID(ctx, q.UserID)
	q = q.Normalized()
	if err := domain.ValidateRelationMode(q.Mode); err != nil {
		return nil, err
	}
	return walkRelations(ctx, q, func(ctx context.Context, frontier []string) ([]domain.MemoryRelation, error) {
		// Each LATERAL branch uses a directional adjacency index and applies LIMIT
		// before returning rows. The global cap is also enforced by walkRelations.
		args := []any{q.UserID, q.GenerationID, q.MinWeight, q.MaxEdgesPerNode}
		nodes := make([]string, 0, len(frontier))
		for _, id := range frontier {
			args = append(args, id)
			nodes = append(nodes, fmt.Sprintf("($%d::text)", len(args)))
		}
		types := []string{}
		for _, kind := range domain.RelationTypesForMode(q.Mode) {
			args = append(args, kind)
			types = append(types, fmt.Sprintf("$%d", len(args)))
		}

		// Directional LIMITs make both adjacency indexes usable even for hubs.
		branches := []string{}
		for _, direction := range []string{"src_node_id", "dst_node_id"} {
			if q.Mode == "dependencies" && direction == "dst_node_id" {
				continue
			}
			if q.Mode == "dependents" && direction == "src_node_id" {
				continue
			}
			extra := ""
			if q.Mode == "neighborhood" && direction == "dst_node_id" {
				extra = " AND e.relation_type NOT IN ('depends_on','resulted_in')"
			}
			branches = append(branches, fmt.Sprintf(`(SELECT e.* FROM memory_relations e
          WHERE e.user_id=$1 AND e.generation_id=$2 AND e.%s=seed.id AND e.weight >= $3 AND e.relation_type IN (%s)%s
          AND EXISTS (SELECT 1 FROM memory_nodes n WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND n.user_id =e.user_id AND n.id=e.src_node_id AND (n.generation_id=$2 OR (n.generation_id='' AND n.node_type='key_memory')))
          AND EXISTS (SELECT 1 FROM memory_nodes n WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND n.user_id =e.user_id AND n.id=e.dst_node_id AND (n.generation_id=$2 OR (n.generation_id='' AND n.node_type='key_memory')))
          ORDER BY e.weight DESC,e.id LIMIT $4)`, direction, strings.Join(types, ","), extra))
		}
		statement := fmt.Sprintf(`SELECT rel.id,rel.generation_id,rel.user_id,rel.src_node_id,rel.dst_node_id,rel.relation_type,rel.weight,rel.source,rel.metadata,rel.created_at
        FROM (VALUES %s) AS seed(id) CROSS JOIN LATERAL (
        SELECT * FROM (%s) adjacent ORDER BY weight DESC,id LIMIT $4) rel`, strings.Join(nodes, ","), strings.Join(branches, " UNION ALL "))

		if len(frontier) == 0 {
			return nil, nil
		}
		rows, err := r.db.QueryContext(ctx, statement, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []domain.MemoryRelation{}
		for rows.Next() {
			var e domain.MemoryRelation
			var b []byte
			if err = rows.Scan(&e.ID, &e.GenerationID, &e.UserID, &e.SrcNodeID, &e.DstNodeID, &e.RelationType, &e.Weight, &e.Source, &b, &e.CreatedAt); err != nil {
				return nil, err
			}
			if err = json.Unmarshal(b, &e.Metadata); err != nil {
				return nil, err
			}
			e.WorkspaceID = q.WorkspaceID
			out = append(out, e)
		}
		return out, rows.Err()
	})
}

func (r *InMemoryMemoryRepository) RecallRelations(ctx context.Context, q domain.RelationQuery) ([]domain.MemoryRelation, error) {
	if _, err := security.DataScope(ctx, q.UserID, q.WorkspaceID); err != nil {
		return nil, err
	}
	q.WorkspaceID = security.WorkspaceID(ctx, q.UserID)
	q = q.Normalized()
	if err := domain.ValidateRelationMode(q.Mode); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	nodes := map[string]domain.MemoryDocument{}
	for _, d := range r.documents {
		if d.UserID == q.UserID {
			nodes[d.NodeID] = d
		}
	}
	types := map[string]bool{}
	for _, v := range domain.RelationTypesForMode(q.Mode) {
		types[v] = true
	}
	return walkRelations(ctx, q, func(ctx context.Context, frontier []string) ([]domain.MemoryRelation, error) {
		out := []domain.MemoryRelation{}
		for _, e := range r.relations {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if e.UserID != q.UserID || e.GenerationID != q.GenerationID || e.Weight < q.MinWeight || !types[e.RelationType] {
				continue
			}
			src, srcOK := nodes[e.SrcNodeID]
			dst, dstOK := nodes[e.DstNodeID]
			if !srcOK || !dstOK || !matchesDocumentGeneration(ctx, q.UserID, q.GenerationID, src) || !matchesDocumentGeneration(ctx, q.UserID, q.GenerationID, dst) {
				continue
			}
			for _, id := range frontier {
				if domain.RelationDestination(e, id, q.Mode) != "" {
					e.WorkspaceID = q.WorkspaceID
					out = append(out, e)
					break
				}
			}
		}
		return out, nil
	})
}

func (r *HybridMemoryRepository) RecallRelations(ctx context.Context, q domain.RelationQuery) ([]domain.MemoryRelation, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).RecallRelations(ctx, q)
	}
	return r.fallback.RecallRelations(ctx, q)
}
