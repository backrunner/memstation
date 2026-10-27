package memory

import (
	"context"
	"fmt"
	"strings"

	"memstation/apps/server/internal/domain"
	storeaccess "memstation/apps/server/internal/store/access"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"
)

// Score before LIMIT: a newest-first prefetch can hide an old, exact match
// behind arbitrarily many recent weak matches. This is lexical term
// matching (not BM25), using the same weights as buildSummaryKeywordMatch.
func lexicalCandidateQuery(ctx context.Context, userID, generationID string, terms []string, limit int, allowedTypes []domain.MemoryType) (string, []any) {
	args := []any{userID}
	values := make([]string, 0, len(terms))
	for _, term := range terms {
		values = append(values, fmt.Sprintf("($%d::text, $%d::double precision)", len(args)+1, len(args)+2))
		args = append(args, term, lexicalKeywordWeight(term))
	}
	generationPosition := len(args) + 1
	args = recordutilstore.AppendGenerationArg(args, userID, generationID)
	metadataKeywords := func(alias string) string {
		return fmt.Sprintf("CASE WHEN jsonb_typeof(%s.metadata->'keywords') = 'array' THEN %s.metadata->'keywords' ELSE '[]'::jsonb END", alias, alias)
	}
	storedKeywords := func(alias string) string {
		return fmt.Sprintf("COALESCE((SELECT jsonb_agg(k.keyword ORDER BY k.weight DESC, k.keyword) FROM memory_keywords k WHERE k.user_id=%s.user_id AND k.summary_id=%s.id), '[]'::jsonb)", alias, alias)
	}
	type projection struct{ table, alias, node, content, keywords, kind, extra string }
	projections := []projection{
		{"memory_summaries", "s", "s.node_id", "s.summary", storedKeywords("s"), "'summary'", ""},
		{"facts", "f", "f.node_id", "f.content", storedKeywords("f") + " || CASE WHEN COALESCE(f.fact_key, '')='' THEN '[]'::jsonb ELSE jsonb_build_array(f.fact_key) END", "'fact'", ""},
		{"impressions", "i", "i.node_id", "i.description", metadataKeywords("i"), "'impression'", ""},
		{"memory_items", "mi", "mi.node_id", "mi.content", metadataKeywords("mi"), "mi.item_type", " AND mi.deleted_at IS NULL"},
		{"conversation_segments", "cs", "n.id", "cs.content", metadataKeywords("cs"), "'segment'", ""},
		{"key_memories", "k", "k.node_id", "CONCAT(k.key, ': ', k.value)", "'[]'::jsonb", "'key_memory'", " AND k.deleted_at IS NULL"},
	}
	parts := make([]string, 0, len(projections))
	for _, p := range projections {
		join := fmt.Sprintf("n.id=%s", p.node)
		generation := recordutilstore.GenerationFilterClause(p.alias+".generation_id", generationPosition, userID, generationID, false)
		if p.alias == "cs" {
			join = "n.entity_id=cs.id AND n.node_type='segment' AND n.generation_id=cs.generation_id"
		}
		if p.alias == "k" {
			generation = ""
		}
		parts = append(parts, fmt.Sprintf(`SELECT %s AS node_id, %s AS content, %s AS keywords, %s AS kind
 FROM %s %s JOIN memory_nodes n ON n.user_id=%s.user_id AND %s
 WHERE %s.user_id=$1 AND %s%s%s`, p.node, p.content, p.keywords, p.kind, p.table, p.alias, p.alias, join, p.alias, storeaccess.NodeSQL(ctx, "n"), generation, p.extra))
	}
	typeFilter := ""
	if len(allowedTypes) > 0 {
		positions := make([]string, 0, len(allowedTypes))
		for _, kind := range allowedTypes {
			positions = append(positions, fmt.Sprintf("$%d", len(args)+1))
			args = append(args, string(kind))
		}
		typeFilter = " WHERE d.kind IN (" + strings.Join(positions, ",") + ")"
	}
	args = append(args, limit)
	query := fmt.Sprintf(`WITH terms(term, weight) AS (VALUES %s), documents AS (%s), scored AS (
 SELECT d.node_id, d.content, d.keywords, relevance.total_weight,
 LEAST(0.95, 0.45 + relevance.total_weight*0.22 + (relevance.matched-1)*0.04) AS score
 FROM documents d CROSS JOIN LATERAL (
   SELECT SUM(hit.weight) AS total_weight, COUNT(*) AS matched FROM (
     SELECT COALESCE((SELECT GREATEST(0.1, 1.0-(kw.position-1)*0.1)
       FROM jsonb_array_elements_text(d.keywords) WITH ORDINALITY AS kw(value, position)
       WHERE LOWER(BTRIM(kw.value))=t.term ORDER BY kw.position LIMIT 1),
       CASE WHEN STRPOS(LOWER(d.content), t.term)>0 THEN t.weight END) AS weight
     FROM terms t
   ) hit WHERE hit.weight IS NOT NULL
 ) relevance%s
 ) SELECT node_id, content, keywords FROM scored WHERE total_weight > 0
 ORDER BY score DESC, total_weight DESC, node_id ASC LIMIT $%d`, strings.Join(values, ","), strings.Join(parts, " UNION ALL "), typeFilter, len(args))
	return query, args
}
