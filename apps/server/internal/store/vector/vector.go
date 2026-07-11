package vector

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
)

const embeddingDimensions = 3

type PGVectorStore struct {
	db runtimeconfig.DBHandle

	embedder ports.EmbeddingClient
	mu       sync.RWMutex
	backend  string
	items    map[string]domain.VectorUpsertRequest
}

func NewPGVectorStore(db runtimeconfig.DBHandle) *PGVectorStore {
	return &PGVectorStore{
		db:      db,
		backend: "pgvector",
		items:   map[string]domain.VectorUpsertRequest{},
	}
}

func (s *PGVectorStore) SetEmbedder(embedder ports.EmbeddingClient) {
	s.embedder = embedder
}

func (s *PGVectorStore) SetBackend(backend string) error {
	normalized := strings.ToLower(strings.TrimSpace(backend))
	if normalized == "" {
		normalized = "pgvector"
	}
	if normalized != "pgvector" {
		return fmt.Errorf("unsupported vector store backend: %s", backend)
	}
	s.mu.Lock()
	s.backend = normalized
	s.mu.Unlock()
	return nil
}

func (s *PGVectorStore) Backend() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backend
}

func (s *PGVectorStore) Search(ctx context.Context, input domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	if input.TopK <= 0 {
		input.TopK = 10
	}
	if input.EmbeddingVersion <= 0 {
		input.EmbeddingVersion = 1
	}
	queryVector, embeddingModel, err := s.embedQuery(ctx, input)
	if err != nil {
		return nil, err
	}

	db := s.currentDB()
	if db == nil {
		return s.searchInMemory(input, queryVector, embeddingModel), nil
	}

	limit := input.TopK * 4
	if limit < 20 {
		limit = 20
	}
	vectorArgPosition := 3
	limitArgPosition := 4
	filterClause := ""
	vectorColumn := "e.vector"
	vectorParameterCast := "vector"
	if vectorType, ok := annVectorType(len(queryVector)); ok {
		vectorColumn = fmt.Sprintf("e.vector::%s(%d)", vectorType, len(queryVector))
		vectorParameterCast = fmt.Sprintf("%s(%d)", vectorType, len(queryVector))
		filterClause += fmt.Sprintf(" AND vector_dims(e.vector) = %d", len(queryVector))
	}
	args := []any{input.UserID, input.EmbeddingVersion}
	if strings.TrimSpace(input.GenerationID) != "" {
		filterClause += " AND (e.generation_id = $3 OR (e.generation_id = '' AND e.node_type = 'key_memory'))"
		args = append(args, input.GenerationID)
		vectorArgPosition = 4
		limitArgPosition = 5
	}
	if strings.TrimSpace(embeddingModel) != "" {
		filterClause += fmt.Sprintf(" AND e.embedding_model = $%d", len(args)+1)
		args = append(args, embeddingModel)
		vectorArgPosition++
		limitArgPosition++
	}
	query := fmt.Sprintf(`
	        SELECT e.node_id, e.node_type, 1 - (%s <=> $%d::%s) AS score
	        FROM memory_embeddings e
	        JOIN memory_nodes n ON n.id = e.node_id AND n.user_id = e.user_id AND n.embedding_status = 'ready'
	        WHERE e.user_id = $1 AND e.embedding_version = $2%s
	        ORDER BY %s <=> $%d::%s ASC
	        LIMIT $%d
	    `, vectorColumn, vectorArgPosition, vectorParameterCast, filterClause, vectorColumn, vectorArgPosition, vectorParameterCast, limitArgPosition)
	args = append(args, vectorLiteral(queryVector), limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.VectorSearchResult, 0, input.TopK)
	for rows.Next() {
		var item domain.VectorSearchResult
		if err := rows.Scan(&item.NodeID, &item.NodeType, &item.Score); err != nil {
			return nil, err
		}
		item.UserID = input.UserID
		if !matchesAllowedType(item.NodeType, input.AllowedTypes) {
			continue
		}
		out = append(out, item)
		if len(out) >= input.TopK {
			break
		}
	}
	return out, rows.Err()
}

func annVectorType(dimension int) (string, bool) {
	switch dimension {
	case 3, 768, 1024, 1536:
		return "vector", true
	case 3072:
		return "halfvec", true
	default:
		return "", false
	}
}

func (s *PGVectorStore) Upsert(ctx context.Context, input domain.VectorUpsertRequest) error {
	if input.EmbeddingVersion <= 0 {
		input.EmbeddingVersion = 1
	}
	if strings.TrimSpace(input.Content) == "" {
		input.Content = input.ContentHash
	}
	if strings.TrimSpace(input.Content) == "" {
		input.Content = input.NodeID
	}
	if len(input.Vector) == 0 {
		embedded, err := s.embedDocument(ctx, input)
		if err != nil {
			return err
		}
		input.Vector = embedded.Vector
		if strings.TrimSpace(input.EmbeddingModel) == "" {
			input.EmbeddingModel = embedded.Model
		}
	}
	if strings.TrimSpace(input.EmbeddingModel) == "" {
		input.EmbeddingModel = deterministicEmbeddingModel
	}
	if strings.TrimSpace(input.ContentHash) == "" {
		input.ContentHash = contentDigest(input.Content)
	}

	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.items[input.UserID+"::"+input.GenerationID+"::"+input.NodeID+"::"+fmt.Sprintf("%d", input.EmbeddingVersion)] = input
		return nil
	}

	now := time.Now().UTC()
	embeddingID := fmt.Sprintf("%s:%s:%d", input.UserID, input.NodeID, input.EmbeddingVersion)
	insertPrefix := `INSERT INTO memory_embeddings (
	            embedding_id, generation_id, user_id, node_id, node_type, embedding_version, embedding_model,
	            content_hash, vector, created_at, updated_at
	        ) `
	valuesClause := `VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::vector,$10,$10)`
	if input.EmbeddingRevision > 0 {
		valuesClause = `SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9::vector,$10,$10
			WHERE EXISTS (
				SELECT 1 FROM memory_nodes
				WHERE id = $4 AND user_id = $3 AND embedding_revision = $11 AND embedding_status = 'pending'
			)`
	}
	query := insertPrefix + valuesClause + `
	        ON CONFLICT (user_id, node_id, embedding_version) DO UPDATE SET
	            generation_id = EXCLUDED.generation_id,
            node_type = EXCLUDED.node_type,
            embedding_model = EXCLUDED.embedding_model,
            content_hash = EXCLUDED.content_hash,
            vector = EXCLUDED.vector,
	            updated_at = EXCLUDED.updated_at`
	args := []any{embeddingID, input.GenerationID, input.UserID, input.NodeID, input.NodeType, input.EmbeddingVersion, input.EmbeddingModel, input.ContentHash, vectorLiteral(input.Vector), now}
	if input.EmbeddingRevision > 0 {
		args = append(args, input.EmbeddingRevision)
	}
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if input.EmbeddingRevision > 0 {
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if rows == 0 {
			return nil
		}
	}
	readyQuery := `UPDATE memory_nodes SET embedding_status = 'ready', updated_at = $1 WHERE id = $2 AND user_id = $3`
	readyArgs := []any{now, input.NodeID, input.UserID}
	if input.EmbeddingRevision > 0 {
		readyQuery += ` AND embedding_revision = $4`
		readyArgs = append(readyArgs, input.EmbeddingRevision)
	}
	_, err = db.ExecContext(ctx, readyQuery, readyArgs...)
	return err
}

func (s *PGVectorStore) DeleteByNode(ctx context.Context, userID, nodeID string) error {
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for key := range s.items {
			if strings.Contains(key, "::"+nodeID+"::") && strings.HasPrefix(key, userID+"::") {
				delete(s.items, key)
			}
		}
		return nil
	}
	_, err := db.ExecContext(ctx, `DELETE FROM memory_embeddings WHERE user_id = $1 AND node_id = $2`, userID, nodeID)
	return err
}

func (s *PGVectorStore) Health(ctx context.Context) error {
	db := s.currentDB()
	if db == nil {
		return nil
	}
	return db.PingContext(ctx)
}

func (s *PGVectorStore) ReindexVersion(ctx context.Context, version int) error {
	db := s.currentDB()
	if db == nil || version <= 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `
        UPDATE memory_nodes
        SET embedding_status = CASE
            WHEN EXISTS (
                SELECT 1
                FROM memory_embeddings e
                WHERE e.node_id = memory_nodes.id AND e.user_id = memory_nodes.user_id AND e.embedding_version = $1
            ) THEN 'ready'
            ELSE 'pending'
        END,
        updated_at = $2
	`, version, time.Now().UTC())
	return err
}

func (s *PGVectorStore) currentDB() *sql.DB {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Current()
}

func (s *PGVectorStore) embedQuery(ctx context.Context, input domain.VectorSearchInput) ([]float32, string, error) {
	model := strings.TrimSpace(input.EmbeddingModel)
	if s.embedder == nil {
		return EmbedText(input.Query), model, nil
	}
	result, err := s.embedder.Embed(ctx, domain.EmbeddingInput{
		UserID: input.UserID,
		Text:   input.Query,
		Model:  model,
	})
	if err != nil {
		return nil, "", err
	}
	return result.Vector, firstNonEmpty(strings.TrimSpace(result.Model), model), nil
}

func (s *PGVectorStore) embedDocument(ctx context.Context, input domain.VectorUpsertRequest) (domain.EmbeddingResult, error) {
	if s.embedder == nil {
		return domain.EmbeddingResult{
			Vector: EmbedText(input.Content),
			Model:  firstNonEmpty(strings.TrimSpace(input.EmbeddingModel), deterministicEmbeddingModel),
		}, nil
	}
	return s.embedder.Embed(ctx, domain.EmbeddingInput{
		UserID: input.UserID,
		Text:   input.Content,
		Model:  strings.TrimSpace(input.EmbeddingModel),
	})
}

func EmbedText(text string) []float32 {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return []float32{1, 0, 0}
	}
	sum := sha256.Sum256([]byte(normalized))
	values := make([]float64, embeddingDimensions)
	for idx, b := range sum {
		values[idx%embeddingDimensions] += float64(b) / 255.0
	}
	for idx, r := range normalized {
		values[idx%embeddingDimensions] += float64((int(r)%97)+1) / 50.0
	}
	norm := 0.0
	for _, value := range values {
		norm += value * value
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return []float32{1, 0, 0}
	}
	out := make([]float32, embeddingDimensions)
	for idx, value := range values {
		out[idx] = float32(value / norm)
	}
	return out
}

func Similarity(left, right []float32) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	size := len(left)
	if len(right) < size {
		size = len(right)
	}
	var dot float64
	for idx := 0; idx < size; idx++ {
		dot += float64(left[idx] * right[idx])
	}
	return dot
}

func (s *PGVectorStore) searchInMemory(input domain.VectorSearchInput, queryVector []float32, embeddingModel string) []domain.VectorSearchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]domain.VectorSearchResult, 0)
	for _, item := range s.items {
		if item.UserID != input.UserID || item.EmbeddingVersion != input.EmbeddingVersion {
			continue
		}
		if strings.TrimSpace(input.GenerationID) != "" && !matchesVectorGeneration(input.GenerationID, item.GenerationID, item.NodeType) {
			continue
		}
		if strings.TrimSpace(embeddingModel) != "" && item.EmbeddingModel != embeddingModel {
			continue
		}
		if !matchesAllowedType(item.NodeType, input.AllowedTypes) {
			continue
		}
		results = append(results, domain.VectorSearchResult{
			NodeID:   item.NodeID,
			Score:    Similarity(queryVector, item.Vector),
			UserID:   item.UserID,
			NodeType: item.NodeType,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].NodeID < results[j].NodeID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > input.TopK {
		results = results[:input.TopK]
	}
	return results
}

func matchesVectorGeneration(requestedGenerationID, actualGenerationID, nodeType string) bool {
	requestedGenerationID = strings.TrimSpace(requestedGenerationID)
	actualGenerationID = strings.TrimSpace(actualGenerationID)
	if requestedGenerationID == "" {
		return true
	}
	if actualGenerationID == requestedGenerationID {
		return true
	}
	return actualGenerationID == "" && nodeType == string(domain.MemoryTypeKeyMemory)
}

func vectorLiteral(vector []float32) string {
	parts := make([]string, 0, len(vector))
	for _, value := range vector {
		parts = append(parts, fmt.Sprintf("%.6f", value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func contentDigest(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return fmt.Sprintf("%x", sum)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func matchesAllowedType(nodeType string, allowed []domain.MemoryType) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if string(item) == nodeType {
			return true
		}
	}
	return false
}

const deterministicEmbeddingModel = "deterministic-local"
