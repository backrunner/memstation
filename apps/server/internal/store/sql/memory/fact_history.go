package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
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

type factSnapshot struct {
	Document   domain.MemoryDocument
	RecordedAt time.Time
	RecordedTo *time.Time
}

func (r *InMemoryMemoryRepository) captureFactHistoryLocked(ctx context.Context, userID string) {
	now := time.Now().UTC()
	for _, doc := range r.documents {
		if doc.UserID != userID || !scopestore.PersonalMemoryWorkspace(ctx, userID, doc.WorkspaceID) || doc.Type != domain.MemoryTypeFact {
			continue
		}

		if r.factHistoryHeads == nil {
			r.factHistoryHeads = map[string]int{}
		}
		key := doc.UserID + "\x1f" + doc.NodeID
		doc.Metadata = recordutilstore.CloneMetadata(doc.Metadata)
		if metadataTimePtr(doc.Metadata, "valid_from") == nil {
			effective := doc.ActivityAt
			if i, ok := r.factHistoryHeads[key]; ok {
				effective = factEffectiveFrom(r.factHistory[i].Document)
			}
			if effective.IsZero() {
				effective = doc.CreatedAt
			}
			doc.Metadata["valid_from"] = effective.UTC().Format(time.RFC3339Nano)
		}
		if i, ok := r.factHistoryHeads[key]; ok {
			h := &r.factHistory[i]
			old, latest := h.Document, doc
			old.UpdatedAt = latest.UpdatedAt
			if reflect.DeepEqual(old, latest) {
				continue
			}
			h.RecordedTo = &now
		}
		doc.Metadata = recordutilstore.CloneMetadata(doc.Metadata)
		r.factHistoryHeads[key] = len(r.factHistory)
		r.factHistory = append(r.factHistory, factSnapshot{Document: doc, RecordedAt: now})
	}
}

func (r *InMemoryMemoryRepository) SearchFactsAt(ctx context.Context, input domain.SearchInput) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, input.UserID, input.WorkspaceID); err != nil {
		return nil, err
	}
	input.WorkspaceID = security.WorkspaceID(ctx, input.UserID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []domain.MemoryDocument{}
	for _, h := range r.factHistory {
		d := h.Document
		if d.UserID != input.UserID || !matchesDocumentGeneration(ctx, input.UserID, input.GenerationID, d) {
			continue
		}
		if _, exists := r.documents[recordutilstore.Key(d.UserID, d.ID)]; !exists {
			continue
		}
		if input.KnownAt == nil {
			if h.RecordedTo != nil {
				continue
			}
		} else if h.RecordedAt.After(*input.KnownAt) || (h.RecordedTo != nil && !input.KnownAt.Before(*h.RecordedTo)) {
			continue
		}
		d.Metadata = recordutilstore.CloneMetadata(d.Metadata)
		d.Metadata["recorded_at"] = h.RecordedAt.Format(time.RFC3339Nano)
		if domain.FactValidAt(d, input.AsOf) {
			out = append(out, d)
		}
	}
	return rankHistoricalCandidates(out, input), nil
}

func (r *PostgresMemoryRepository) SearchFactsAt(ctx context.Context, input domain.SearchInput) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, input.UserID, input.WorkspaceID); err != nil {
		return nil, err
	}
	input.WorkspaceID = security.WorkspaceID(ctx, input.UserID)
	// KnownAt is deliberately independent of AsOf. Old knowledge must never be
	// reconstructed from today's mutable supersession flags or content.
	query := `SELECT document,recorded_at FROM memory_fact_history WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=memory_fact_history.user_id AND scope_node.id=memory_fact_history.node_id AND ` + storeaccess.NodeSQL(ctx, "scope_node") + `) AND user_id=$1 AND generation_id=$2
 AND (($3::timestamptz IS NULL AND recorded_to IS NULL) OR (recorded_at<=$3 AND (recorded_to IS NULL OR recorded_to>$3)))
 AND ($4::timestamptz IS NULL OR (valid_from<=$4 AND (valid_to IS NULL OR valid_to>$4)))
 ORDER BY CASE WHEN $5='' THEN 0 WHEN position(lower($5) in lower(document->>'content'))>0 THEN 1 ELSE 0 END DESC,recorded_at DESC,id DESC LIMIT 512`
	rows, err := r.db.QueryContext(ctx, query, input.UserID, input.GenerationID, input.KnownAt, input.AsOf, strings.TrimSpace(input.Query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.MemoryDocument{}
	for rows.Next() {
		var b []byte
		var recorded time.Time
		var d domain.MemoryDocument
		if err = rows.Scan(&b, &recorded); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &d); err != nil {
			return nil, err
		}
		if d.Metadata == nil {
			d.Metadata = map[string]any{}
		}
		d.WorkspaceID = input.WorkspaceID
		d.Metadata["recorded_at"] = recorded.Format(time.RFC3339Nano)
		if domain.FactValidAt(d, input.AsOf) {
			out = append(out, d)
		}
	}
	return rankHistoricalCandidates(out, input), rows.Err()
}

func rankHistoricalCandidates(docs []domain.MemoryDocument, input domain.SearchInput) []domain.MemoryDocument {
	q := strings.ToLower(strings.TrimSpace(input.Query))
	for i := range docs {
		docs[i].Score = 0.2
		if q == "" {
			docs[i].Score = 0.5
		} else if strings.Contains(strings.ToLower(docs[i].Content), q) {
			docs[i].Score = 1
		} else {
			for _, term := range strings.Fields(q) {
				if strings.Contains(strings.ToLower(docs[i].Content), term) {
					docs[i].Score += 0.6 / float64(len(strings.Fields(q)))
				}
			}
		}
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Score != docs[j].Score {
			return docs[i].Score > docs[j].Score
		}
		if !docs[i].CreatedAt.Equal(docs[j].CreatedAt) {
			return docs[i].CreatedAt.After(docs[j].CreatedAt)
		}
		return docs[i].ID < docs[j].ID
	})
	if len(docs) > 512 {
		docs = docs[:512]
	}
	return docs
}

func (r *HybridMemoryRepository) SearchFactsAt(ctx context.Context, input domain.SearchInput) ([]domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).SearchFactsAt(ctx, input)
	}
	return r.fallback.SearchFactsAt(ctx, input)
}

func factTemporalNeighborsTx(ctx context.Context, tx *sql.Tx, doc domain.MemoryDocument) (string, string, *time.Time, error) {
	rows, err := tx.QueryContext(ctx, `SELECT f.id,COALESCE(f.valid_from,n.activity_at,f.created_at) FROM facts f JOIN memory_nodes n ON n.user_id=f.user_id AND n.id=f.node_id WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=f.user_id AND scope_node.id=f.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND f.user_id=$1 AND f.generation_id=$2 AND f.lineage_group_id=$3 AND f.id<>$4 ORDER BY COALESCE(f.valid_from,n.activity_at,f.created_at),f.revision_seq`, doc.UserID, doc.GenerationID, factLineageGroupID(doc.UserID, doc), doc.ID)
	if err != nil {
		return "", "", nil, err
	}
	defer rows.Close()
	previous, next := "", ""
	var nextTime *time.Time
	effective := factEffectiveFrom(doc)
	for rows.Next() {
		var id string
		var at time.Time
		if err = rows.Scan(&id, &at); err != nil {
			return "", "", nil, err
		}
		if !at.After(effective) {
			previous = id
		} else if next == "" {
			next = id
			nextTime = &at
		}
	}
	return previous, next, nextTime, rows.Err()
}

func (r *InMemoryMemoryRepository) factTemporalNeighborsLocked(ctx context.Context, doc domain.MemoryDocument) (string, string, *time.Time) {
	candidates := []domain.MemoryDocument{}
	for _, d := range r.documents {
		if d.ID != doc.ID && d.UserID == doc.UserID && scopestore.PersonalMemoryWorkspace(ctx, doc.UserID, d.WorkspaceID) && d.GenerationID == doc.GenerationID && d.Type == domain.MemoryTypeFact && factLineageGroupIDFromMetadata(d.Metadata) == factLineageGroupIDFromMetadata(doc.Metadata) {
			candidates = append(candidates, d)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := factEffectiveFrom(candidates[i]), factEffectiveFrom(candidates[j])
		if a.Equal(b) {
			return factLineageRevisionSeqFromMetadata(candidates[i].Metadata) < factLineageRevisionSeqFromMetadata(candidates[j].Metadata)
		}
		return a.Before(b)
	})
	previous, next := "", ""
	var nextTime *time.Time
	for _, d := range candidates {
		at := factEffectiveFrom(d)
		if !at.After(factEffectiveFrom(doc)) {
			previous = d.ID
		} else if next == "" {
			next = d.ID
			nextTime = &at
		}
	}
	return previous, next, nextTime
}

func (r *InMemoryMemoryRepository) purgeGraphAuxLocked(userID, nodeID string) {
	history := r.factHistory[:0]
	for _, h := range r.factHistory {
		if h.Document.UserID != userID || h.Document.NodeID != nodeID {
			history = append(history, h)
		}
	}
	r.factHistory = history
	r.factHistoryHeads = map[string]int{}
	for i, h := range history {
		if h.RecordedTo == nil {
			r.factHistoryHeads[h.Document.UserID+"\x1f"+h.Document.NodeID] = i
		}
	}
	aliases := r.entityAliases[:0]
	for _, a := range r.entityAliases {
		if a.UserID != userID || (a.SourceNodeID != nodeID && a.TargetNodeID != nodeID) {
			aliases = append(aliases, a)
		}
	}
	r.entityAliases = aliases
}
