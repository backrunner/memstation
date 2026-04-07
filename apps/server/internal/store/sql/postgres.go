package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
)

type PostgresConversationRepository struct {
	db *sqlpkg.DB
}

func NewPostgresConversationRepository(db *sqlpkg.DB) *PostgresConversationRepository {
	return &PostgresConversationRepository{db: db}
}

func (r *PostgresConversationRepository) PersistExchange(ctx context.Context, conversation domain.ConversationRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	existingMessages, err := r.loadMessagesTx(ctx, tx, conversation.UserID, conversation.ConversationID)
	if err != nil {
		return err
	}
	plan := planConversationMessages(existingMessages, conversation.Messages)
	if conversation.Metadata == nil {
		conversation.Metadata = map[string]any{}
	}
	conversation.Metadata["message_count"] = len(plan.Stored)
	if len(plan.Stored) > 0 {
		conversation.Metadata["last_sequence_num"] = len(plan.Stored) - 1
	}
	conversation.Metadata["message_storage_mode"] = plan.Mode
	conversation.Metadata["history_rewrite_detected"] = plan.Mode == "rewrite"
	conversation.Metadata["history_partial_snapshot"] = plan.Mode == "partial_snapshot"

	metadata := marshalJSON(conversation.Metadata)
	if _, err = tx.ExecContext(ctx, `
        INSERT INTO conversations (
            id, user_id, request_format, upstream_provider, mode, finalize_status,
            summary_status, analysis_status, metadata, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
        ON CONFLICT (id) DO UPDATE SET
            user_id = EXCLUDED.user_id,
            request_format = EXCLUDED.request_format,
            upstream_provider = EXCLUDED.upstream_provider,
            mode = EXCLUDED.mode,
            finalize_status = EXCLUDED.finalize_status,
            summary_status = EXCLUDED.summary_status,
            analysis_status = EXCLUDED.analysis_status,
            metadata = EXCLUDED.metadata,
            updated_at = EXCLUDED.updated_at
    `,
		conversation.ConversationID,
		conversation.UserID,
		conversation.RequestFormat,
		string(conversation.Provider),
		string(conversation.Mode),
		string(conversation.FinalizeStatus),
		statusOrDefault(string(conversation.SummaryStatus), string(domain.ProcessingStatusPending)),
		statusOrDefault(string(conversation.AnalysisStatus), string(domain.ProcessingStatusPending)),
		metadata,
		zeroTime(conversation.CreatedAt),
		zeroTime(conversation.UpdatedAt),
	); err != nil {
		return err
	}

	if plan.DeleteFrom != nil {
		if _, err = tx.ExecContext(ctx, `
            DELETE FROM messages
            WHERE conversation_id = $1 AND user_id = $2 AND sequence_num >= $3
        `, conversation.ConversationID, conversation.UserID, *plan.DeleteFrom); err != nil {
			return err
		}
	}

	for offset, message := range plan.Insert {
		sequenceNum := plan.InsertFrom + offset
		id := stableMessageID(conversation.ConversationID, sequenceNum, message)
		_, err = tx.ExecContext(ctx, `
            INSERT INTO messages (
                id, user_id, conversation_id, role, content_blocks, text_content,
                tool_calls, tool_result, provider_payload, sequence_num, created_at, updated_at
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
            ON CONFLICT (id) DO UPDATE SET
                content_blocks = EXCLUDED.content_blocks,
                text_content = EXCLUDED.text_content,
                tool_calls = EXCLUDED.tool_calls,
                tool_result = EXCLUDED.tool_result,
                provider_payload = EXCLUDED.provider_payload,
                updated_at = EXCLUDED.updated_at
        `,
			id,
			conversation.UserID,
			conversation.ConversationID,
			string(message.Role),
			marshalJSON(message.Blocks),
			message.Text,
			marshalJSON(message.ToolCalls),
			marshalJSON(firstToolResult(message.ToolResults)),
			marshalJSON(message.Metadata),
			sequenceNum,
			zeroTime(conversation.CreatedAt),
			zeroTime(conversation.UpdatedAt),
		)
		if err != nil {
			return err
		}
	}

	if conversation.Usage.TotalTokens > 0 {
		usageID := stableUsageID(conversation.ConversationID, conversation.Metadata)
		_, err = tx.ExecContext(ctx, `
            INSERT INTO token_usages (
                id, user_id, conversation_id, provider, model, input_tokens, output_tokens, total_tokens, cost, created_at, updated_at
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
            ON CONFLICT (id) DO UPDATE SET
                input_tokens = EXCLUDED.input_tokens,
                output_tokens = EXCLUDED.output_tokens,
                total_tokens = EXCLUDED.total_tokens,
                updated_at = EXCLUDED.updated_at
        `,
			usageID,
			conversation.UserID,
			conversation.ConversationID,
			string(conversation.Provider),
			responseModel(conversation.Response),
			conversation.Usage.InputTokens,
			conversation.Usage.OutputTokens,
			conversation.Usage.TotalTokens,
			0,
			zeroTime(conversation.CreatedAt),
			zeroTime(conversation.UpdatedAt),
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *PostgresConversationRepository) GetConversation(ctx context.Context, userID, conversationID string) (domain.ConversationRecord, error) {
	row := r.db.QueryRowContext(ctx, `
        SELECT request_format, upstream_provider, mode, finalize_status, summary_status, analysis_status, metadata, created_at, updated_at
        FROM conversations
        WHERE id = $1 AND user_id = $2
    `, conversationID, userID)

	var record domain.ConversationRecord
	var provider string
	var mode string
	var finalizeStatus string
	var summaryStatus string
	var analysisStatus string
	var metadataBytes []byte
	record.ConversationID = conversationID
	record.UserID = userID
	if err := row.Scan(&record.RequestFormat, &provider, &mode, &finalizeStatus, &summaryStatus, &analysisStatus, &metadataBytes, &record.CreatedAt, &record.UpdatedAt); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ConversationRecord{}, errors.New("conversation not found")
		}
		return domain.ConversationRecord{}, err
	}
	record.Provider = domain.Provider(provider)
	record.Mode = domain.Mode(mode)
	record.FinalizeStatus = domain.FinalizeStatus(finalizeStatus)
	record.SummaryStatus = domain.ProcessingStatus(summaryStatus)
	record.AnalysisStatus = domain.ProcessingStatus(analysisStatus)
	_ = json.Unmarshal(metadataBytes, &record.Metadata)

	messages, err := r.loadMessages(ctx, userID, conversationID)
	if err != nil {
		return domain.ConversationRecord{}, err
	}
	record.Messages = messages
	segments, err := r.loadSegments(ctx, userID, conversationID)
	if err != nil {
		return domain.ConversationRecord{}, err
	}
	record.Segments = segments

	usageRow := r.db.QueryRowContext(ctx, `
        SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(total_tokens), 0)
        FROM token_usages
        WHERE conversation_id = $1 AND user_id = $2
    `, conversationID, userID)
	if err := usageRow.Scan(&record.Usage.InputTokens, &record.Usage.OutputTokens, &record.Usage.TotalTokens); err != nil {
		return domain.ConversationRecord{}, err
	}
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		if last.Role == domain.RoleAssistant {
			record.Response = &domain.UnifiedResponse{
				ResponseID: stableMessageID(conversationID, len(messages)-1, last),
				Protocol:   domain.Protocol(record.RequestFormat),
				Provider:   record.Provider,
				Message:    last,
				Usage:      record.Usage,
				CreatedAt:  record.UpdatedAt,
			}
		}
	}
	return record, nil
}

func (r *PostgresConversationRepository) ListConversations(ctx context.Context, userID string) ([]domain.ConversationRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, request_format, upstream_provider, mode, finalize_status, summary_status, analysis_status, metadata, created_at, updated_at
        FROM conversations
        WHERE user_id = $1
        ORDER BY updated_at DESC
    `, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.ConversationRecord, 0)
	for rows.Next() {
		var item domain.ConversationRecord
		var provider string
		var mode string
		var finalizeStatus string
		var summaryStatus string
		var analysisStatus string
		var metadataBytes []byte
		item.UserID = userID
		if err := rows.Scan(&item.ConversationID, &item.RequestFormat, &provider, &mode, &finalizeStatus, &summaryStatus, &analysisStatus, &metadataBytes, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Provider = domain.Provider(provider)
		item.Mode = domain.Mode(mode)
		item.FinalizeStatus = domain.FinalizeStatus(finalizeStatus)
		item.SummaryStatus = domain.ProcessingStatus(summaryStatus)
		item.AnalysisStatus = domain.ProcessingStatus(analysisStatus)
		_ = json.Unmarshal(metadataBytes, &item.Metadata)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PostgresConversationRepository) ListTimedOutConversations(ctx context.Context, before time.Time, limit int) ([]domain.ConversationRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, user_id, request_format, upstream_provider, mode, finalize_status, summary_status, analysis_status, metadata, created_at, updated_at
        FROM conversations
        WHERE finalize_status = 'active' AND updated_at < $1
        ORDER BY updated_at ASC
        LIMIT $2
    `, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.ConversationRecord, 0)
	for rows.Next() {
		var item domain.ConversationRecord
		var provider string
		var mode string
		var finalizeStatus string
		var summaryStatus string
		var analysisStatus string
		var metadataBytes []byte
		if err := rows.Scan(&item.ConversationID, &item.UserID, &item.RequestFormat, &provider, &mode, &finalizeStatus, &summaryStatus, &analysisStatus, &metadataBytes, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Provider = domain.Provider(provider)
		item.Mode = domain.Mode(mode)
		item.FinalizeStatus = domain.FinalizeStatus(finalizeStatus)
		item.SummaryStatus = domain.ProcessingStatus(summaryStatus)
		item.AnalysisStatus = domain.ProcessingStatus(analysisStatus)
		_ = json.Unmarshal(metadataBytes, &item.Metadata)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PostgresConversationRepository) ReplaceSegments(ctx context.Context, userID, conversationID string, segments []domain.ConversationSegment) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	if _, err = tx.ExecContext(ctx, `
        DELETE FROM conversation_segments
        WHERE user_id = $1 AND conversation_id = $2
    `, userID, conversationID); err != nil {
		return err
	}
	for _, segment := range segments {
		if _, err = tx.ExecContext(ctx, `
            INSERT INTO conversation_segments (
                id, user_id, conversation_id, segment_index, start_sequence, end_sequence,
                message_count, overlap_from_sequence, overlap_message_count, content, content_hash,
                metadata, created_at, updated_at
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
        `, segment.ID, segment.UserID, segment.ConversationID, segment.SegmentIndex, segment.StartSequence,
			segment.EndSequence, segment.MessageCount, segment.OverlapFromSequence, segment.OverlapMessageCount,
			segment.Content, segment.ContentHash, marshalJSON(segment.Metadata), zeroTime(segment.CreatedAt),
			zeroTime(segment.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *PostgresConversationRepository) FinalizeConversation(ctx context.Context, input domain.ConversationFinalizeInput) error {
	metadata := marshalJSON(map[string]any{"finalize_reason": input.Reason})
	_, err := r.db.ExecContext(ctx, `
        UPDATE conversations
        SET finalize_status = $1,
            metadata = COALESCE(metadata, '{}'::jsonb) || $2::jsonb,
            updated_at = $3
        WHERE id = $4 AND user_id = $5
    `, string(input.Status), string(metadata), time.Now().UTC(), input.ConversationID, input.UserID)
	return err
}

func (r *PostgresConversationRepository) UpdateConversationState(ctx context.Context, input domain.ConversationStateUpdate) error {
	metadata := marshalJSON(input.Metadata)
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}
	updatedAt := input.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
        UPDATE conversations
        SET finalize_status = COALESCE($1, finalize_status),
            summary_status = COALESCE($2, summary_status),
            analysis_status = COALESCE($3, analysis_status),
            metadata = COALESCE(metadata, '{}'::jsonb) || $4::jsonb,
            updated_at = $5
        WHERE id = $6 AND user_id = $7
    `, nullableFinalizeStatus(input.FinalizeStatus), nullableProcessingStatus(input.SummaryStatus), nullableProcessingStatus(input.AnalysisStatus), string(metadata), updatedAt, input.ConversationID, input.UserID)
	return err
}

func (r *PostgresConversationRepository) loadMessages(ctx context.Context, userID, conversationID string) ([]domain.UnifiedMessage, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT role, text_content, content_blocks, tool_calls, tool_result, provider_payload
        FROM messages
        WHERE conversation_id = $1 AND user_id = $2
        ORDER BY sequence_num ASC, created_at ASC
    `, conversationID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.UnifiedMessage, 0)
	for rows.Next() {
		var role string
		var text string
		var blocksBytes []byte
		var toolCallsBytes []byte
		var toolResultBytes []byte
		var providerPayloadBytes []byte
		if err := rows.Scan(&role, &text, &blocksBytes, &toolCallsBytes, &toolResultBytes, &providerPayloadBytes); err != nil {
			return nil, err
		}
		item := domain.UnifiedMessage{Role: domain.MessageRole(role), Text: text}
		_ = json.Unmarshal(blocksBytes, &item.Blocks)
		_ = json.Unmarshal(toolCallsBytes, &item.ToolCalls)
		var toolResult map[string]any
		_ = json.Unmarshal(toolResultBytes, &toolResult)
		if len(toolResult) > 0 {
			item.ToolResults = []domain.ToolResult{{
				ToolCallID: stringValue(toolResult, "tool_call_id"),
				Name:       stringValue(toolResult, "name"),
				Output:     toolResult,
			}}
		}
		_ = json.Unmarshal(providerPayloadBytes, &item.Metadata)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PostgresConversationRepository) loadMessagesTx(ctx context.Context, tx *sqlpkg.Tx, userID, conversationID string) ([]domain.UnifiedMessage, error) {
	rows, err := tx.QueryContext(ctx, `
        SELECT role, text_content, content_blocks, tool_calls, tool_result, provider_payload
        FROM messages
        WHERE conversation_id = $1 AND user_id = $2
        ORDER BY sequence_num ASC, created_at ASC
    `, conversationID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.UnifiedMessage, 0)
	for rows.Next() {
		var role string
		var text string
		var blocksBytes []byte
		var toolCallsBytes []byte
		var toolResultBytes []byte
		var providerPayloadBytes []byte
		if err := rows.Scan(&role, &text, &blocksBytes, &toolCallsBytes, &toolResultBytes, &providerPayloadBytes); err != nil {
			return nil, err
		}
		item := domain.UnifiedMessage{Role: domain.MessageRole(role), Text: text}
		_ = json.Unmarshal(blocksBytes, &item.Blocks)
		_ = json.Unmarshal(toolCallsBytes, &item.ToolCalls)
		var toolResult map[string]any
		_ = json.Unmarshal(toolResultBytes, &toolResult)
		if len(toolResult) > 0 {
			item.ToolResults = []domain.ToolResult{{
				ToolCallID: stringValue(toolResult, "tool_call_id"),
				Name:       stringValue(toolResult, "name"),
				Output:     toolResult,
			}}
		}
		_ = json.Unmarshal(providerPayloadBytes, &item.Metadata)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PostgresConversationRepository) loadSegments(ctx context.Context, userID, conversationID string) ([]domain.ConversationSegment, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, segment_index, start_sequence, end_sequence, message_count,
               overlap_from_sequence, overlap_message_count, content, content_hash,
               metadata, created_at, updated_at
        FROM conversation_segments
        WHERE user_id = $1 AND conversation_id = $2
        ORDER BY segment_index ASC
    `, userID, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	segments := make([]domain.ConversationSegment, 0)
	for rows.Next() {
		var segment domain.ConversationSegment
		var metadataBytes []byte
		segment.UserID = userID
		segment.ConversationID = conversationID
		if err := rows.Scan(&segment.ID, &segment.SegmentIndex, &segment.StartSequence, &segment.EndSequence,
			&segment.MessageCount, &segment.OverlapFromSequence, &segment.OverlapMessageCount,
			&segment.Content, &segment.ContentHash, &metadataBytes, &segment.CreatedAt, &segment.UpdatedAt); err != nil {
			return nil, err
		}
		if len(metadataBytes) > 0 {
			segment.Metadata = map[string]any{}
			if err := json.Unmarshal(metadataBytes, &segment.Metadata); err != nil {
				return nil, err
			}
		}
		segments = append(segments, segment)
	}
	return segments, rows.Err()
}

type PostgresMemoryRepository struct {
	db *sqlpkg.DB
}

func NewPostgresMemoryRepository(db *sqlpkg.DB) *PostgresMemoryRepository {
	return &PostgresMemoryRepository{db: db}
}

func (r *PostgresMemoryRepository) Search(ctx context.Context, input domain.SearchInput) (domain.SearchResult, error) {
	docs, err := r.ListDocuments(ctx, input.UserID, input.MemoryTypes, 200)
	if err != nil {
		return domain.SearchResult{}, err
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	filtered := make([]domain.MemoryDocument, 0, len(docs))
	now := time.Now().UTC()
	for _, doc := range docs {
		if !matchesTemporalInput(doc, input) {
			continue
		}
		score := 0.2
		if query == "" {
			score = 0.5
		} else if strings.Contains(strings.ToLower(doc.Content), query) {
			score = 0.95
		} else if strings.Contains(strings.ToLower(doc.Content), firstToken(query)) {
			score = 0.6
		}
		doc.Score = score
		doc.Importance = domain.ClampImportance(doc.Importance)
		doc.DecayFactor = domain.ClampDecayFactor(doc.DecayFactor)
		doc.ActivityAt = domain.DeriveActivityAt(doc.ActivityAt, doc.UpdatedAt, doc.CreatedAt)
		doc.TimeTier = domain.ClassifyTimeTier(doc.ActivityAt, now)
		doc.EffectiveScore = score * doc.Importance * domain.TimeDecay(doc.Type, doc.ActivityAt, doc.DecayFactor, doc.Importance, now) * domain.StalePenalty(doc, now)
		if doc.Score >= input.MinScore {
			filtered = append(filtered, doc)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].EffectiveScore == filtered[j].EffectiveScore {
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		}
		return filtered[i].EffectiveScore > filtered[j].EffectiveScore
	})
	if input.TopN > 0 && len(filtered) > input.TopN {
		filtered = filtered[:input.TopN]
	}
	return domain.SearchResult{Memories: filtered, Total: len(filtered), QueryEmbeddingTokens: len(query) / 4}, nil
}

func (r *PostgresMemoryRepository) StoreDocument(ctx context.Context, doc domain.MemoryDocument) error {
	if doc.ID == "" {
		doc.ID = uuid.NewString()
	}
	if doc.NodeID == "" {
		doc.NodeID = uuid.NewString()
	}
	now := time.Now().UTC()
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = now
	}
	doc.UpdatedAt = now
	if doc.Importance == 0 {
		doc.Importance = 1
	}
	if doc.DecayFactor == 0 {
		doc.DecayFactor = 1
	}
	doc.Importance = domain.ClampImportance(doc.Importance)
	doc.DecayFactor = domain.ClampDecayFactor(doc.DecayFactor)
	doc.ActivityAt = domain.DeriveActivityAt(doc.ActivityAt, doc.UpdatedAt, doc.CreatedAt)
	if doc.TimeTier == "" {
		doc.TimeTier = domain.ClassifyTimeTier(doc.ActivityAt, now)
	}
	doc.Metadata = cloneMetadata(doc.Metadata)
	doc.Metadata["activity_at"] = doc.ActivityAt.Format(time.RFC3339)
	doc.Metadata["time_tier"] = doc.TimeTier

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	_, err = tx.ExecContext(ctx, `
	        INSERT INTO memory_nodes (id, user_id, node_type, entity_id, importance, decay_factor, activity_at, time_tier, embedding_status, created_at, updated_at)
	        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	        ON CONFLICT (id) DO UPDATE SET
	            node_type = EXCLUDED.node_type,
	            entity_id = EXCLUDED.entity_id,
	            importance = EXCLUDED.importance,
	            decay_factor = EXCLUDED.decay_factor,
	            activity_at = EXCLUDED.activity_at,
	            time_tier = EXCLUDED.time_tier,
	            updated_at = EXCLUDED.updated_at
	    `, doc.NodeID, doc.UserID, string(doc.Type), doc.ID, doc.Importance, doc.DecayFactor, doc.ActivityAt, string(doc.TimeTier), "pending", doc.CreatedAt, doc.UpdatedAt)
	if err != nil {
		return err
	}

	switch doc.Type {
	case domain.MemoryTypeSummary:
		_, err = tx.ExecContext(ctx, `
            INSERT INTO memory_summaries (id, user_id, conversation_id, node_id, summary, topic_label, summary_version, chunk_index, chunk_total, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
            ON CONFLICT (id) DO UPDATE SET
                summary = EXCLUDED.summary,
                topic_label = EXCLUDED.topic_label,
                updated_at = EXCLUDED.updated_at
        `, doc.ID, doc.UserID, doc.ConversationID, doc.NodeID, doc.Content, stringValue(doc.Metadata, "topic_label"), 1, 0, 1, doc.CreatedAt, doc.UpdatedAt)
		if err == nil {
			if _, err = tx.ExecContext(ctx, `DELETE FROM memory_keywords WHERE summary_id = $1 AND user_id = $2`, doc.ID, doc.UserID); err != nil {
				return err
			}
			for idx, keyword := range stringSlice(doc.Metadata, "keywords") {
				if _, err = tx.ExecContext(ctx, `
                    INSERT INTO memory_keywords (id, user_id, summary_id, keyword, weight, created_at, updated_at)
                    VALUES ($1,$2,$3,$4,$5,$6,$7)
                `, fmt.Sprintf("%s:%d", doc.ID, idx), doc.UserID, doc.ID, keyword, 1.0-float64(idx)*0.1, doc.CreatedAt, doc.UpdatedAt); err != nil {
					return err
				}
			}
		}
	case domain.MemoryTypeSegment:
		_, err = tx.ExecContext(ctx, `
            INSERT INTO conversation_segments (
                id, user_id, conversation_id, segment_index, start_sequence, end_sequence,
                message_count, overlap_from_sequence, overlap_message_count, content, content_hash,
                metadata, created_at, updated_at
            )
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
            ON CONFLICT (id) DO UPDATE SET
                conversation_id = EXCLUDED.conversation_id,
                segment_index = EXCLUDED.segment_index,
                start_sequence = EXCLUDED.start_sequence,
                end_sequence = EXCLUDED.end_sequence,
                message_count = EXCLUDED.message_count,
                overlap_from_sequence = EXCLUDED.overlap_from_sequence,
                overlap_message_count = EXCLUDED.overlap_message_count,
                content = EXCLUDED.content,
                content_hash = EXCLUDED.content_hash,
                metadata = EXCLUDED.metadata,
                updated_at = EXCLUDED.updated_at
        `, doc.ID, doc.UserID, doc.ConversationID, intValue(doc.Metadata, "segment_index", 0), intValue(doc.Metadata, "start_sequence", 0),
			intValue(doc.Metadata, "end_sequence", 0), intValue(doc.Metadata, "message_count", 0),
			intValue(doc.Metadata, "overlap_from_sequence", 0), intValue(doc.Metadata, "overlap_message_count", 0),
			doc.Content, defaultString(stringValue(doc.Metadata, "content_hash"), doc.ID), marshalJSON(doc.Metadata), doc.CreatedAt, doc.UpdatedAt)
	case domain.MemoryTypeImpression:
		_, err = tx.ExecContext(ctx, `
            INSERT INTO impressions (id, user_id, conversation_id, node_id, subject, subject_type, evaluation, sentiment, familiarity, description, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
            ON CONFLICT (id) DO UPDATE SET
                description = EXCLUDED.description,
                subject = EXCLUDED.subject,
                subject_type = EXCLUDED.subject_type,
                updated_at = EXCLUDED.updated_at
        `, doc.ID, doc.UserID, doc.ConversationID, doc.NodeID, stringValue(doc.Metadata, "subject"), stringValue(doc.Metadata, "subject_type"), stringValue(doc.Metadata, "evaluation"), stringValue(doc.Metadata, "sentiment"), stringValue(doc.Metadata, "familiarity"), doc.Content, doc.CreatedAt, doc.UpdatedAt)
	case domain.MemoryTypeFact:
		supersededByIDValue := stringValue(doc.Metadata, "superseded_by_id")
		var supersededByID any
		if supersededByIDValue != "" {
			supersededByID = supersededByIDValue
		}
		_, err = tx.ExecContext(ctx, `
            INSERT INTO facts (id, user_id, conversation_id, node_id, category, subject, content, confidence, is_superseded, superseded_by_id, valid_from, valid_to, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
            ON CONFLICT (id) DO UPDATE SET
                content = EXCLUDED.content,
                category = EXCLUDED.category,
                subject = EXCLUDED.subject,
                confidence = EXCLUDED.confidence,
                updated_at = EXCLUDED.updated_at
		`, doc.ID, doc.UserID, doc.ConversationID, doc.NodeID, stringValue(doc.Metadata, "category"), stringValue(doc.Metadata, "subject"), doc.Content, floatValue(doc.Metadata, "confidence", 0.8), supersededByID != nil, supersededByID, nil, nil, doc.CreatedAt, doc.UpdatedAt)
		if err == nil {
			if _, err = tx.ExecContext(ctx, `
                DELETE FROM memory_relations
                WHERE user_id = $1 AND relation_type = 'supersedes' AND dst_node_id = $2
            `, doc.UserID, doc.NodeID); err != nil {
				return err
			}

			if supersededByIDValue != "" {
				var supersedingNodeID string
				switch scanErr := tx.QueryRowContext(ctx, `
                    SELECT node_id FROM facts WHERE user_id = $1 AND id = $2
                `, doc.UserID, supersededByIDValue).Scan(&supersedingNodeID); scanErr {
				case nil:
					if _, err = tx.ExecContext(ctx, `
                        INSERT INTO memory_relations (id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at, updated_at)
                        VALUES ($1,$2,$3,$4,'supersedes',1,'fact_supersession',$5,$6,$6)
                        ON CONFLICT (user_id, src_node_id, dst_node_id, relation_type) DO UPDATE SET
                            weight = EXCLUDED.weight,
                            source = EXCLUDED.source,
                            metadata = EXCLUDED.metadata,
                            updated_at = EXCLUDED.updated_at
					`, uuid.NewString(), doc.UserID, supersedingNodeID, doc.NodeID, marshalJSON(map[string]any{
						"superseded_fact_id":  doc.ID,
						"superseding_fact_id": supersededByIDValue,
					}), doc.UpdatedAt); err != nil {
						return err
					}
				case sqlpkg.ErrNoRows:
				default:
					return scanErr
				}
			}

			rows, queryErr := tx.QueryContext(ctx, `
                SELECT id, node_id
                FROM facts
                WHERE user_id = $1 AND superseded_by_id = $2 AND id <> $3
            `, doc.UserID, doc.ID, doc.ID)
			if queryErr != nil {
				return queryErr
			}
			defer rows.Close()
			for rows.Next() {
				var supersededFactID string
				var supersededNodeID string
				if err = rows.Scan(&supersededFactID, &supersededNodeID); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `
                    INSERT INTO memory_relations (id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at, updated_at)
                    VALUES ($1,$2,$3,$4,'supersedes',1,'fact_supersession',$5,$6,$6)
                    ON CONFLICT (user_id, src_node_id, dst_node_id, relation_type) DO UPDATE SET
                        weight = EXCLUDED.weight,
                        source = EXCLUDED.source,
                        metadata = EXCLUDED.metadata,
                        updated_at = EXCLUDED.updated_at
                `, uuid.NewString(), doc.UserID, doc.NodeID, supersededNodeID, marshalJSON(map[string]any{
					"superseded_fact_id":  supersededFactID,
					"superseding_fact_id": doc.ID,
				}), doc.UpdatedAt); err != nil {
					return err
				}
			}
			if err = rows.Err(); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported memory type: %s", doc.Type)
	}
	if err != nil {
		return err
	}
	if err = syncTemporalRelationsTx(tx, doc.UserID, doc.NodeID, doc.ActivityAt, doc.TimeTier, doc.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PostgresMemoryRepository) DeleteDocument(ctx context.Context, userID, id string, kind domain.MemoryType) error {
	var nodeID string
	if err := r.db.QueryRowContext(ctx, `
        SELECT id
        FROM memory_nodes
        WHERE user_id = $1 AND entity_id = $2 AND node_type = $3
    `, userID, id, string(kind)).Scan(&nodeID); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return nil
		}
		return err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	if _, err := tx.ExecContext(ctx, `
        DELETE FROM memory_relations
        WHERE user_id = $1 AND (src_node_id = $2 OR dst_node_id = $2)
    `, userID, nodeID); err != nil {
		return err
	}

	switch kind {
	case domain.MemoryTypeSummary:
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_keywords WHERE user_id = $1 AND summary_id = $2`, userID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_summaries WHERE user_id = $1 AND id = $2`, userID, id); err != nil {
			return err
		}
	case domain.MemoryTypeSegment:
		if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_segments WHERE user_id = $1 AND id = $2`, userID, id); err != nil {
			return err
		}
	case domain.MemoryTypeImpression:
		if _, err := tx.ExecContext(ctx, `DELETE FROM impressions WHERE user_id = $1 AND id = $2`, userID, id); err != nil {
			return err
		}
	case domain.MemoryTypeFact:
		if _, err := tx.ExecContext(ctx, `DELETE FROM facts WHERE user_id = $1 AND id = $2`, userID, id); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported memory type: %s", kind)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_nodes WHERE user_id = $1 AND id = $2`, userID, nodeID); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *PostgresMemoryRepository) DeleteSegmentsByConversation(ctx context.Context, userID, conversationID string, keepIDs []string) ([]string, error) {
	if userID == "" || conversationID == "" {
		return nil, nil
	}

	keep := make(map[string]struct{}, len(keepIDs))
	for _, id := range keepIDs {
		keep[id] = struct{}{}
	}

	rows, err := r.db.QueryContext(ctx, `
        SELECT id, entity_id
        FROM memory_nodes
        WHERE user_id = $1 AND node_type = $2 AND entity_id LIKE $3 ESCAPE '\'
    `, userID, string(domain.MemoryTypeSegment), segmentEntityPrefixPattern(conversationID))
	if err != nil {
		return nil, err
	}

	nodeIDs := make([]string, 0)
	for rows.Next() {
		var nodeID string
		var segmentID string
		if err := rows.Scan(&nodeID, &segmentID); err != nil {
			return nil, err
		}
		if _, ok := keep[segmentID]; ok {
			continue
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	if err := deleteConversationSegmentsByConversationTx(ctx, tx, userID, conversationID, keepIDs); err != nil {
		return nil, err
	}
	if len(nodeIDs) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err := deleteRelationsByNodeIDsTx(ctx, tx, userID, nodeIDs); err != nil {
		return nil, err
	}
	if err := deleteMemoryNodesByIDsTx(ctx, tx, userID, nodeIDs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	sort.Strings(nodeIDs)
	return nodeIDs, nil
}

func (r *PostgresMemoryRepository) GetDocument(ctx context.Context, userID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	docs, err := r.listByType(ctx, userID, kind, 1000)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	for _, doc := range docs {
		if doc.ID == id {
			return doc, nil
		}
	}
	return domain.MemoryDocument{}, errors.New("memory document not found")
}

func (r *PostgresMemoryRepository) ListDocuments(ctx context.Context, userID string, memoryTypes []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	types := memoryTypes
	if len(types) == 0 {
		types = []domain.MemoryType{
			domain.MemoryTypeSummary,
			domain.MemoryTypeSegment,
			domain.MemoryTypeImpression,
			domain.MemoryTypeFact,
			domain.MemoryTypeKeyMemory,
		}
	}
	out := make([]domain.MemoryDocument, 0)
	for _, kind := range types {
		docs, err := r.listByType(ctx, userID, kind, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, docs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *PostgresMemoryRepository) ListDocumentsByNodeIDs(ctx context.Context, userID string, nodeIDs []string) ([]domain.MemoryDocument, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	allDocs, err := r.ListDocuments(ctx, userID, []domain.MemoryType{
		domain.MemoryTypeSummary,
		domain.MemoryTypeSegment,
		domain.MemoryTypeImpression,
		domain.MemoryTypeFact,
		domain.MemoryTypeKeyMemory,
	}, 1000)
	if err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{}
	for _, nodeID := range nodeIDs {
		allowed[nodeID] = struct{}{}
	}
	out := make([]domain.MemoryDocument, 0, len(nodeIDs))
	for _, doc := range allDocs {
		if _, ok := allowed[doc.NodeID]; ok {
			out = append(out, doc)
		}
	}
	return out, nil
}

func (r *PostgresMemoryRepository) ListDocumentsByConversation(ctx context.Context, userID, conversationID string) ([]domain.MemoryDocument, error) {
	docs, err := r.ListDocuments(ctx, userID, []domain.MemoryType{domain.MemoryTypeSummary, domain.MemoryTypeImpression, domain.MemoryTypeFact}, 500)
	if err != nil {
		return nil, err
	}
	out := make([]domain.MemoryDocument, 0)
	for _, doc := range docs {
		if doc.ConversationID == conversationID {
			out = append(out, doc)
		}
	}
	return out, nil
}

func (r *PostgresMemoryRepository) SearchKeywordMatches(ctx context.Context, userID, query string, limit int) ([]domain.KeywordMatch, error) {
	terms := normalizeKeywordTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	args := make([]any, 0, len(terms)+2)
	args = append(args, userID)
	placeholders := make([]string, 0, len(terms))
	for idx, term := range terms {
		placeholders = append(placeholders, fmt.Sprintf("$%d", idx+2))
		args = append(args, term)
	}
	args = append(args, limit)

	querySQL := fmt.Sprintf(`
        SELECT
            s.node_id,
            COALESCE(json_agg(mk.keyword ORDER BY mk.weight DESC), '[]'::json),
            COALESCE(SUM(mk.weight), 0)
        FROM memory_keywords mk
        JOIN memory_summaries s
          ON s.id = mk.summary_id
         AND s.user_id = mk.user_id
        WHERE mk.user_id = $1
          AND mk.keyword IN (%s)
        GROUP BY s.node_id
        ORDER BY SUM(mk.weight) DESC, COUNT(*) DESC
        LIMIT $%d
    `, strings.Join(placeholders, ","), len(args))

	rows, err := r.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.KeywordMatch, 0)
	for rows.Next() {
		var item domain.KeywordMatch
		var keywordsJSON []byte
		if err := rows.Scan(&item.NodeID, &keywordsJSON, &item.TotalWeight); err != nil {
			return nil, err
		}
		if len(keywordsJSON) > 0 {
			if err := json.Unmarshal(keywordsJSON, &item.Keywords); err != nil {
				return nil, err
			}
		}
		item.Score = keywordMatchScore(item.TotalWeight, len(item.Keywords))
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PostgresMemoryRepository) UpdateDocument(ctx context.Context, userID, id string, kind domain.MemoryType, content string) (domain.MemoryDocument, error) {
	var query string
	switch kind {
	case domain.MemoryTypeSummary:
		query = `UPDATE memory_summaries SET summary = $1, updated_at = $2 WHERE id = $3 AND user_id = $4`
	case domain.MemoryTypeImpression:
		query = `UPDATE impressions SET description = $1, updated_at = $2 WHERE id = $3 AND user_id = $4`
	case domain.MemoryTypeFact:
		query = `UPDATE facts SET content = $1, updated_at = $2 WHERE id = $3 AND user_id = $4`
	default:
		return domain.MemoryDocument{}, fmt.Errorf("unsupported memory type: %s", kind)
	}
	if _, err := r.db.ExecContext(ctx, query, content, time.Now().UTC(), id, userID); err != nil {
		return domain.MemoryDocument{}, err
	}
	return r.GetDocument(ctx, userID, id, kind)
}

func (r *PostgresMemoryRepository) ListKeyMemories(ctx context.Context, userID string, limit int) ([]domain.KeyMemory, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
	        SELECT k.id, k.node_id, k.key, k.value, k.category, k.source, k.review_status, k.last_confirmed_at, k.created_at, k.updated_at,
	               COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1), COALESCE(n.activity_at, k.updated_at), COALESCE(n.time_tier, '')
	        FROM key_memories k
	        LEFT JOIN memory_nodes n ON n.id = k.node_id
	        WHERE k.user_id = $1 AND k.deleted_at IS NULL
	        ORDER BY k.updated_at DESC
	        LIMIT $2
	    `, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.KeyMemory, 0)
	for rows.Next() {
		var item domain.KeyMemory
		var nodeID string
		var lastConfirmedAt sqlpkg.NullTime
		var timeTier string
		item.UserID = userID
		if err := rows.Scan(&item.ID, &nodeID, &item.Key, &item.Value, &item.Category, &item.Source, &item.ReviewStatus, &lastConfirmedAt, &item.CreatedAt, &item.UpdatedAt, &item.Importance, &item.DecayFactor, &item.ActivityAt, &timeTier); err != nil {
			return nil, err
		}
		item.Metadata = map[string]any{"node_id": nodeID}
		item.TimeTier = domain.ParseTimeTier(timeTier)
		item.EffectiveScore = item.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, item.ActivityAt, item.DecayFactor, item.Importance, time.Now().UTC())
		if lastConfirmedAt.Valid {
			item.LastConfirmedAt = &lastConfirmedAt.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PostgresMemoryRepository) SearchKeyMemories(ctx context.Context, input domain.KeyMemoryQuery) ([]domain.KeyMemory, error) {
	items, err := r.ListKeyMemories(ctx, input.UserID, max(input.TopK*10, 200))
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	now := time.Now().UTC()
	filtered := make([]domain.KeyMemory, 0, len(items))
	for _, item := range items {
		if !matchesTemporalKeyMemory(item, input) {
			continue
		}
		item.Importance = domain.ClampImportance(item.Importance)
		item.DecayFactor = domain.ClampDecayFactor(item.DecayFactor)
		item.ActivityAt = domain.DeriveActivityAt(item.ActivityAt, item.UpdatedAt, item.CreatedAt)
		item.TimeTier = domain.ClassifyTimeTier(item.ActivityAt, now)
		score := item.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, item.ActivityAt, item.DecayFactor, item.Importance, now)
		if query != "" {
			content := strings.ToLower(item.Key + " " + item.Value + " " + item.Category)
			switch {
			case strings.Contains(content, query):
			case strings.Contains(content, firstToken(query)):
				score *= 0.8
			default:
				score *= 0.5
			}
		}
		item.EffectiveScore = score
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].EffectiveScore == filtered[j].EffectiveScore {
			return filtered[i].ActivityAt.After(filtered[j].ActivityAt)
		}
		return filtered[i].EffectiveScore > filtered[j].EffectiveScore
	})
	if input.TopK > 0 && len(filtered) > input.TopK {
		filtered = filtered[:input.TopK]
	}
	return filtered, nil
}

func (r *PostgresMemoryRepository) UpsertKeyMemory(ctx context.Context, km domain.KeyMemory) error {
	if km.ID == "" {
		km.ID = uuid.NewString()
	}
	nodeID := stringValue(km.Metadata, "node_id")
	if nodeID == "" {
		nodeID = uuid.NewString()
	}
	if km.Importance == 0 {
		km.Importance = 1
	}
	if km.DecayFactor == 0 {
		km.DecayFactor = 1
	}
	km.Importance = domain.ClampImportance(km.Importance)
	km.DecayFactor = domain.ClampDecayFactor(km.DecayFactor)
	km.ActivityAt = domain.DeriveActivityAt(km.ActivityAt, km.UpdatedAt, km.CreatedAt)
	if km.TimeTier == "" {
		km.TimeTier = domain.ClassifyTimeTier(km.ActivityAt, time.Now().UTC())
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
	        INSERT INTO memory_nodes (id, user_id, node_type, entity_id, importance, decay_factor, activity_at, time_tier, embedding_status, created_at, updated_at)
	        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	        ON CONFLICT (id) DO UPDATE SET
	            importance = EXCLUDED.importance,
	            decay_factor = EXCLUDED.decay_factor,
	            activity_at = EXCLUDED.activity_at,
	            time_tier = EXCLUDED.time_tier,
	            updated_at = EXCLUDED.updated_at
	    `, nodeID, km.UserID, string(domain.MemoryTypeKeyMemory), km.ID, km.Importance, km.DecayFactor, km.ActivityAt, string(km.TimeTier), "pending", now, now)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
        INSERT INTO key_memories (id, user_id, node_id, key, value, category, source, review_status, last_confirmed_at, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	        ON CONFLICT (user_id, key) DO UPDATE SET
	            value = EXCLUDED.value,
	            category = EXCLUDED.category,
	            source = EXCLUDED.source,
	            review_status = EXCLUDED.review_status,
	            last_confirmed_at = EXCLUDED.last_confirmed_at,
	            deleted_at = NULL,
	            updated_at = EXCLUDED.updated_at
	    `, km.ID, km.UserID, nodeID, km.Key, km.Value, km.Category, km.Source, defaultString(km.ReviewStatus, "pending"), km.LastConfirmedAt, now, now)
	if err != nil {
		return err
	}
	if err = syncTemporalRelationsTx(tx, km.UserID, nodeID, km.ActivityAt, km.TimeTier, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PostgresMemoryRepository) DeleteKeyMemory(ctx context.Context, userID, key string) error {
	_, err := r.db.ExecContext(ctx, `
	        WITH target AS (
	            SELECT node_id
	            FROM key_memories
	            WHERE user_id = $2 AND key = $3
	        ), deleted AS (
	            UPDATE key_memories
	            SET deleted_at = $1, updated_at = $1
	            WHERE user_id = $2 AND key = $3
	        )
	        DELETE FROM memory_relations
	        WHERE user_id = $2
	          AND (
	              src_node_id IN (SELECT node_id FROM target)
	              OR dst_node_id IN (SELECT node_id FROM target)
	          )
	    `, time.Now().UTC(), userID, key)
	return err
}

func (r *PostgresMemoryRepository) LinkDocuments(ctx context.Context, relation domain.MemoryRelation) error {
	if relation.ID == "" {
		relation.ID = uuid.NewString()
	}
	if relation.CreatedAt.IsZero() {
		relation.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO memory_relations (id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
        ON CONFLICT (user_id, src_node_id, dst_node_id, relation_type) DO UPDATE SET
            weight = EXCLUDED.weight,
            source = EXCLUDED.source,
            metadata = EXCLUDED.metadata,
            updated_at = EXCLUDED.updated_at
	`, relation.ID, relation.UserID, relation.SrcNodeID, relation.DstNodeID, relation.RelationType, relation.Weight, relation.Source, marshalJSON(relation.Metadata), relation.CreatedAt)
	return err
}

func (r *PostgresMemoryRepository) DeleteRelationsByTarget(ctx context.Context, userID, targetNodeID, relationType string) error {
	_, err := r.db.ExecContext(ctx, `
        DELETE FROM memory_relations
        WHERE user_id = $1 AND dst_node_id = $2 AND relation_type = $3
    `, userID, targetNodeID, relationType)
	return err
}

func (r *PostgresMemoryRepository) ListRelations(ctx context.Context, userID string, nodeIDs []string, depth int) ([]domain.MemoryRelation, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	if depth <= 0 {
		depth = 1
	}

	seenNodes := map[string]struct{}{}
	for _, nodeID := range nodeIDs {
		seenNodes[nodeID] = struct{}{}
	}
	seenRelations := map[string]struct{}{}
	collected := make([]domain.MemoryRelation, 0)
	frontier := append([]string{}, nodeIDs...)
	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		query, args := relationQuery(userID, frontier)
		rows, err := r.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		nextFrontier := make([]string, 0)
		for rows.Next() {
			var item domain.MemoryRelation
			var metadataBytes []byte
			if err := rows.Scan(&item.ID, &item.UserID, &item.SrcNodeID, &item.DstNodeID, &item.RelationType, &item.Weight, &item.Source, &metadataBytes, &item.CreatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			if _, ok := seenRelations[item.ID]; ok {
				continue
			}
			_ = json.Unmarshal(metadataBytes, &item.Metadata)
			seenRelations[item.ID] = struct{}{}
			collected = append(collected, item)
			if _, ok := seenNodes[item.SrcNodeID]; !ok {
				seenNodes[item.SrcNodeID] = struct{}{}
				nextFrontier = append(nextFrontier, item.SrcNodeID)
			}
			if _, ok := seenNodes[item.DstNodeID]; !ok {
				seenNodes[item.DstNodeID] = struct{}{}
				nextFrontier = append(nextFrontier, item.DstNodeID)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		frontier = nextFrontier
	}
	return collected, nil
}

func (r *PostgresMemoryRepository) listByType(ctx context.Context, userID string, kind domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	if limit <= 0 {
		limit = 100
	}
	var query string
	switch kind {
	case domain.MemoryTypeSummary:
		query = `
	            SELECT s.id, s.node_id, s.conversation_id, s.summary, COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1), COALESCE(n.activity_at, s.updated_at), COALESCE(n.time_tier, ''), s.created_at, s.updated_at,
	                   json_build_object(
	                       'topic_label', s.topic_label,
	                       'keywords', COALESCE((
                           SELECT json_agg(k.keyword ORDER BY k.weight DESC)
                           FROM memory_keywords k
                           WHERE k.summary_id = s.id AND k.user_id = s.user_id
                       ), '[]'::json)
                   )
            FROM memory_summaries s
            LEFT JOIN memory_nodes n ON n.id = s.node_id
            WHERE s.user_id = $1
            ORDER BY s.updated_at DESC
            LIMIT $2
        `
	case domain.MemoryTypeSegment:
		query = `
	            SELECT cs.id, n.id AS node_id, cs.conversation_id, cs.content, COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1), COALESCE(n.activity_at, cs.updated_at), COALESCE(n.time_tier, ''), cs.created_at, cs.updated_at,
	                   jsonb_build_object(
	                       'segment_index', cs.segment_index,
	                       'start_sequence', cs.start_sequence,
	                       'end_sequence', cs.end_sequence,
	                       'message_count', cs.message_count,
	                       'overlap_from_sequence', cs.overlap_from_sequence,
	                       'overlap_message_count', cs.overlap_message_count,
	                       'content_hash', cs.content_hash
	                   ) || COALESCE(cs.metadata, '{}'::jsonb)
            FROM conversation_segments cs
            JOIN memory_nodes n
              ON n.user_id = cs.user_id
             AND n.entity_id = cs.id
             AND n.node_type = 'segment'
            WHERE cs.user_id = $1
            ORDER BY cs.updated_at DESC, cs.segment_index DESC
            LIMIT $2
        `
	case domain.MemoryTypeImpression:
		query = `
	            SELECT i.id, i.node_id, i.conversation_id, i.description, COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1), COALESCE(n.activity_at, i.updated_at), COALESCE(n.time_tier, ''), i.created_at, i.updated_at,
	                   json_build_object('subject', i.subject, 'subject_type', i.subject_type, 'evaluation', i.evaluation, 'sentiment', i.sentiment, 'familiarity', i.familiarity)
            FROM impressions i
            LEFT JOIN memory_nodes n ON n.id = i.node_id
            WHERE i.user_id = $1
            ORDER BY i.updated_at DESC
            LIMIT $2
        `
	case domain.MemoryTypeFact:
		query = `
	            SELECT f.id, f.node_id, f.conversation_id, f.content, COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1), COALESCE(n.activity_at, f.updated_at), COALESCE(n.time_tier, ''), f.created_at, f.updated_at,
	                   json_build_object('category', f.category, 'confidence', f.confidence, 'valid_from', f.valid_from, 'valid_to', f.valid_to, 'superseded_by_id', f.superseded_by_id)
            FROM facts f
            LEFT JOIN memory_nodes n ON n.id = f.node_id
            WHERE f.user_id = $1
            ORDER BY f.updated_at DESC
            LIMIT $2
        `
	case domain.MemoryTypeKeyMemory:
		query = `
	            SELECT k.id, k.node_id, '' AS conversation_id, CONCAT(k.key, ': ', k.value), COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1), COALESCE(n.activity_at, k.updated_at), COALESCE(n.time_tier, ''), k.created_at, k.updated_at,
	                   json_build_object('key', k.key, 'value', k.value, 'category', k.category, 'source', k.source, 'review_status', k.review_status, 'last_confirmed_at', k.last_confirmed_at)
            FROM key_memories k
            LEFT JOIN memory_nodes n ON n.id = k.node_id
            WHERE k.user_id = $1 AND k.deleted_at IS NULL
            ORDER BY k.updated_at DESC
            LIMIT $2
        `
	default:
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.MemoryDocument, 0)
	for rows.Next() {
		var item domain.MemoryDocument
		var metadataBytes []byte
		var timeTier string
		item.UserID = userID
		item.Type = kind
		if err := rows.Scan(&item.ID, &item.NodeID, &item.ConversationID, &item.Content, &item.Importance, &item.DecayFactor, &item.ActivityAt, &timeTier, &item.CreatedAt, &item.UpdatedAt, &metadataBytes); err != nil {
			return nil, err
		}
		if len(metadataBytes) > 0 {
			item.Metadata = map[string]any{}
			if err := json.Unmarshal(metadataBytes, &item.Metadata); err != nil {
				return nil, err
			}
		}
		item.TimeTier = domain.ParseTimeTier(timeTier)
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		item.Metadata["activity_at"] = item.ActivityAt.Format(time.RFC3339)
		item.Metadata["time_tier"] = item.TimeTier
		item.Metadata["temporal"] = temporalMetadata(item.UserID, item.ActivityAt, item.TimeTier)
		out = append(out, item)
	}
	return out, rows.Err()
}

func syncTemporalRelationsTx(tx *sqlpkg.Tx, userID, nodeID string, activityAt time.Time, tier domain.TimeTier, updatedAt time.Time) error {
	if tx == nil || userID == "" || nodeID == "" {
		return nil
	}
	activityAt = domain.DeriveActivityAt(activityAt, updatedAt, updatedAt)
	if tier == "" {
		tier = domain.ClassifyTimeTier(activityAt, time.Now().UTC())
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	if _, err := tx.Exec(`
	        DELETE FROM memory_relations
	        WHERE user_id = $1 AND src_node_id = $2 AND relation_type IN ('indexed_in_tier', 'occurred_on_day')
	    `, userID, nodeID); err != nil {
		return err
	}
	tierNodeID := temporalTierNodeID(userID, tier)
	dayNodeID := temporalDayNodeID(userID, activityAt)
	monthNodeID := temporalMonthNodeID(userID, activityAt)
	yearNodeID := temporalYearNodeID(userID, activityAt)
	if err := upsertTemporalNodeTx(tx, tierNodeID, userID, "time_tier", string(tier), activityAt, updatedAt); err != nil {
		return err
	}
	if err := upsertTemporalNodeTx(tx, dayNodeID, userID, "calendar_day", activityAt.UTC().Format("2006-01-02"), activityAt, updatedAt); err != nil {
		return err
	}
	if err := upsertTemporalNodeTx(tx, monthNodeID, userID, "calendar_month", activityAt.UTC().Format("2006-01"), activityAt, updatedAt); err != nil {
		return err
	}
	if err := upsertTemporalNodeTx(tx, yearNodeID, userID, "calendar_year", activityAt.UTC().Format("2006"), activityAt, updatedAt); err != nil {
		return err
	}
	for _, relation := range []domain.MemoryRelation{
		{UserID: userID, SrcNodeID: nodeID, DstNodeID: tierNodeID, RelationType: "indexed_in_tier", Weight: 1, Source: "temporal", CreatedAt: updatedAt},
		{UserID: userID, SrcNodeID: nodeID, DstNodeID: dayNodeID, RelationType: "occurred_on_day", Weight: 1, Source: "temporal", CreatedAt: updatedAt},
		{UserID: userID, SrcNodeID: dayNodeID, DstNodeID: monthNodeID, RelationType: "day_in_month", Weight: 1, Source: "temporal", CreatedAt: updatedAt},
		{UserID: userID, SrcNodeID: monthNodeID, DstNodeID: yearNodeID, RelationType: "month_in_year", Weight: 1, Source: "temporal", CreatedAt: updatedAt},
	} {
		if _, err := tx.Exec(`
	            INSERT INTO memory_relations (id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at, updated_at)
	            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
	            ON CONFLICT (user_id, src_node_id, dst_node_id, relation_type) DO UPDATE SET
	                weight = EXCLUDED.weight,
	                source = EXCLUDED.source,
	                metadata = EXCLUDED.metadata,
	                updated_at = EXCLUDED.updated_at
	        `, uuid.NewString(), relation.UserID, relation.SrcNodeID, relation.DstNodeID, relation.RelationType, relation.Weight, relation.Source, marshalJSON(map[string]any{
			"activity_at": activityAt.Format(time.RFC3339),
		}), relation.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func deleteConversationSegmentsByConversationTx(ctx context.Context, tx *sqlpkg.Tx, userID, conversationID string, keepIDs []string) error {
	if tx == nil || userID == "" || conversationID == "" {
		return nil
	}
	args := []any{userID, conversationID}
	query := `
        DELETE FROM conversation_segments
        WHERE user_id = $1 AND conversation_id = $2
    `
	if len(keepIDs) > 0 {
		query += fmt.Sprintf(" AND id NOT IN (%s)", placeholders(3, len(keepIDs)))
		for _, id := range keepIDs {
			args = append(args, id)
		}
	}
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func deleteRelationsByNodeIDsTx(ctx context.Context, tx *sqlpkg.Tx, userID string, nodeIDs []string) error {
	if tx == nil || userID == "" || len(nodeIDs) == 0 {
		return nil
	}
	args := []any{userID}
	for _, nodeID := range nodeIDs {
		args = append(args, nodeID)
	}
	query := fmt.Sprintf(`
        DELETE FROM memory_relations
        WHERE user_id = $1 AND (src_node_id IN (%s) OR dst_node_id IN (%s))
    `, placeholders(2, len(nodeIDs)), placeholders(2, len(nodeIDs)))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func deleteMemoryNodesByIDsTx(ctx context.Context, tx *sqlpkg.Tx, userID string, nodeIDs []string) error {
	if tx == nil || userID == "" || len(nodeIDs) == 0 {
		return nil
	}
	args := []any{userID}
	for _, nodeID := range nodeIDs {
		args = append(args, nodeID)
	}
	query := fmt.Sprintf(`
        DELETE FROM memory_nodes
        WHERE user_id = $1 AND id IN (%s)
    `, placeholders(2, len(nodeIDs)))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func upsertTemporalNodeTx(tx *sqlpkg.Tx, nodeID, userID, nodeType, entityID string, activityAt, updatedAt time.Time) error {
	_, err := tx.Exec(`
	        INSERT INTO memory_nodes (id, user_id, node_type, entity_id, importance, decay_factor, activity_at, time_tier, embedding_status, created_at, updated_at)
	        VALUES ($1,$2,$3,$4,1,1,$5,'',$6,$7,$7)
	        ON CONFLICT (id) DO UPDATE SET
	            activity_at = EXCLUDED.activity_at,
	            updated_at = EXCLUDED.updated_at
	    `, nodeID, userID, nodeType, entityID, activityAt, "ready", updatedAt)
	return err
}

func temporalMetadata(userID string, activityAt time.Time, tier domain.TimeTier) map[string]any {
	activityAt = domain.DeriveActivityAt(activityAt, time.Time{}, time.Now().UTC())
	if tier == "" {
		tier = domain.ClassifyTimeTier(activityAt, time.Now().UTC())
	}
	return map[string]any{
		"time_tier":     tier,
		"tier_node_id":  temporalTierNodeID(userID, tier),
		"day_node_id":   temporalDayNodeID(userID, activityAt),
		"month_node_id": temporalMonthNodeID(userID, activityAt),
		"year_node_id":  temporalYearNodeID(userID, activityAt),
	}
}

type PostgresOutboxRepository struct {
	db *sqlpkg.DB
}

func NewPostgresOutboxRepository(db *sqlpkg.DB) *PostgresOutboxRepository {
	return &PostgresOutboxRepository{db: db}
}

func (r *PostgresOutboxRepository) Enqueue(ctx context.Context, event domain.OutboxEvent) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.AvailableAt.IsZero() {
		event.AvailableAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO outbox_events (id, user_id, event_type, aggregate_type, aggregate_id, payload, status, available_at, processed_at, retry_count, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
        ON CONFLICT (id) DO UPDATE SET
            payload = EXCLUDED.payload,
            status = EXCLUDED.status,
            available_at = EXCLUDED.available_at,
            processed_at = EXCLUDED.processed_at,
            retry_count = EXCLUDED.retry_count,
            updated_at = EXCLUDED.updated_at
    `, event.ID, event.UserID, event.EventType, event.AggregateType, event.AggregateID, marshalJSON(event.Payload), defaultString(event.Status, "pending"), event.AvailableAt, event.ProcessedAt, event.RetryCount, event.CreatedAt)
	return err
}

func (r *PostgresOutboxRepository) Pending(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, user_id, event_type, aggregate_type, aggregate_id, payload, status, available_at, processed_at, retry_count, created_at
        FROM outbox_events
        WHERE processed_at IS NULL AND available_at <= $1
        ORDER BY created_at ASC
        LIMIT $2
    `, time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.OutboxEvent, 0)
	for rows.Next() {
		var item domain.OutboxEvent
		var payloadBytes []byte
		var processedAt sqlpkg.NullTime
		if err := rows.Scan(&item.ID, &item.UserID, &item.EventType, &item.AggregateType, &item.AggregateID, &payloadBytes, &item.Status, &item.AvailableAt, &processedAt, &item.RetryCount, &item.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payloadBytes, &item.Payload)
		if processedAt.Valid {
			item.ProcessedAt = &processedAt.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PostgresOutboxRepository) MarkProcessed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE outbox_events SET processed_at = $1, status = 'processed', updated_at = $1 WHERE id = $2`, time.Now().UTC(), id)
	return err
}

func rollback(tx *sqlpkg.Tx) {
	_ = tx.Rollback()
}

func marshalJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func zeroTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value
}

func firstToolResult(items []domain.ToolResult) map[string]any {
	if len(items) == 0 {
		return map[string]any{}
	}
	if items[0].Output != nil {
		return items[0].Output
	}
	return map[string]any{
		"tool_call_id": items[0].ToolCallID,
		"name":         items[0].Name,
	}
}

type conversationMessagePlan struct {
	Stored     []domain.UnifiedMessage
	Insert     []domain.UnifiedMessage
	InsertFrom int
	DeleteFrom *int
	Mode       string
}

func planConversationMessages(existing, incoming []domain.UnifiedMessage) conversationMessagePlan {
	if len(existing) == 0 {
		return conversationMessagePlan{
			Stored:     append([]domain.UnifiedMessage{}, incoming...),
			Insert:     append([]domain.UnifiedMessage{}, incoming...),
			InsertFrom: 0,
			Mode:       "initial",
		}
	}
	if len(incoming) == 0 {
		return conversationMessagePlan{
			Stored: append([]domain.UnifiedMessage{}, existing...),
			Mode:   "noop",
		}
	}

	prefix := 0
	for prefix < len(existing) && prefix < len(incoming) && messagesEqual(existing[prefix], incoming[prefix]) {
		prefix++
	}

	switch {
	case prefix == len(existing) && len(incoming) >= len(existing):
		return conversationMessagePlan{
			Stored:     append(append([]domain.UnifiedMessage{}, existing...), incoming[prefix:]...),
			Insert:     append([]domain.UnifiedMessage{}, incoming[prefix:]...),
			InsertFrom: prefix,
			Mode:       modeForAppend(prefix, len(incoming)),
		}
	case prefix == len(incoming):
		return conversationMessagePlan{
			Stored: append([]domain.UnifiedMessage{}, existing...),
			Mode:   "partial_snapshot",
		}
	default:
		deleteFrom := prefix
		stored := append([]domain.UnifiedMessage{}, existing[:prefix]...)
		stored = append(stored, incoming[prefix:]...)
		return conversationMessagePlan{
			Stored:     stored,
			Insert:     append([]domain.UnifiedMessage{}, incoming[prefix:]...),
			InsertFrom: prefix,
			DeleteFrom: &deleteFrom,
			Mode:       "rewrite",
		}
	}
}

func modeForAppend(prefix, incomingLength int) string {
	if prefix == 0 && incomingLength > 0 {
		return "initial"
	}
	if prefix == incomingLength {
		return "noop"
	}
	return "append"
}

func messagesEqual(left, right domain.UnifiedMessage) bool {
	return string(messageFingerprint(left)) == string(messageFingerprint(right))
}

func messageFingerprint(message domain.UnifiedMessage) []byte {
	type comparableMessage struct {
		Role        domain.MessageRole    `json:"role"`
		Text        string                `json:"text,omitempty"`
		Blocks      []domain.ContentBlock `json:"blocks,omitempty"`
		ToolCalls   []domain.ToolCall     `json:"tool_calls,omitempty"`
		ToolResults []domain.ToolResult   `json:"tool_results,omitempty"`
		Metadata    map[string]any        `json:"metadata,omitempty"`
	}
	return marshalJSON(comparableMessage{
		Role:        message.Role,
		Text:        message.Text,
		Blocks:      message.Blocks,
		ToolCalls:   message.ToolCalls,
		ToolResults: message.ToolResults,
		Metadata:    message.Metadata,
	})
}

func stableMessageID(conversationID string, idx int, message domain.UnifiedMessage) string {
	return fmt.Sprintf("%s:%d:%s:%s", conversationID, idx, message.Role, uuid.NewSHA1(uuid.NameSpaceOID, messageFingerprint(message)).String())
}

func stableUsageID(conversationID string, metadata map[string]any) string {
	if requestID := valueOrString(metadata, "request_id"); requestID != "" {
		return conversationID + ":" + requestID
	}
	return conversationID + ":usage"
}

func responseModel(resp *domain.UnifiedResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Model
}

func nullableFinalizeStatus(value *domain.FinalizeStatus) any {
	if value == nil || *value == "" {
		return nil
	}
	return string(*value)
}

func nullableProcessingStatus(value *domain.ProcessingStatus) any {
	if value == nil || *value == "" {
		return nil
	}
	return string(*value)
}

func valueOrString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	return stringValue(metadata, key)
}

func stringValue(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func floatValue(metadata map[string]any, key string, fallback float64) float64 {
	if metadata == nil {
		return fallback
	}
	value, ok := metadata[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	default:
		return fallback
	}
}

func intValue(metadata map[string]any, key string, fallback int) int {
	if metadata == nil {
		return fallback
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return fallback
	}
}

func statusOrDefault(value string, fallback string) string {
	if value == "" || value == "%!v(<nil>)" || value == "<nil>" {
		return fallback
	}
	return value
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func stringSlice(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	value, ok := metadata[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := fmt.Sprintf("%v", item)
			if text != "" && text != "%!v(<nil>)" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func relationQuery(userID string, nodeIDs []string) (string, []any) {
	args := make([]any, 0, len(nodeIDs)*2+1)
	args = append(args, userID)
	placeholders := make([]string, 0, len(nodeIDs))
	for idx, nodeID := range nodeIDs {
		placeholders = append(placeholders, fmt.Sprintf("$%d", idx+2))
		args = append(args, nodeID)
	}
	inClause := strings.Join(placeholders, ",")
	query := fmt.Sprintf(`
        SELECT id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at
        FROM memory_relations
        WHERE user_id = $1 AND (src_node_id IN (%s) OR dst_node_id IN (%s))
        ORDER BY created_at DESC
    `, inClause, inClause)
	return query, args
}

func firstToken(query string) string {
	parts := strings.Fields(query)
	if len(parts) == 0 {
		return query
	}
	return parts[0]
}

func placeholders(start, count int) string {
	items := make([]string, 0, count)
	for idx := 0; idx < count; idx++ {
		items = append(items, fmt.Sprintf("$%d", start+idx))
	}
	return strings.Join(items, ",")
}

func segmentEntityPrefixPattern(conversationID string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(conversationID)
	return fmt.Sprintf("segment:%s:%%", escaped)
}
