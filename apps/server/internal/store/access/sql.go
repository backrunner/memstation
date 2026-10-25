// Package access contains SQL predicates for an already authenticated data scope.
// All dynamic values are quoted here, never concatenated by callers. Identifiers
// are source-code constants; none of these helpers accepts a client SQL fragment.
package access

import (
	"context"
	"strings"
	"time"

	"memstation/apps/server/internal/security"
)

func Literal(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func WorkspaceSQL(ctx context.Context, ownerColumn string) string {
	if workspace := security.BoundWorkspace(ctx); workspace != "" {
		return Literal(workspace)
	}
	return "'personal-' || encode(sha256(convert_to(" + ownerColumn + ",'UTF8')),'hex')"
}

func PartitionSQL(ctx context.Context, column string) string {
	parts := security.Partitions(ctx)
	if len(parts) == 0 {
		return "FALSE"
	}
	values := make([]string, len(parts))
	for i, part := range parts {
		values[i] = Literal(part)
	}
	return column + " IN (" + strings.Join(values, ",") + ")"
}

func NodeSQL(ctx context.Context, alias string) string {
	return QuerySQL(ctx, alias) + alias + ".workspace_id = " + WorkspaceSQL(ctx, alias+".user_id") +
		" AND " + PartitionSQL(ctx, alias+".partition_id") + " AND " + alias + ".access_status='active' AND NOT EXISTS (SELECT 1 FROM memory_publications publication JOIN memory_nodes origin ON origin.id=publication.source_node_id WHERE publication.target_node_id=" + alias + ".id AND (publication.status<>'active' OR origin.access_status<>'active' OR origin.revision<>publication.source_revision))"
}

func QuerySQL(ctx context.Context, alias string) string {
	f := security.MemoryFilterFromContext(ctx)
	out := ""
	if len(f.SubjectActorIDs) > 0 {
		actors := make([]string, len(f.SubjectActorIDs))
		for i, v := range f.SubjectActorIDs {
			actors[i] = Literal(v)
		}
		out += alias + ".subject_actor_ids ?| ARRAY[" + strings.Join(actors, ",") + "]::text[] AND "
	}
	if f.From != nil {
		out += alias + ".activity_at >= " + Literal(f.From.UTC().Format(time.RFC3339Nano)) + "::timestamptz AND "
	}
	if f.To != nil {
		out += alias + ".activity_at <= " + Literal(f.To.UTC().Format(time.RFC3339Nano)) + "::timestamptz AND "
	}
	return out
}
