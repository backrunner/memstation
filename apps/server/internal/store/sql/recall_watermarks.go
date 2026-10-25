package sql

import (
	"context"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
)

func (r *DurableIngestionRepository) Watermarks(ctx context.Context) ([]domain.StreamWatermark, bool, error) {
	repo, err := r.current()
	if err != nil {
		return nil, false, err
	}
	return repo.Watermarks(ctx)
}

func (r *PostgresIngestionRepository) Watermarks(ctx context.Context) ([]domain.StreamWatermark, bool, error) {
	a, ok := security.AccessFromContext(ctx)
	if !ok {
		return []domain.StreamWatermark{}, false, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT (SELECT id FROM ingestion_receipts r WHERE r.stream_id=s.id ORDER BY last_sequence DESC,created_at DESC LIMIT 1) FROM memory_streams s WHERE s.user_id=$1 AND s.workspace_id=$2 AND `+storeaccess.PartitionSQL(ctx, "s.partition_id")+` AND EXISTS(SELECT 1 FROM ingestion_receipts r WHERE r.stream_id=s.id) ORDER BY s.id LIMIT 101`, a.UserID, a.WorkspaceID)
	if err != nil {
		return nil, false, err
	}
	ids, err := readIDs(rows)
	if err != nil {
		return nil, false, err
	}
	partial := len(ids) > 100
	out := []domain.StreamWatermark{}
	for _, id := range ids[:min(len(ids), 100)] {
		receipt, err := r.Progress(ctx, id)
		if err != nil {
			return nil, partial, err
		}
		out = append(out, domain.StreamWatermark{StreamID: receipt.StreamID, PartitionID: receipt.PartitionID, GenerationID: receipt.GenerationID, Accepted: receipt.AcceptedSequence, Indexed: receipt.IndexedSequence})
	}
	return out, partial, nil
}
