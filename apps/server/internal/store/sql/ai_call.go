package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
)

type AICallRepository struct{ db runtimeconfig.DBHandle }

func NewAICallRepository(db runtimeconfig.DBHandle) *AICallRepository {
	return &AICallRepository{db: db}
}

func (r *AICallRepository) StartAICall(ctx context.Context, c domain.AICall) error {
	workspace, err := security.DataScope(ctx, c.UserID, c.WorkspaceID)
	if err != nil {
		return err
	}
	for _, part := range c.PartitionIDs {
		if !security.CanReadPartition(ctx, part) {
			return domain.ErrAccessDenied
		}
	}
	if !security.CanReadPartition(ctx, c.PartitionID) {
		return domain.ErrAccessDenied
	}
	db := currentDB(r.db)
	if db == nil {
		return errors.New("AI call ledger unavailable")
	}
	_, err = db.ExecContext(ctx, `INSERT INTO memory_ai_calls(id,user_id,workspace_id,partition_id,role,model_alias,trace_id,job_id,attempt,usage_authority,partition_ids) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'broker',$10)`, c.ID, c.UserID, workspace, c.PartitionID, c.Role, c.ModelAlias, c.TraceID, c.JobID, c.Attempt, marshalJSON(security.Partitions(ctx)))
	return err
}

func (r *AICallRepository) FinishAICall(ctx context.Context, c domain.AICall) error {
	workspace, err := security.DataScope(ctx, c.UserID, c.WorkspaceID)
	if err != nil {
		return err
	}
	for _, partition := range append(append([]string{}, c.PartitionIDs...), c.PartitionID) {
		if !security.CanReadPartition(ctx, partition) {
			return domain.ErrAccessDenied
		}
	}
	db := currentDB(r.db)
	if db == nil {
		return errors.New("AI call ledger unavailable")
	}
	result, err := db.ExecContext(ctx, `UPDATE memory_ai_calls SET model_version=$5,provider_request_id=$6,broker_usage_id=$7,status=$8,input_tokens=$9,output_tokens=$10,reported_cost=$11,currency=$12,failure_code=$13,updated_at=now() WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND partition_id=$4 AND status IN('started','unknown')`, c.ID, c.UserID, workspace, c.PartitionID, c.ModelVersion, c.ProviderRequestID, c.BrokerUsageID, c.Status, c.InputTokens, c.OutputTokens, c.ReportedCost, c.Currency, c.FailureCode)
	if err != nil {
		return err
	}
	if n, e := result.RowsAffected(); e != nil {
		return e
	} else if n != 1 {
		var same bool
		err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_ai_calls WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND partition_id=$4 AND status=$5 AND broker_usage_id=$6 AND provider_request_id=$7 AND model_version=$8 AND input_tokens=$9 AND output_tokens=$10 AND reported_cost=$11 AND currency=$12 AND failure_code=$13)`, c.ID, c.UserID, workspace, c.PartitionID, c.Status, c.BrokerUsageID, c.ProviderRequestID, c.ModelVersion, c.InputTokens, c.OutputTokens, c.ReportedCost, c.Currency, c.FailureCode).Scan(&same)
		if err != nil {
			return err
		}
		if !same {
			return domain.ErrAccessConflict
		}
	}
	return nil
}

func (r *AICallRepository) AICalls(ctx context.Context, after string, limit int) ([]domain.AICall, error) {
	a, err := lifecycleAccess(ctx)
	if err != nil {
		return nil, err
	}
	db := currentDB(r.db)
	if db == nil {
		return nil, errors.New("AI call ledger unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT id,workspace_id,partition_id,role,model_alias,model_version,trace_id,job_id,attempt,provider_request_id,broker_usage_id,CASE WHEN status='started' THEN 'unknown' ELSE status END,input_tokens,output_tokens,reported_cost,currency,usage_authority,failure_code,created_at,updated_at,partition_ids FROM memory_ai_calls WHERE user_id=$1 AND workspace_id=$2 AND `+`NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(partition_ids) p(id) WHERE NOT (`+storeaccess.PartitionSQL(ctx, "p.id")+`))`+` AND ($3='' OR id>$3) ORDER BY id LIMIT $4`, a.UserID, a.WorkspaceID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AICall{}
	for rows.Next() {
		var c domain.AICall
		var partitions []byte
		if err = rows.Scan(&c.ID, &c.WorkspaceID, &c.PartitionID, &c.Role, &c.ModelAlias, &c.ModelVersion, &c.TraceID, &c.JobID, &c.Attempt, &c.ProviderRequestID, &c.BrokerUsageID, &c.Status, &c.InputTokens, &c.OutputTokens, &c.ReportedCost, &c.Currency, &c.UsageAuthority, &c.FailureCode, &c.CreatedAt, &c.UpdatedAt, &partitions); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(partitions, &c.PartitionIDs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *AICallRepository) EmbeddingIdentity(ctx context.Context, user, generation string, version int, configured domain.EmbeddingIdentity) (domain.EmbeddingIdentity, error) {
	var out domain.EmbeddingIdentity
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil {
		return out, err
	}
	db := currentDB(r.db)
	if db == nil {
		return out, errors.New("embedding identity store unavailable")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	if generation != "" {
		var known string
		if err = tx.QueryRowContext(ctx, `SELECT generation_id FROM memory_generations WHERE user_id=$1 AND workspace_id=$2 AND generation_id=$3 AND status<>'discarded' FOR SHARE`, user, workspace, generation).Scan(&known); err != nil {
			return out, domain.ErrAccessNotFound
		}
	}
	if version <= 0 {
		version = 1
	}
	id := ingestionID("embedding-profile", user, workspace, generation, fmt.Sprint(version))
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT model_version,dimensions,fingerprint FROM embedding_index_profiles WHERE id=$1`, id).Scan(&out.ModelVersion, &out.Dimensions, &out.Fingerprint)
	if err == nil {
		return out, tx.Commit()
	}
	if !errors.Is(err, sqlpkg.ErrNoRows) {
		return out, err
	}
	var populated bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_embeddings WHERE user_id=$1 AND workspace_id=$2 AND generation_id=$3 AND embedding_version=$4)`, user, workspace, generation, version).Scan(&populated); err != nil {
		return out, err
	}
	if populated {
		return out, errors.New("existing index has no broker fingerprint; create a new generation")
	}
	if configured.ModelVersion == "" || configured.Dimensions < 1 || configured.Fingerprint == "" {
		return out, errors.New("embedding identity required")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO embedding_index_profiles(id,user_id,workspace_id,generation_id,embedding_version,model_version,dimensions,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, user, workspace, generation, version, configured.ModelVersion, configured.Dimensions, configured.Fingerprint)
	if err != nil {
		return out, err
	}
	return configured, tx.Commit()
}

func (r *AICallRepository) AICall(ctx context.Context, id string) (domain.AICall, error) {
	a, err := lifecycleAccess(ctx)
	if err != nil {
		return domain.AICall{}, err
	}
	db := currentDB(r.db)
	if db == nil {
		return domain.AICall{}, errors.New("AI call ledger unavailable")
	}
	var c domain.AICall
	var partitions []byte
	err = db.QueryRowContext(ctx, `SELECT id,user_id,workspace_id,partition_id,partition_ids,role,model_alias,model_version,trace_id,job_id,attempt,provider_request_id,broker_usage_id,CASE WHEN status='started' THEN 'unknown' ELSE status END,input_tokens,output_tokens,reported_cost,currency,usage_authority,failure_code,created_at,updated_at FROM memory_ai_calls WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(partition_ids) p(id) WHERE NOT (`+storeaccess.PartitionSQL(ctx, "p.id")+`))`, id, a.UserID, a.WorkspaceID).Scan(&c.ID, &c.UserID, &c.WorkspaceID, &c.PartitionID, &partitions, &c.Role, &c.ModelAlias, &c.ModelVersion, &c.TraceID, &c.JobID, &c.Attempt, &c.ProviderRequestID, &c.BrokerUsageID, &c.Status, &c.InputTokens, &c.OutputTokens, &c.ReportedCost, &c.Currency, &c.UsageAuthority, &c.FailureCode, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return c, domain.ErrAccessNotFound
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(partitions, &c.PartitionIDs)
}
