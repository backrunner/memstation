package conversation

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	conversationservice "memstation/apps/server/internal/service/conversation"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"
	"memstation/apps/server/internal/store/sql/memorygeneration"

	"github.com/google/uuid"
)

type PostgresConversationRepository struct {
	db *sqlpkg.DB
}

func NewPostgresConversationRepository(db *sqlpkg.DB) *PostgresConversationRepository {
	return &PostgresConversationRepository{db: db}
}

func (r *PostgresConversationRepository) PersistExchange(ctx context.Context, conversation domain.ConversationRecord) error {
	var err error
	conversation, err = normalizeConversationScope(ctx, conversation)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)

	// Serialize creation as well as updates; SELECT FOR UPDATE alone cannot lock a missing row.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "conversation:"+conversation.ConversationID); err != nil {
		return err
	}
	var owner, workspace, agent string
	var revision int64
	var existingFinalize, existingSummary, existingAnalysis string
	var existingMetadata []byte
	err = tx.QueryRowContext(ctx, `SELECT user_id,workspace_id,agent_id,write_revision,finalize_status,summary_status,analysis_status,metadata FROM conversations WHERE id=$1`, conversation.ConversationID).Scan(&owner, &workspace, &agent, &revision, &existingFinalize, &existingSummary, &existingAnalysis, &existingMetadata)
	if err != nil && !errors.Is(err, sqlpkg.ErrNoRows) {
		return err
	}
	if owner != "" && (owner != conversation.UserID || workspace != conversation.WorkspaceID || agent != conversation.AgentID) {
		return domain.ErrAccessDenied
	}
	if conversation.ExpectedRevision != nil && revision != *conversation.ExpectedRevision {
		return domain.ErrAccessConflict
	}
	if owner != "" {
		var stored map[string]any
		if len(existingMetadata) > 0 {
			if err = json.Unmarshal(existingMetadata, &stored); err != nil {
				return err
			}
		}
		conversation.Metadata = mergeConversationWriteMetadata(stored, conversation.Metadata, domain.FinalizeStatus(existingFinalize))
		// A request prepared before finalization must not reset newer worker state.
		conversation.FinalizeStatus = domain.FinalizeStatus(existingFinalize)
		conversation.SummaryStatus = domain.ProcessingStatus(existingSummary)
		conversation.AnalysisStatus = domain.ProcessingStatus(existingAnalysis)
	}
	existingMessages, err := r.loadMessagesTx(ctx, tx, conversation.UserID, conversation.ConversationID)
	if err != nil {
		return err
	}
	conversation.Messages = conversationservice.PrepareMessagesForStorage(conversation.Messages)
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

	metadata := dbutilstore.MarshalJSON(conversation.Metadata)
	if _, err = tx.ExecContext(ctx, `
        INSERT INTO conversations (
            id, user_id, request_format, upstream_provider, mode, finalize_status,
            summary_status, analysis_status, metadata, created_at, updated_at, write_revision, workspace_id, agent_id
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1,$12,$13)
        ON CONFLICT (id) DO UPDATE SET
            write_revision = conversations.write_revision + 1,
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
		dbutilstore.ZeroTime(conversation.CreatedAt),
		dbutilstore.ZeroTime(conversation.UpdatedAt),
		conversation.WorkspaceID, conversation.AgentID,
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
                tool_calls, tool_result, provider_payload, sequence_num, created_at, updated_at, workspace_id
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
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
			dbutilstore.MarshalJSON(message.Blocks),
			message.Text,
			dbutilstore.MarshalJSON(message.ToolCalls),
			dbutilstore.MarshalJSON(domain.StoredToolResultPayload(message.ToolResults)),
			dbutilstore.MarshalJSON(message.Metadata),
			sequenceNum,
			dbutilstore.ZeroTime(conversation.CreatedAt),
			dbutilstore.ZeroTime(conversation.UpdatedAt),
			conversation.WorkspaceID,
		)
		if err != nil {
			return err
		}
	}

	if conversation.Usage.TotalTokens > 0 {
		usageID := stableUsageID(conversation.ConversationID, conversation.Metadata)
		_, err = tx.ExecContext(ctx, `
            INSERT INTO token_usages (
                id, user_id, conversation_id, provider, model, input_tokens, output_tokens, total_tokens, cost, created_at, updated_at, workspace_id
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
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
			dbutilstore.ZeroTime(conversation.CreatedAt),
			dbutilstore.ZeroTime(conversation.UpdatedAt),
			conversation.WorkspaceID,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *PostgresConversationRepository) GetConversation(ctx context.Context, userID, conversationID string) (domain.ConversationRecord, error) {
	return r.GetConversationInGeneration(ctx, userID, conversationID, "")
}

func (r *PostgresConversationRepository) GetConversationInGeneration(ctx context.Context, userID, conversationID, generationID string) (domain.ConversationRecord, error) {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return domain.ConversationRecord{}, err
	}
	row := r.db.QueryRowContext(ctx, `
        SELECT request_format, COALESCE(upstream_provider,''), mode, finalize_status, summary_status, analysis_status, metadata, created_at, updated_at, write_revision, workspace_id, agent_id,partition_id,context_mode
        FROM conversations
        WHERE id = $1 AND user_id = $2 AND workspace_id=$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND finalize_status<>'invalidated'
    `, conversationID, userID, workspace)

	var record domain.ConversationRecord
	var provider string
	var mode string
	var finalizeStatus string
	var summaryStatus string
	var analysisStatus string
	var metadataBytes []byte
	record.ConversationID = conversationID
	record.UserID = userID
	if err := row.Scan(&record.RequestFormat, &provider, &mode, &finalizeStatus, &summaryStatus, &analysisStatus, &metadataBytes, &record.CreatedAt, &record.UpdatedAt, &record.Revision, &record.WorkspaceID, &record.AgentID, &record.PartitionID, &record.ContextMode); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ConversationRecord{}, domain.ErrConversationNotFound
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
	if record.ContextMode == "events" {
		messages, err = r.loadIngestMessages(ctx, record)
		if err != nil {
			return domain.ConversationRecord{}, err
		}
	}
	record.Messages = messages
	resolvedGenerationID := strings.TrimSpace(generationID)
	if resolvedGenerationID == "" {
		resolvedGenerationID = conversationSegmentGenerationForSQL(record.WorkspaceID, record.Metadata)
	}
	segments, err := r.loadSegments(ctx, userID, conversationID, resolvedGenerationID)
	if err != nil {
		return domain.ConversationRecord{}, err
	}
	record.Segments = segments
	record.Metadata = recordutilstore.CloneMetadata(record.Metadata)
	if record.Metadata == nil {
		record.Metadata = map[string]any{}
	}
	record.Metadata["memory_generation_id"] = resolvedGenerationID

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
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, request_format, COALESCE(upstream_provider,''), mode, finalize_status, summary_status, analysis_status, metadata, created_at, updated_at, workspace_id, agent_id, write_revision
        FROM conversations
        WHERE user_id = $1 AND workspace_id=$2 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` AND finalize_status<>'invalidated'
        ORDER BY updated_at DESC
    `, userID, workspace)
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
		item.WorkspaceID = security.WorkspaceID(ctx, userID)
		if err := rows.Scan(&item.ConversationID, &item.RequestFormat, &provider, &mode, &finalizeStatus, &summaryStatus, &analysisStatus, &metadataBytes, &item.CreatedAt, &item.UpdatedAt, &item.WorkspaceID, &item.AgentID, &item.Revision); err != nil {
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
        SELECT id, user_id, request_format, COALESCE(upstream_provider,''), mode, finalize_status, summary_status, analysis_status, metadata, created_at, updated_at, workspace_id, agent_id, write_revision
        FROM conversations
        WHERE finalize_status = 'active' AND updated_at < $1 AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+`
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
		if err := rows.Scan(&item.ConversationID, &item.UserID, &item.RequestFormat, &provider, &mode, &finalizeStatus, &summaryStatus, &analysisStatus, &metadataBytes, &item.CreatedAt, &item.UpdatedAt, &item.WorkspaceID, &item.AgentID, &item.Revision); err != nil {
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
	conversation, err := r.GetConversation(ctx, userID, conversationID)
	if err != nil {
		return err
	}
	for _, part := range segments {
		if part.UserID != userID || part.ConversationID != conversationID {
			return domain.ErrAccessDenied
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)

	targetGenerationID := recordutilstore.CurrentGenerationIDForSQL(security.WorkspaceID(ctx, userID))
	if len(segments) > 0 && strings.TrimSpace(segments[0].GenerationID) != "" {
		targetGenerationID = strings.TrimSpace(segments[0].GenerationID)
	}
	writable, err := memorygeneration.LockWritableGeneration(ctx, tx, userID, conversation.WorkspaceID, targetGenerationID)
	if err != nil || !writable {
		return err
	}
	for idx := range segments {
		if strings.TrimSpace(segments[idx].GenerationID) == "" {
			segments[idx].GenerationID = targetGenerationID
		}
		if segments[idx].GenerationID != targetGenerationID {
			return domain.ErrAccessDenied
		}
		if segments[idx].Metadata == nil {
			segments[idx].Metadata = map[string]any{}
		}
		segments[idx].Metadata["generation_id"] = segments[idx].GenerationID
	}

	if _, err = tx.ExecContext(ctx, `
        DELETE FROM conversation_segments
        WHERE user_id = $1 AND conversation_id = $2
    `+recordutilstore.GenerationFilterClause("generation_id", 3, userID, targetGenerationID, false), recordutilstore.GenerationFilterArgs(userID, targetGenerationID, userID, conversationID)...); err != nil {
		return err
	}
	for _, segment := range segments {
		if _, err = tx.ExecContext(ctx, `
            INSERT INTO conversation_segments (
                id, generation_id, user_id, conversation_id, segment_index, start_sequence, end_sequence,
                message_count, overlap_from_sequence, overlap_message_count, content, content_hash,
                metadata, created_at, updated_at
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
        `, segment.ID, segment.GenerationID, segment.UserID, segment.ConversationID, segment.SegmentIndex, segment.StartSequence,
			segment.EndSequence, segment.MessageCount, segment.OverlapFromSequence, segment.OverlapMessageCount,
			segment.Content, segment.ContentHash, dbutilstore.MarshalJSON(segment.Metadata), dbutilstore.ZeroTime(segment.CreatedAt),
			dbutilstore.ZeroTime(segment.UpdatedAt)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `
        UPDATE conversations
        SET metadata = COALESCE(metadata, '{}'::jsonb) || $1::jsonb,
            updated_at = $2
        WHERE id = $3 AND user_id = $4
    `, string(dbutilstore.MarshalJSON(map[string]any{"memory_generation_id": targetGenerationID})), time.Now().UTC(), conversationID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PostgresConversationRepository) FinalizeConversation(ctx context.Context, input domain.ConversationFinalizeInput) error {
	return r.UpdateConversationState(ctx, domain.ConversationStateUpdate{UserID: input.UserID, ConversationID: input.ConversationID, FinalizeStatus: &input.Status, Metadata: map[string]any{"finalize_reason": input.Reason}})
}

func (r *PostgresConversationRepository) UpdateConversationState(ctx context.Context, input domain.ConversationStateUpdate) error {
	c, err := r.GetConversation(ctx, input.UserID, input.ConversationID)
	if err != nil {
		return err
	}
	if err = authorizeConversationWriter(ctx, c); err != nil {
		return err
	}
	input.Metadata = conversationScopeMetadata(c, input.Metadata)
	metadata := dbutilstore.MarshalJSON(input.Metadata)
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}
	updatedAt := input.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err = r.db.ExecContext(ctx, `
        UPDATE conversations
        SET finalize_status = COALESCE($1, finalize_status),
            summary_status = COALESCE($2, summary_status),
            analysis_status = COALESCE($3, analysis_status),
            metadata = COALESCE(metadata, '{}'::jsonb) || $4::jsonb,
            updated_at = $5
        WHERE id = $6 AND user_id = $7 AND workspace_id=$8
    `, nullableFinalizeStatus(input.FinalizeStatus), nullableProcessingStatus(input.SummaryStatus), nullableProcessingStatus(input.AnalysisStatus), string(metadata), updatedAt, input.ConversationID, input.UserID, c.WorkspaceID)
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
		if toolResult, ok := domain.DecodeStoredToolResult(toolResultBytes); ok {
			item.ToolResults = []domain.ToolResult{toolResult}
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
		if toolResult, ok := domain.DecodeStoredToolResult(toolResultBytes); ok {
			item.ToolResults = []domain.ToolResult{toolResult}
		}
		_ = json.Unmarshal(providerPayloadBytes, &item.Metadata)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PostgresConversationRepository) loadSegments(ctx context.Context, userID, conversationID, generationID string) ([]domain.ConversationSegment, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, generation_id, segment_index, start_sequence, end_sequence, message_count,
               overlap_from_sequence, overlap_message_count, content, content_hash,
               metadata, created_at, updated_at
        FROM conversation_segments
        WHERE user_id = $1 AND conversation_id = $2
    `+recordutilstore.GenerationFilterClause("generation_id", 3, userID, generationID, false)+`
        ORDER BY segment_index ASC
    `, recordutilstore.GenerationFilterArgs(userID, generationID, userID, conversationID)...)
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
		if err := rows.Scan(&segment.ID, &segment.GenerationID, &segment.SegmentIndex, &segment.StartSequence, &segment.EndSequence,
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
    `, event.ID, event.UserID, event.EventType, event.AggregateType, event.AggregateID, dbutilstore.MarshalJSON(event.Payload), dbutilstore.DefaultString(event.Status, "pending"), event.AvailableAt, event.ProcessedAt, event.RetryCount, event.CreatedAt)
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
	return dbutilstore.MarshalJSON(comparableMessage{
		Role:        message.Role,
		Text:        message.Text,
		Blocks:      message.Blocks,
		ToolCalls:   message.ToolCalls,
		ToolResults: message.ToolResults,
		Metadata:    comparableMessageMetadata(message.Metadata),
	})
}

func comparableMessageMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		if key == conversationservice.MemoryPromptTextMetadataKey {
			continue
		}
		cloned[key] = value
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
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
	return dbutilstore.StringValue(metadata, key)
}

func MetadataHasKey(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	_, ok := metadata[key]
	return ok
}

func statusOrDefault(value string, fallback string) string {
	if value == "" || value == "%!v(<nil>)" || value == "<nil>" {
		return fallback
	}
	return value
}

func conversationSegmentGenerationForSQL(workspaceID string, metadata map[string]any) string {
	if generationID := strings.TrimSpace(dbutilstore.StringValue(metadata, "memory_generation_id")); generationID != "" {
		return generationID
	}
	return recordutilstore.CurrentGenerationIDForSQL(workspaceID)
}

func ShouldKeepExistingSnapshotSQL(existingMetadata, incomingMetadata map[string]any) bool {
	incomingVersion := SourceSnapshotVersionSQL(incomingMetadata)
	if incomingVersion <= 0 {
		return false
	}
	return SourceSnapshotVersionSQL(existingMetadata) > incomingVersion
}

func SourceSnapshotVersionSQL(metadata map[string]any) int {
	if len(metadata) == 0 {
		return 0
	}
	switch typed := metadata["source_snapshot_version"].(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		value, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return value
		}
	}
	return 0
}
