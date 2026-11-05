package aicall

import (
	"context"
	"database/sql"
	"errors"

	"memstation/apps/server/internal/domain"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"
)

func (r *AICallRepository) AIUsageReport(ctx context.Context, q domain.AIUsageQuery) (domain.AIUsageReport, error) {
	out := domain.AIUsageReport{Query: q, Groups: []domain.AIUsageGroup{}, Costs: []domain.CurrencyCost{}}
	a, err := scopestore.LifecycleAccess(ctx)
	if err != nil {
		return out, err
	}
	db := dbutilstore.CurrentDB(r.db)
	if db == nil {
		return out, errors.New("AI usage store unavailable")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer dbutilstore.Rollback(tx)
	where := ` FROM memory_ai_calls WHERE user_id=$1 AND workspace_id=$2 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(partition_ids) p(id) WHERE NOT (` + storeaccess.PartitionSQL(ctx, "p.id") + `)) AND created_at >= $3 AND created_at < $4 AND ($5='' OR stage=$5) AND ($6='' OR strategy=$6) AND ($7='' OR (CASE WHEN status='started' THEN 'unknown' ELSE status END)=$7) AND ($8='' OR model_alias=$8)`
	args := []any{a.UserID, a.WorkspaceID, q.Since, q.Until, q.Stage, q.Strategy, q.Status, q.Model}
	rows, err := tx.QueryContext(ctx, `SELECT to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD'),stage,strategy,model_alias,usage_authority,CASE WHEN status='started' THEN 'unknown' ELSE status END,count(*),count(*) FILTER(WHERE usage_known),COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(duration_ms),0),count(*) FILTER(WHERE attempt>0)`+where+` GROUP BY 1,2,3,4,5,6 ORDER BY 1 DESC,2,3,4,5,6`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var g domain.AIUsageGroup
		if err = rows.Scan(&g.Day, &g.Stage, &g.Strategy, &g.Model, &g.Authority, &g.Status, &g.Calls, &g.KnownUsageCalls, &g.InputTokens, &g.OutputTokens, &g.DurationMS, &g.Retries); err != nil {
			break
		}
		out.Groups = append(out.Groups, g)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	// Costs retain their currencies and PostgreSQL's exact decimal arithmetic.
	rows, err = tx.QueryContext(ctx, `SELECT currency,COALESCE(sum(reported_cost::numeric),0)::text,count(*)`+where+` AND reported_cost ~ '^[+]?(?:[0-9]+(?:[.][0-9]*)?|[.][0-9]+)(?:[eE][+-]?[0-9]+)?$' AND currency<>'' GROUP BY currency ORDER BY currency`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c domain.CurrencyCost
		if err = rows.Scan(&c.Currency, &c.Amount, &c.Calls); err != nil {
			break
		}
		out.Costs = append(out.Costs, c)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT count(*)`+where+` AND NOT(reported_cost ~ '^[+]?(?:[0-9]+(?:[.][0-9]*)?|[.][0-9]+)(?:[eE][+-]?[0-9]+)?$' AND currency<>'')`, args...).Scan(&out.UnpricedCalls)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
