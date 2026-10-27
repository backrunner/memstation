package vector

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	storeaccess "memstation/apps/server/internal/store/access"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/store/sql/memorygeneration"
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
	workspace, err := security.DataScope(ctx, input.UserID, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = workspace
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
		return s.searchInMemory(ctx, input, queryVector, embeddingModel), nil
	}

	limit := input.TopK * 4
	if limit < 20 {
		limit = 20
	}
	filterClause := " AND " + storeaccess.NodeSQL(ctx, "n")
	vectorColumn := "e.vector"
	vectorParameterCast := "vector"
	if vectorType, ok := annVectorType(len(queryVector)); ok {
		vectorColumn = fmt.Sprintf("e.vector::%s(%d)", vectorType, len(queryVector))
		vectorParameterCast = fmt.Sprintf("%s(%d)", vectorType, len(queryVector))
		filterClause += fmt.Sprintf(" AND vector_dims(e.vector) = %d", len(queryVector))
	}
	args := []any{input.UserID, input.EmbeddingVersion, workspace}
	if strings.TrimSpace(input.GenerationID) != "" {
		filterClause += " AND (e.generation_id = $4 OR (e.generation_id = '' AND e.node_type = 'key_memory'))"
		args = append(args, input.GenerationID)
	}
	if strings.TrimSpace(embeddingModel) != "" {
		filterClause += fmt.Sprintf(" AND e.embedding_model = $%d", len(args)+1)
		args = append(args, embeddingModel)
	}
	if len(input.AllowedTypes) > 0 {
		types := make([]string, 0, len(input.AllowedTypes))
		for _, kind := range input.AllowedTypes {
			args = append(args, string(kind))
			types = append(types, fmt.Sprintf("$%d", len(args)))
		}
		filterClause += " AND e.node_type IN (" + strings.Join(types, ",") + ")"
	}
	vectorArgPosition := len(args) + 1
	limitArgPosition := len(args) + 2
	query := fmt.Sprintf(`
	        SELECT e.node_id, e.node_type, 1 - (%s <=> $%d::%s) AS score
	        FROM memory_embeddings e
	        JOIN memory_nodes n ON n.id = e.node_id AND n.user_id = e.user_id AND n.workspace_id=e.workspace_id AND n.embedding_status = 'ready' AND n.node_type=e.node_type AND n.generation_id=e.generation_id
	        WHERE e.user_id = $1 AND e.embedding_version = $2 AND e.workspace_id=$3%s
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
		item.WorkspaceID = workspace
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
	workspace, err := security.DataScope(ctx, input.UserID, input.WorkspaceID)
	if err != nil {
		return err
	}
	input.WorkspaceID = workspace
	input.PartitionID = security.WritePartition(ctx)
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
		if strings.TrimSpace(embedded.Model) != "" {
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
		identity, _ := json.Marshal([]string{input.UserID, workspace, input.NodeID, fmt.Sprintf("%d", input.EmbeddingVersion)})
		s.items[string(identity)] = input
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var expectedGeneration string
	if err = tx.QueryRowContext(ctx, `SELECT generation_id FROM memory_nodes WHERE id=$1 AND user_id=$2 AND workspace_id=$3`, input.NodeID, input.UserID, workspace).Scan(&expectedGeneration); errors.Is(err, sql.ErrNoRows) {
		// Preserve scope denial for known hidden parents; missing nodes remain a no-op.
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_nodes WHERE id=$1)`, input.NodeID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return domain.ErrAccessDenied
		}
		return nil
	} else if err != nil {
		return err
	}
	lockedGeneration := expectedGeneration
	if input.GenerationID != "" {
		lockedGeneration = input.GenerationID
	}
	if writable, err := memorygeneration.LockWritableGeneration(ctx, tx, input.UserID, workspace, lockedGeneration); err != nil || !writable {
		return err
	}
	var owner, parentWorkspace, generation, kind, status, partition, accessStatus string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT user_id,workspace_id,generation_id,node_type,embedding_revision,embedding_status,partition_id,access_status FROM memory_nodes WHERE id=$1 FOR UPDATE`, input.NodeID).Scan(&owner, &parentWorkspace, &generation, &kind, &revision, &status, &partition, &accessStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // A delayed task must not recreate a deleted node's embedding.
	}
	if err != nil {
		return err
	}
	carry := false
	if input.GenerationID != "" && input.GenerationID != generation {
		carry, err = memorygeneration.ValidateCarryEmbeddingTx(ctx, tx, input.UserID, workspace, input.GenerationID, input.NodeID)
		if err != nil {
			return err
		}
	}
	if owner != input.UserID || parentWorkspace != workspace || kind != input.NodeType || (input.GenerationID != "" && input.GenerationID != generation && !carry) {
		return domain.ErrAccessDenied
	}
	if !security.CanReadPartition(ctx, partition) {
		return domain.ErrAccessDenied
	}
	if accessStatus != "active" {
		return nil
	}
	if generation != expectedGeneration {
		return errors.New("embedding parent generation changed; retry")
	}
	if !carry {
		input.GenerationID = generation
	}
	if !carry && input.EmbeddingRevision > 0 && (revision != input.EmbeddingRevision || status != "pending") {
		return nil
	}
	now := time.Now().UTC()
	embeddingID := fmt.Sprintf("%s:%s:%d", input.UserID, input.NodeID, input.EmbeddingVersion)
	insertPrefix := `INSERT INTO memory_embeddings (
	            embedding_id, generation_id, user_id, node_id, node_type, embedding_version, embedding_model,
	            content_hash, vector, created_at, updated_at, workspace_id
	        ) `
	valuesClause := `VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::vector,$10,$10,$11)`
	query := insertPrefix + valuesClause + `
	        ON CONFLICT (user_id, node_id, embedding_version) DO UPDATE SET
	            generation_id = EXCLUDED.generation_id,
            node_type = EXCLUDED.node_type,
            embedding_model = EXCLUDED.embedding_model,
            content_hash = EXCLUDED.content_hash,
            vector = EXCLUDED.vector,
	            updated_at = EXCLUDED.updated_at`
	args := []any{embeddingID, input.GenerationID, input.UserID, input.NodeID, input.NodeType, input.EmbeddingVersion, input.EmbeddingModel, input.ContentHash, vectorLiteral(input.Vector), now, workspace}
	_, err = tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if carry {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE memory_nodes SET embedding_status='ready',updated_at=$1 WHERE id=$2 AND user_id=$3 AND workspace_id=$4`, now, input.NodeID, input.UserID, workspace)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PGVectorStore) DeleteByNode(ctx context.Context, input domain.VectorDeleteRequest) error {
	workspace, err := security.DataScope(ctx, input.UserID, "")
	if err != nil {
		return err
	}
	if input.NodeID == "" || input.EmbeddingRevision < 0 || (input.EmbeddingRevision > 0) == input.RequireMissingNode {
		return domain.ErrAccessDenied
	}
	db := s.currentDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for key, item := range s.items {
			if item.UserID == input.UserID && item.WorkspaceID == workspace && item.NodeID == input.NodeID && security.CanReadPartition(ctx, item.PartitionID) && (input.RequireMissingNode || item.EmbeddingRevision < input.EmbeddingRevision) {
				delete(s.items, key)
			}
		}
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Share the node-creation lock so an absent-node cleanup cannot race recreation.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "memory-node:"+input.NodeID); err != nil {
		return err
	}
	var revision int64
	var state, partition string
	err = tx.QueryRowContext(ctx, `SELECT embedding_revision,embedding_status,partition_id FROM memory_nodes WHERE user_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE`, input.UserID, workspace, input.NodeID).Scan(&revision, &state, &partition)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if !security.CanReadPartition(ctx, partition) {
			return domain.ErrAccessDenied
		}
		if input.RequireMissingNode || revision != input.EmbeddingRevision || state != "deleted" {
			return tx.Commit()
		}
	} else if !input.RequireMissingNode {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM memory_embeddings WHERE user_id=$1 AND workspace_id=$2 AND node_id=$3`, input.UserID, workspace, input.NodeID); err != nil {
		return err
	}
	return tx.Commit()
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
		UserID: input.UserID, GenerationID: input.GenerationID, EmbeddingVersion: input.EmbeddingVersion,
		Text:  input.Query,
		Model: model,
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
		UserID: input.UserID, GenerationID: input.GenerationID, EmbeddingVersion: input.EmbeddingVersion,
		Text:  input.Content,
		Model: strings.TrimSpace(input.EmbeddingModel),
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

func (s *PGVectorStore) searchInMemory(ctx context.Context, input domain.VectorSearchInput, queryVector []float32, embeddingModel string) []domain.VectorSearchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]domain.VectorSearchResult, 0)
	for _, item := range s.items {
		if item.UserID != input.UserID || item.WorkspaceID != input.WorkspaceID || !security.CanReadPartition(ctx, item.PartitionID) || item.EmbeddingVersion != input.EmbeddingVersion {
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
			WorkspaceID: item.WorkspaceID,
			NodeID:      item.NodeID,
			Score:       Similarity(queryVector, item.Vector),
			UserID:      item.UserID,
			NodeType:    item.NodeType,
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
