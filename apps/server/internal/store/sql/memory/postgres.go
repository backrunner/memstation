package memory

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	ingestionstore "memstation/apps/server/internal/store/sql/ingestion"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"
	"memstation/apps/server/internal/store/sql/memorygeneration"

	"github.com/google/uuid"
)

const (
	memoryKeywordInsertBatchSize  = 128
	memoryEvidenceInsertBatchSize = 256
)

type PostgresMemoryRepository struct {
	db *sqlpkg.DB
}

func NewPostgresMemoryRepository(db *sqlpkg.DB) *PostgresMemoryRepository {
	return &PostgresMemoryRepository{db: db}
}

func (r *PostgresMemoryRepository) Search(ctx context.Context, input domain.SearchInput) (domain.SearchResult, error) {
	if _, err := security.DataScope(ctx, input.UserID, input.WorkspaceID); err != nil {
		return domain.SearchResult{}, err
	}
	input.WorkspaceID = security.WorkspaceID(ctx, input.UserID)
	docs, err := r.ListDocumentsInGeneration(ctx, input.UserID, input.GenerationID, input.MemoryTypes, 200)
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
	return r.StoreDocumentTx(ctx, doc, nil)
}

func (r *PostgresMemoryRepository) StoreDocumentTx(ctx context.Context, doc domain.MemoryDocument, external *sqlpkg.Tx) error {
	if _, err := security.DataScope(ctx, doc.UserID, doc.WorkspaceID); err != nil {
		return err
	}
	doc.WorkspaceID = security.WorkspaceID(ctx, doc.UserID)
	if err := scopestore.PrepareDocumentAccess(ctx, &doc); err != nil {
		return err
	}
	if doc.Type != domain.MemoryTypeKeyMemory && strings.TrimSpace(doc.GenerationID) == "" {
		doc.GenerationID = recordutilstore.CurrentGenerationIDForSQL(security.WorkspaceID(ctx, doc.UserID))
	}
	hadExplicitID := strings.TrimSpace(doc.ID) != ""
	hadExplicitNodeID := strings.TrimSpace(doc.NodeID) != ""
	if doc.ID == "" {
		doc.ID = uuid.NewString()
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
	doc.Metadata = recordutilstore.CloneMetadata(doc.Metadata)
	if doc.Type == domain.MemoryTypeFact {
		doc = ensureFactLineageMetadata(doc)
		if groupID := factLineageGroupIDFromMetadata(doc.Metadata); groupID != "" {
			if !hadExplicitID {
				doc.ID = lineageRevisionID(doc)
			}
			if !hadExplicitNodeID {
				doc.NodeID = "node:" + doc.ID
			}
		}
	}
	if doc.NodeID == "" {
		doc.NodeID = defaultMemoryNodeID(doc.UserID, doc.Type, doc.ID)
	}
	doc.Metadata["activity_at"] = doc.ActivityAt.Format(time.RFC3339)
	doc.Metadata["time_tier"] = doc.TimeTier
	if doc.GenerationID != "" {
		doc.Metadata["generation_id"] = doc.GenerationID
	}
	if stale, err := r.shouldSkipDocumentStore(ctx, doc); err != nil {
		return err
	} else if stale {
		return nil
	}

	tx := external
	var err error
	if tx == nil {
		tx, err = r.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer dbutilstore.Rollback(tx)
	}
	commit := func() error {
		if external != nil {
			return nil
		}
		return tx.Commit()
	}
	if writable, err := memorygeneration.LockWritableGeneration(ctx, tx, doc.UserID, doc.WorkspaceID, doc.GenerationID); err != nil || !writable {
		return err
	}
	if err = ingestionstore.BindIngestEvidenceTx(ctx, tx, &doc); err != nil {
		return err
	}
	if err = scopestore.LockMemoryIdentityTx(ctx, tx, doc.UserID, doc.WorkspaceID, doc.NodeID, doc.ID, string(doc.Type)); err != nil {
		return err
	}
	if err = scopestore.EnsureMemoryWorkspaceTx(ctx, tx, doc.UserID); err != nil {
		return err
	}
	if dbutilstore.StringValue(doc.Metadata, "extraction_method") == "llm" {
		var revision int64
		err := tx.QueryRowContext(ctx, `SELECT embedding_revision FROM memory_nodes WHERE user_id=$1 AND id=$2`, doc.UserID, doc.NodeID).Scan(&revision)
		if err != nil && !errors.Is(err, sqlpkg.ErrNoRows) {
			return err
		}
		// Explicit edits change the revision under the same identity lock.
		// A cached model result cannot overwrite that body or its embedding.
		if revision > 0 {
			return domain.ErrAccessConflict
		}
	}
	if doc.Type == domain.MemoryTypeSummary && doc.Metadata["_llm_summary_checkpoint"] != nil {
		var metadata []byte
		err := tx.QueryRowContext(ctx, `SELECT metadata FROM memory_summaries WHERE user_id=$1 AND id=$2`, doc.UserID, doc.ID).Scan(&metadata)
		if err != nil && !errors.Is(err, sqlpkg.ErrNoRows) {
			return err
		}
		var previous map[string]any
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &previous); err != nil {
				return err
			}
		}
		if sameProcessingCheckpoint(previous["_llm_summary_checkpoint"], doc.Metadata["_llm_summary_checkpoint"]) {
			return commit()
		}
	}

	_, err = tx.ExecContext(ctx, `
	        INSERT INTO memory_nodes (id, generation_id, user_id, node_type, entity_id, importance, decay_factor, activity_at, time_tier, embedding_status, created_at, updated_at, workspace_id,partition_id,subject_actor_ids,source_refs,revision,source_conversation_id)
	        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
	        ON CONFLICT (id) DO UPDATE SET
	            generation_id = EXCLUDED.generation_id,
             node_type = EXCLUDED.node_type,
             subject_actor_ids=EXCLUDED.subject_actor_ids,source_refs=EXCLUDED.source_refs,revision=memory_nodes.revision+1,
             source_conversation_id=EXCLUDED.source_conversation_id,
	            entity_id = EXCLUDED.entity_id,
	            importance = EXCLUDED.importance,
	            decay_factor = EXCLUDED.decay_factor,
	            activity_at = EXCLUDED.activity_at,
	            time_tier = EXCLUDED.time_tier,
	            embedding_status = EXCLUDED.embedding_status,
	            updated_at = EXCLUDED.updated_at
	        WHERE memory_nodes.user_id = EXCLUDED.user_id AND memory_nodes.workspace_id = EXCLUDED.workspace_id
	    `, doc.NodeID, doc.GenerationID, doc.UserID, string(doc.Type), doc.ID, doc.Importance, doc.DecayFactor, doc.ActivityAt, string(doc.TimeTier), "pending", doc.CreatedAt, doc.UpdatedAt, doc.WorkspaceID, doc.PartitionID, dbutilstore.MarshalJSON(doc.SubjectActorIDs), dbutilstore.MarshalJSON(doc.SourceRefs), doc.Revision, doc.ConversationID)
	if err != nil {
		return err
	}

	if err = ingestionstore.PersistIngestDependenciesTx(ctx, tx, doc); err != nil {
		return err
	}

	switch doc.Type {
	case domain.MemoryTypeSummary:
		_, err = tx.ExecContext(ctx, `
            INSERT INTO memory_summaries (id, generation_id, user_id, conversation_id, node_id, summary, topic_label, summary_version, chunk_index, chunk_total, created_at, updated_at, metadata)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
            ON CONFLICT (id) DO UPDATE SET
                generation_id = EXCLUDED.generation_id,
                summary = EXCLUDED.summary,
                metadata = EXCLUDED.metadata,
                topic_label = EXCLUDED.topic_label,
                updated_at = EXCLUDED.updated_at
        `, doc.ID, doc.GenerationID, doc.UserID, doc.ConversationID, doc.NodeID, doc.Content, dbutilstore.StringValue(doc.Metadata, "topic_label"), 1, 0, 1, doc.CreatedAt, doc.UpdatedAt, dbutilstore.MarshalJSON(doc.Metadata))
		if err == nil {
			keywords := dbutilstore.StringSlice(doc.Metadata, "keywords")
			rows := make([]memoryKeywordRow, 0, len(keywords))
			for idx, keyword := range keywords {
				rows = append(rows, memoryKeywordRow{
					ID:        fmt.Sprintf("%s:%d", doc.ID, idx),
					UserID:    doc.UserID,
					SummaryID: doc.ID,
					Keyword:   keyword,
					Weight:    1.0 - float64(idx)*0.1,
					CreatedAt: doc.CreatedAt,
					UpdatedAt: doc.UpdatedAt,
				})
			}
			if err = replaceMemoryKeywordsTx(ctx, tx, doc.UserID, doc.ID, rows); err != nil {
				return err
			}
		}
	case domain.MemoryTypeSegment:
		_, err = tx.ExecContext(ctx, `
            INSERT INTO conversation_segments (
                id, generation_id, user_id, conversation_id, segment_index, start_sequence, end_sequence,
                message_count, overlap_from_sequence, overlap_message_count, content, content_hash,
                metadata, created_at, updated_at
            )
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
            ON CONFLICT (id) DO UPDATE SET
                generation_id = EXCLUDED.generation_id,
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
        `, doc.ID, doc.GenerationID, doc.UserID, doc.ConversationID, dbutilstore.IntValue(doc.Metadata, "segment_index", 0), dbutilstore.IntValue(doc.Metadata, "start_sequence", 0),
			dbutilstore.IntValue(doc.Metadata, "end_sequence", 0), dbutilstore.IntValue(doc.Metadata, "message_count", 0),
			dbutilstore.IntValue(doc.Metadata, "overlap_from_sequence", 0), dbutilstore.IntValue(doc.Metadata, "overlap_message_count", 0),
			doc.Content, dbutilstore.DefaultString(dbutilstore.StringValue(doc.Metadata, "content_hash"), doc.ID), dbutilstore.MarshalJSON(doc.Metadata), doc.CreatedAt, doc.UpdatedAt)
	case domain.MemoryTypeImpression:
		_, err = tx.ExecContext(ctx, `
            INSERT INTO impressions (id, generation_id, user_id, conversation_id, node_id, subject, subject_type, evaluation, sentiment, familiarity, description, created_at, updated_at, metadata)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
            ON CONFLICT (id) DO UPDATE SET
                generation_id = EXCLUDED.generation_id,
                description = EXCLUDED.description,
                metadata = EXCLUDED.metadata,
                subject = EXCLUDED.subject,
                subject_type = EXCLUDED.subject_type,
                updated_at = EXCLUDED.updated_at
        `, doc.ID, doc.GenerationID, doc.UserID, doc.ConversationID, doc.NodeID, dbutilstore.StringValue(doc.Metadata, "subject"), dbutilstore.StringValue(doc.Metadata, "subject_type"), dbutilstore.StringValue(doc.Metadata, "evaluation"), dbutilstore.StringValue(doc.Metadata, "sentiment"), dbutilstore.StringValue(doc.Metadata, "familiarity"), doc.Content, doc.CreatedAt, doc.UpdatedAt, dbutilstore.MarshalJSON(doc.Metadata))
	case domain.MemoryTypeFact:
		supersededByIDValue := strings.TrimSpace(dbutilstore.StringValue(doc.Metadata, "superseded_by_id"))
		supersessionStateSpecified := conversationstore.MetadataHasKey(doc.Metadata, "superseded_by_id") || conversationstore.MetadataHasKey(doc.Metadata, "is_superseded")
		incomingIsSuperseded := supersededByIDValue != "" || dbutilstore.BoolValue(doc.Metadata, "is_superseded")
		validFromSpecified := conversationstore.MetadataHasKey(doc.Metadata, "valid_from")
		validToSpecified := conversationstore.MetadataHasKey(doc.Metadata, "valid_to")
		validFrom := dbutilstore.NullableTimeValue(doc.Metadata, "valid_from")
		validTo := dbutilstore.NullableTimeValue(doc.Metadata, "valid_to")
		factKey := factKeyFromMetadata(doc.Metadata)
		lineageGroupID := factLineageGroupID(doc.UserID, doc)
		supersededIDs := supersededFactIDsFromMetadata(doc.Metadata)
		if lineageGroupID == "" && (supersededByIDValue != "" || len(supersededIDs) > 0) {
			return errors.New("fact supersession requires fact_key or lineage_group_id")
		}
		if err = lockFactLineageTx(ctx, tx, doc.UserID, doc.GenerationID, lineageGroupID); err != nil {
			return err
		}
		if !supersessionStateSpecified {
			existingIsSuperseded, existingSupersededByID, stateErr := existingFactSupersessionStateTx(ctx, tx, doc.UserID, doc.GenerationID, doc.ID)
			if stateErr != nil {
				return stateErr
			}
			if existingIsSuperseded || existingSupersededByID != "" {
				incomingIsSuperseded = true
				supersededByIDValue = existingSupersededByID
			}
		}
		var supersededByID any
		if supersededByIDValue != "" {
			supersededByID = supersededByIDValue
		}
		revisionSeq := factLineageRevisionSeqFromMetadata(doc.Metadata)
		if revisionSeq <= 0 {
			nextSeq, seqErr := nextFactRevisionSeqTx(ctx, tx, doc.UserID, doc.GenerationID, lineageGroupID, doc.ID, doc.CreatedAt)
			if seqErr != nil {
				return seqErr
			}
			revisionSeq = nextSeq
			doc.Metadata["revision_seq"] = revisionSeq
		}
		if effectiveSupersedingID := supersededByIDValue; effectiveSupersedingID != "" {
			cycle, cycleErr := factSupersessionWouldCycleTx(ctx, tx, doc.UserID, doc.GenerationID, effectiveSupersedingID, doc.ID)
			if cycleErr != nil {
				return cycleErr
			}
			if cycle {
				return errors.New("fact supersession cycle detected")
			}
		}
		if validFromSpecified && !conversationstore.MetadataHasKey(doc.Metadata, "supersedes_fact_ids") && !supersessionStateSpecified && !incomingIsSuperseded && lineageGroupID != "" && len(supersededIDs) == 0 {
			previous, next, nextTime, temporalErr := factTemporalNeighborsTx(ctx, tx, doc)
			if temporalErr != nil {
				return temporalErr
			}
			if previous != "" {
				supersededIDs = []string{previous}
			}
			if next != "" {
				incomingIsSuperseded = true
				supersededByID = next
				validTo = nextTime
				validToSpecified = true
			}
		}
		if !conversationstore.MetadataHasKey(doc.Metadata, "supersedes_fact_ids") && !supersessionStateSpecified && !incomingIsSuperseded && lineageGroupID != "" && len(supersededIDs) == 0 && !validFromSpecified {
			lineageHeadIDs, headErr := currentFactLineageHeadIDsTx(ctx, tx, doc.UserID, doc.GenerationID, lineageGroupID, doc.ID)
			if headErr != nil {
				return headErr
			}
			supersededIDs = lineageHeadIDs
			if len(supersededIDs) > 0 {
				doc.Metadata["supersedes_fact_ids"] = supersededIDs
			}
		}
		var effectiveSupersededByID string
		err = tx.QueryRowContext(ctx, `
            INSERT INTO facts (
                id, generation_id, user_id, conversation_id, node_id,
                category, subject, fact_key, lineage_group_id, revision_seq,
                content, confidence, is_superseded, superseded_by_id,
                valid_from, valid_to, created_at, updated_at, metadata
            )
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$22)
            ON CONFLICT (id) DO UPDATE SET
                generation_id = EXCLUDED.generation_id,
                content = EXCLUDED.content,
                category = EXCLUDED.category,
                subject = EXCLUDED.subject,
                fact_key = COALESCE(NULLIF(EXCLUDED.fact_key, ''), facts.fact_key),
                lineage_group_id = COALESCE(NULLIF(EXCLUDED.lineage_group_id, ''), facts.lineage_group_id),
                revision_seq = CASE WHEN EXCLUDED.revision_seq > 0 THEN EXCLUDED.revision_seq ELSE facts.revision_seq END,
                confidence = EXCLUDED.confidence,
                metadata = EXCLUDED.metadata,
                is_superseded = CASE WHEN $19 THEN EXCLUDED.is_superseded ELSE facts.is_superseded OR EXCLUDED.is_superseded END,
                superseded_by_id = CASE WHEN $19 THEN EXCLUDED.superseded_by_id ELSE COALESCE(facts.superseded_by_id, EXCLUDED.superseded_by_id) END,
                valid_from = CASE WHEN $20 THEN EXCLUDED.valid_from ELSE COALESCE(EXCLUDED.valid_from, facts.valid_from) END,
                valid_to = CASE WHEN $21 THEN EXCLUDED.valid_to ELSE COALESCE(EXCLUDED.valid_to, facts.valid_to) END,
                updated_at = EXCLUDED.updated_at
            RETURNING COALESCE(superseded_by_id, '')
		`, doc.ID, doc.GenerationID, doc.UserID, doc.ConversationID, doc.NodeID, dbutilstore.StringValue(doc.Metadata, "category"), dbutilstore.StringValue(doc.Metadata, "subject"), factKey, lineageGroupID, revisionSeq, doc.Content, dbutilstore.FloatValue(doc.Metadata, "confidence", 0.8), incomingIsSuperseded, supersededByID, validFrom, validTo, doc.CreatedAt, doc.UpdatedAt, supersessionStateSpecified, validFromSpecified, validToSpecified, dbutilstore.MarshalJSON(doc.Metadata)).Scan(&effectiveSupersededByID)
		if err == nil {
			if err = syncFactKeywordsTx(ctx, tx, doc); err != nil {
				return err
			}
			for _, supersededFactID := range supersededIDs {
				if supersededFactID == "" {
					continue
				}
				if supersededFactID == doc.ID {
					return errors.New("fact supersession cycle detected")
				}
				cycle, cycleErr := factSupersessionWouldCycleTx(ctx, tx, doc.UserID, doc.GenerationID, doc.ID, supersededFactID)
				if cycleErr != nil {
					return cycleErr
				}
				if cycle {
					return errors.New("fact supersession cycle detected")
				}
				result, execErr := tx.ExecContext(ctx, `
                    UPDATE facts
                    SET is_superseded = TRUE,
                        superseded_by_id = $4,
                        valid_to = LEAST(valid_to, $5),
                        updated_at = $6
                    WHERE user_id = $1
                      AND generation_id = $2
                      AND id = $3
                      AND id <> $4
                      AND COALESCE(lineage_group_id, '') = $7
                `, doc.UserID, doc.GenerationID, supersededFactID, doc.ID, factEffectiveFrom(doc), doc.UpdatedAt, lineageGroupID)
				if execErr != nil {
					return execErr
				}
				rowsAffected, affectedErr := result.RowsAffected()
				if affectedErr != nil {
					return affectedErr
				}
				if rowsAffected != 1 {
					return fmt.Errorf("fact supersession target %q not found in lineage %q", supersededFactID, lineageGroupID)
				}
			}
			if _, err = tx.ExecContext(ctx, `
                DELETE FROM memory_relations
                WHERE user_id = $1 AND relation_type = $2 AND generation_id = $3 AND (dst_node_id = $4 OR src_node_id = $4)
            `, doc.UserID, domain.MemoryRelationSupersedes, doc.GenerationID, doc.NodeID); err != nil {
				return err
			}

			if effectiveSupersededByID != "" {
				var supersedingNodeID string
				switch scanErr := tx.QueryRowContext(ctx, `
                    SELECT node_id FROM facts WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=facts.user_id AND scope_node.id=facts.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND generation_id = $2 AND id = $3 AND COALESCE(lineage_group_id, '') = $4
                `, doc.UserID, doc.GenerationID, effectiveSupersededByID, lineageGroupID).Scan(&supersedingNodeID); scanErr {
				case nil:
					if _, err = tx.ExecContext(ctx, `
                        INSERT INTO memory_relations (id, generation_id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at, updated_at)
                        VALUES ($1,$2,$3,$4,$5,$6,1,'fact_supersession',$7,$8,$8)
                        ON CONFLICT (user_id, src_node_id, dst_node_id, relation_type) DO UPDATE SET
                            generation_id = EXCLUDED.generation_id,
                            weight = EXCLUDED.weight,
                            source = EXCLUDED.source,
                            metadata = EXCLUDED.metadata,
                            updated_at = EXCLUDED.updated_at
					`, uuid.NewString(), doc.GenerationID, doc.UserID, supersedingNodeID, doc.NodeID, domain.MemoryRelationSupersedes, dbutilstore.MarshalJSON(map[string]any{
						"superseded_fact_id":  doc.ID,
						"superseding_fact_id": effectiveSupersededByID,
						"fact_key":            factKey,
						"lineage_group_id":    lineageGroupID,
					}), doc.UpdatedAt); err != nil {
						return err
					}
				case sqlpkg.ErrNoRows:
					return fmt.Errorf("fact supersession target %q not found in lineage %q", effectiveSupersededByID, lineageGroupID)
				default:
					return scanErr
				}
			}

			if lineageGroupID != "" {
				rows, queryErr := tx.QueryContext(ctx, `
                    SELECT id, node_id
                    FROM facts
                    WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=facts.user_id AND scope_node.id=facts.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND generation_id = $2 AND superseded_by_id = $3 AND id <> $4
                      AND COALESCE(lineage_group_id, '') = $5
                `, doc.UserID, doc.GenerationID, doc.ID, doc.ID, lineageGroupID)
				if queryErr != nil {
					return queryErr
				}

				type supersededFact struct{ id, nodeID string }
				pending := []supersededFact{}
				for rows.Next() {
					var item supersededFact
					if err = rows.Scan(&item.id, &item.nodeID); err != nil {
						rows.Close()
						return err
					}
					pending = append(pending, item)
				}
				rowsErr := rows.Err()
				rows.Close()
				if rowsErr != nil {
					return rowsErr
				}
				for _, item := range pending {
					supersededFactID, supersededNodeID := item.id, item.nodeID
					if _, err = tx.ExecContext(ctx, `
                        INSERT INTO memory_relations (id, generation_id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at, updated_at)
                        VALUES ($1,$2,$3,$4,$5,$6,1,'fact_supersession',$7,$8,$8)
                        ON CONFLICT (user_id, src_node_id, dst_node_id, relation_type) DO UPDATE SET
                            generation_id = EXCLUDED.generation_id,
                            weight = EXCLUDED.weight,
                            source = EXCLUDED.source,
                            metadata = EXCLUDED.metadata,
                            updated_at = EXCLUDED.updated_at
                    `, uuid.NewString(), doc.GenerationID, doc.UserID, doc.NodeID, supersededNodeID, domain.MemoryRelationSupersedes, dbutilstore.MarshalJSON(map[string]any{
						"superseded_fact_id":  supersededFactID,
						"superseding_fact_id": doc.ID,
						"fact_key":            factKey,
						"lineage_group_id":    lineageGroupID,
					}), doc.UpdatedAt); err != nil {
						return err
					}
				}
			}
		}
	case domain.MemoryTypeEpisode, domain.MemoryTypeEvent,
		domain.MemoryTypeTask,
		domain.MemoryTypeProcedure,
		domain.MemoryTypeArtifact,
		domain.MemoryTypeEntity,
		domain.MemoryTypeAgentAction:
		title := dbutilstore.StringValue(doc.Metadata, "title")
		status := dbutilstore.DefaultString(dbutilstore.StringValue(doc.Metadata, "status"), defaultMemoryItemStatus(doc.Type))
		reviewStatus := dbutilstore.DefaultString(dbutilstore.StringValue(doc.Metadata, "review_status"), "auto")
		confidence := dbutilstore.FloatValue(doc.Metadata, "confidence", 0.75)
		validFrom := dbutilstore.NullableTimeValue(doc.Metadata, "valid_from")
		validTo := dbutilstore.NullableTimeValue(doc.Metadata, "valid_to")
		doc.Metadata["item_type"] = string(doc.Type)
		doc.Metadata["status"] = status
		doc.Metadata["review_status"] = reviewStatus
		doc.Metadata["confidence"] = confidence
		if title != "" {
			doc.Metadata["title"] = title
		}
		_, err = tx.ExecContext(ctx, `
            INSERT INTO memory_items (
                id, generation_id, user_id, conversation_id, node_id, item_type,
                title, content, status, confidence, valid_from, valid_to,
                review_status, metadata, created_at, updated_at
            )
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
            ON CONFLICT (id) DO UPDATE SET
                generation_id = EXCLUDED.generation_id,
                conversation_id = EXCLUDED.conversation_id,
                node_id = EXCLUDED.node_id,
                item_type = EXCLUDED.item_type,
                title = EXCLUDED.title,
                content = EXCLUDED.content,
                status = EXCLUDED.status,
                confidence = EXCLUDED.confidence,
                valid_from = EXCLUDED.valid_from,
                valid_to = EXCLUDED.valid_to,
                review_status = EXCLUDED.review_status,
                metadata = EXCLUDED.metadata,
                deleted_at = NULL,
                updated_at = EXCLUDED.updated_at
        `, doc.ID, doc.GenerationID, doc.UserID, doc.ConversationID, doc.NodeID, string(doc.Type), title, doc.Content, status, confidence, validFrom, validTo, reviewStatus, dbutilstore.MarshalJSON(doc.Metadata), doc.CreatedAt, doc.UpdatedAt)
	default:
		return fmt.Errorf("unsupported memory type: %s", doc.Type)
	}
	if err != nil {
		return err
	}
	if err = syncTemporalRelationsTx(ctx, tx, doc.UserID, doc.GenerationID, doc.NodeID, doc.ActivityAt, doc.TimeTier, doc.UpdatedAt); err != nil {
		return err
	}
	return commit()
}

func (r *PostgresMemoryRepository) DeleteDocument(ctx context.Context, userID, id string, kind domain.MemoryType) error {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return err
	}
	var nodeID string
	if err := r.db.QueryRowContext(ctx, `
        SELECT id
        FROM memory_nodes
        WHERE `+storeaccess.NodeSQL(ctx, "memory_nodes")+` AND user_id = $1 AND entity_id = $2 AND node_type = $3
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
	defer dbutilstore.Rollback(tx)

	if _, err := tx.ExecContext(ctx, `
        DELETE FROM memory_relations
        WHERE user_id = $1 AND (src_node_id = $2 OR dst_node_id = $2)
    `, userID, nodeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
        DELETE FROM memory_evidences
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=memory_evidences.user_id AND scope_node.id=memory_evidences.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND node_id = $2
    `, userID, nodeID); err != nil {
		return err
	}
	if err := deleteMemoryEmbeddingsByNodeIDsTx(ctx, tx, userID, []string{nodeID}); err != nil {
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
		if _, err := tx.ExecContext(ctx, `DELETE FROM facts WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=facts.user_id AND scope_node.id=facts.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND id = $2`, userID, id); err != nil {
			return err
		}
	case domain.MemoryTypeEpisode, domain.MemoryTypeEvent,
		domain.MemoryTypeTask,
		domain.MemoryTypeProcedure,
		domain.MemoryTypeArtifact,
		domain.MemoryTypeEntity,
		domain.MemoryTypeAgentAction:
		if _, err := tx.ExecContext(ctx, `
            UPDATE memory_items
            SET deleted_at = NOW(), updated_at = NOW()
            WHERE user_id = $1 AND id = $2 AND item_type = $3
        `, userID, id, string(kind)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported memory type: %s", kind)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_nodes WHERE `+storeaccess.NodeSQL(ctx, "memory_nodes")+` AND user_id = $1 AND id = $2`, userID, nodeID); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *PostgresMemoryRepository) DeleteSegmentsByConversation(ctx context.Context, userID, conversationID string, keepIDs []string) ([]string, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	return r.DeleteSegmentsByConversationInGeneration(ctx, userID, "", conversationID, keepIDs)
}

func (r *PostgresMemoryRepository) DeleteSegmentsByConversationInGeneration(ctx context.Context, userID, generationID, conversationID string, keepIDs []string) ([]string, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
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
        WHERE `+storeaccess.NodeSQL(ctx, "memory_nodes")+` AND user_id = $1 AND node_type = $2 AND source_conversation_id = $3
    `+recordutilstore.GenerationFilterClause("generation_id", 4, userID, generationID, false), recordutilstore.GenerationFilterArgs(userID, generationID, userID, string(domain.MemoryTypeSegment), conversationID)...)
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
	defer dbutilstore.Rollback(tx)

	if err := deleteConversationSegmentsByConversationTx(ctx, tx, userID, generationID, conversationID, keepIDs); err != nil {
		return nil, err
	}
	if len(nodeIDs) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err := deleteMemoryEvidencesByNodeIDsTx(ctx, tx, userID, nodeIDs); err != nil {
		return nil, err
	}
	if err := deleteRelationsByNodeIDsTx(ctx, tx, userID, nodeIDs); err != nil {
		return nil, err
	}
	if err := deleteMemoryEmbeddingsByNodeIDsTx(ctx, tx, userID, nodeIDs); err != nil {
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
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryDocument{}, err
	}
	return r.GetDocumentInGeneration(ctx, userID, "", id, kind)
}

func (r *PostgresMemoryRepository) GetDocumentInGeneration(ctx context.Context, userID, generationID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryDocument{}, err
	}
	docs, err := r.listByEntityIDsInGeneration(ctx, userID, generationID, kind, []string{id})
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	if len(docs) == 0 {
		return domain.MemoryDocument{}, domain.ErrAccessNotFound
	}
	return docs[0], nil
}

func (r *PostgresMemoryRepository) ListDocuments(ctx context.Context, userID string, memoryTypes []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	return r.ListDocumentsInGeneration(ctx, userID, "", memoryTypes, limit)
}

func (r *PostgresMemoryRepository) ListDocumentsInGeneration(ctx context.Context, userID, generationID string, memoryTypes []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	return r.listDocumentsInGeneration(ctx, userID, generationID, memoryTypes, limit, nil, nil)
}

func (r *PostgresMemoryRepository) listDocumentsInGeneration(ctx context.Context, userID, generationID string, memoryTypes []domain.MemoryType, limit int, browse *domain.MemoryBrowseQuery, position *domain.MemoryPosition) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	types := memoryTypes
	if len(types) == 0 {
		types = domain.DefaultSearchMemoryTypes(true)
	}
	if limit <= 0 {
		limit = 100
	}
	seenTypes := make(map[domain.MemoryType]struct{}, len(types))
	orderedTypes := make([]domain.MemoryType, 0, len(types))
	for _, kind := range types {
		if _, ok := seenTypes[kind]; ok {
			continue
		}
		seenTypes[kind] = struct{}{}
		orderedTypes = append(orderedTypes, kind)
	}

	needsGenerationFilter := false
	for _, kind := range orderedTypes {
		if kind != domain.MemoryTypeKeyMemory {
			needsGenerationFilter = true
			break
		}
	}

	branches := make([]string, 0, len(orderedTypes))
	args := []any{userID, limit}
	generationClauseSummary := ""
	generationClauseSegment := ""
	generationClauseImpression := ""
	generationClauseFact := ""
	generationClauseItem := ""
	if needsGenerationFilter {
		generationArgIndex := 3
		generationClauseSummary = recordutilstore.GenerationFilterClause("s.generation_id", generationArgIndex, userID, generationID, false)
		generationClauseSegment = recordutilstore.GenerationFilterClause("cs.generation_id", generationArgIndex, userID, generationID, false)
		generationClauseImpression = recordutilstore.GenerationFilterClause("i.generation_id", generationArgIndex, userID, generationID, false)
		generationClauseFact = recordutilstore.GenerationFilterClause("f.generation_id", generationArgIndex, userID, generationID, false)
		generationClauseItem = recordutilstore.GenerationFilterClause("mi.generation_id", generationArgIndex, userID, generationID, false)
		args = recordutilstore.AppendGenerationArg(args, userID, generationID)
	}

	for _, kind := range orderedTypes {
		switch kind {
		case domain.MemoryTypeSummary:
			branches = append(branches, fmt.Sprintf(`
                SELECT s.id, s.node_id, s.generation_id, s.conversation_id, s.summary AS content,
                       COALESCE(n.importance, 1) AS importance,
                       COALESCE(n.decay_factor, 1) AS decay_factor,
                       COALESCE(n.activity_at, s.updated_at) AS activity_at,
                       COALESCE(n.time_tier, '') AS time_tier,
                       s.created_at, s.updated_at,
                       COALESCE(s.metadata, '{}'::jsonb) || jsonb_build_object(
                           'topic_label', s.topic_label,
                           'keywords', COALESCE((
                               SELECT json_agg(k.keyword ORDER BY k.weight DESC)
                               FROM memory_keywords k
                               WHERE k.summary_id = s.id AND k.user_id = s.user_id
                           ), '[]'::json)
                       ) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                       'summary' AS memory_type
                FROM memory_summaries s
                LEFT JOIN memory_nodes n ON n.user_id = s.user_id AND n.id = s.node_id
                WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=s.user_id AND scope_node.id=s.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND s.user_id = $1%s
            `, generationClauseSummary))
		case domain.MemoryTypeSegment:
			branches = append(branches, fmt.Sprintf(`
                SELECT cs.id, n.id AS node_id, cs.generation_id, cs.conversation_id, cs.content,
                       COALESCE(n.importance, 1) AS importance,
                       COALESCE(n.decay_factor, 1) AS decay_factor,
                       COALESCE(n.activity_at, cs.updated_at) AS activity_at,
                       COALESCE(n.time_tier, '') AS time_tier,
                       cs.created_at, cs.updated_at,
                       jsonb_build_object(
                           'segment_index', cs.segment_index,
                           'start_sequence', cs.start_sequence,
                           'end_sequence', cs.end_sequence,
                           'message_count', cs.message_count,
                           'overlap_from_sequence', cs.overlap_from_sequence,
                           'overlap_message_count', cs.overlap_message_count,
                           'content_hash', cs.content_hash
                       ) || COALESCE(cs.metadata, '{}'::jsonb) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                       'segment' AS memory_type
                FROM conversation_segments cs
                JOIN memory_nodes n
                  ON n.user_id = cs.user_id
                 AND n.entity_id = cs.id
                 AND n.node_type = 'segment'
                WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND cs.user_id = $1%s
            `, generationClauseSegment))
		case domain.MemoryTypeImpression:
			branches = append(branches, fmt.Sprintf(`
                SELECT i.id, i.node_id, i.generation_id, i.conversation_id, i.description AS content,
                       COALESCE(n.importance, 1) AS importance,
                       COALESCE(n.decay_factor, 1) AS decay_factor,
                       COALESCE(n.activity_at, i.updated_at) AS activity_at,
                       COALESCE(n.time_tier, '') AS time_tier,
                       i.created_at, i.updated_at,
                       COALESCE(i.metadata, '{}'::jsonb) || jsonb_build_object('subject', i.subject, 'subject_type', i.subject_type, 'evaluation', i.evaluation, 'sentiment', i.sentiment, 'familiarity', i.familiarity) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                       'impression' AS memory_type
                FROM impressions i
                LEFT JOIN memory_nodes n ON n.user_id = i.user_id AND n.id = i.node_id
                WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=i.user_id AND scope_node.id=i.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND i.user_id = $1%s
            `, generationClauseImpression))
		case domain.MemoryTypeFact:
			branches = append(branches, fmt.Sprintf(`
                SELECT f.id, f.node_id, f.generation_id, f.conversation_id, f.content,
                       COALESCE(n.importance, 1) AS importance,
                       COALESCE(n.decay_factor, 1) AS decay_factor,
                       COALESCE(n.activity_at, f.updated_at) AS activity_at,
                       COALESCE(n.time_tier, '') AS time_tier,
                       f.created_at, f.updated_at,
                       COALESCE(f.metadata, '{}'::jsonb) || jsonb_build_object(
                           'category', f.category,
                           'subject', f.subject,
                           'fact_key', f.fact_key,
                           'lineage_group_id', f.lineage_group_id,
                           'revision_seq', f.revision_seq,
                           'confidence', f.confidence,
                           'is_superseded', f.is_superseded,
                           'valid_from', f.valid_from,
                           'valid_to', f.valid_to,
                           'superseded_by_id', f.superseded_by_id,
                           'keywords', COALESCE((
                               SELECT json_agg(k.keyword ORDER BY k.weight DESC)
                               FROM memory_keywords k
                               WHERE k.summary_id = f.id AND k.user_id = f.user_id
                           ), '[]'::json)
                       ) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                       'fact' AS memory_type
                FROM facts f
                LEFT JOIN memory_nodes n ON n.user_id = f.user_id AND n.id = f.node_id
                WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=f.user_id AND scope_node.id=f.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND f.user_id = $1%s
            `, generationClauseFact))
		case domain.MemoryTypeKeyMemory:
			branches = append(branches, `
                SELECT k.id, k.node_id, '' AS generation_id, '' AS conversation_id, CONCAT(k.key, ': ', k.value) AS content,
                       COALESCE(n.importance, 1) AS importance,
                       COALESCE(n.decay_factor, 1) AS decay_factor,
                       COALESCE(n.activity_at, k.updated_at) AS activity_at,
                       COALESCE(n.time_tier, '') AS time_tier,
                       k.created_at, k.updated_at,
                       jsonb_build_object('key', k.key, 'value', k.value, 'category', k.category, 'source', k.source, 'review_status', k.review_status, 'last_confirmed_at', k.last_confirmed_at) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                       'key_memory' AS memory_type
                FROM key_memories k
                LEFT JOIN memory_nodes n ON n.user_id = k.user_id AND n.id = k.node_id
                WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=k.user_id AND scope_node.id=k.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND k.user_id = $1 AND k.deleted_at IS NULL
            `)
		case domain.MemoryTypeEpisode, domain.MemoryTypeEvent,
			domain.MemoryTypeTask,
			domain.MemoryTypeProcedure,
			domain.MemoryTypeArtifact,
			domain.MemoryTypeEntity,
			domain.MemoryTypeAgentAction:
			branches = append(branches, fmt.Sprintf(`
                SELECT mi.id, mi.node_id, mi.generation_id, mi.conversation_id, mi.content,
                       COALESCE(n.importance, 1) AS importance,
                       COALESCE(n.decay_factor, 1) AS decay_factor,
                       COALESCE(n.activity_at, mi.updated_at) AS activity_at,
                       COALESCE(n.time_tier, '') AS time_tier,
                       mi.created_at, mi.updated_at,
                       jsonb_build_object(
                           'item_type', mi.item_type,
                           'title', mi.title,
                           'status', mi.status,
                           'confidence', mi.confidence,
                           'valid_from', mi.valid_from,
                           'valid_to', mi.valid_to,
                           'review_status', mi.review_status
                       ) || COALESCE(mi.metadata, '{}'::jsonb) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                       mi.item_type AS memory_type
                FROM memory_items mi
                LEFT JOIN memory_nodes n ON n.user_id = mi.user_id AND n.id = mi.node_id
                WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=mi.user_id AND scope_node.id=mi.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND mi.user_id = $1 AND mi.item_type = '%s' AND mi.deleted_at IS NULL%s
            `, string(kind), generationClauseItem))
		}
	}

	if len(branches) == 0 {
		return nil, nil
	}
	filter := ""
	if browse != nil {
		args = append(args, browse.Query)
		filter = fmt.Sprintf(" WHERE (strpos(lower(content), lower($%d)) > 0 OR strpos(lower(id), lower($%d)) > 0)", len(args), len(args))
		if position != nil {
			args = append(args, position.UpdatedAt, string(position.Kind), position.ID)
			filter += fmt.Sprintf(` AND (updated_at, memory_type COLLATE "C", id COLLATE "C") < ($%d, $%d COLLATE "C", $%d COLLATE "C")`, len(args)-2, len(args)-1, len(args))
		}
	}
	for i := range branches {
		// Filter and keyset precede each branch's limit, including old records.
		branches[i] = `(SELECT * FROM (` + branches[i] + `) branch` + filter + ` ORDER BY updated_at DESC, memory_type COLLATE "C" DESC, id COLLATE "C" DESC LIMIT $2)`
	}
	query := fmt.Sprintf(`
        SELECT id, node_id, generation_id, conversation_id, content, importance, decay_factor,
               activity_at, time_tier, created_at, updated_at, metadata, memory_type
        FROM (
            %s
        ) docs
        ORDER BY updated_at DESC, memory_type COLLATE "C" DESC, id COLLATE "C" DESC
        LIMIT $2
    `, strings.Join(branches, " UNION ALL "))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTypedMemoryDocumentRows(ctx, rows, userID)
}

func (r *PostgresMemoryRepository) ListDocumentsByNodeIDs(ctx context.Context, userID string, nodeIDs []string) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	return r.ListDocumentsByNodeIDsInGeneration(ctx, userID, "", nodeIDs)
}

func (r *PostgresMemoryRepository) ListDocumentsByNodeIDsInGeneration(ctx context.Context, userID, generationID string, nodeIDs []string) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	nodeIDs = dbutilstore.UniqueNonEmptyStrings(nodeIDs)
	if len(nodeIDs) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(nodeIDs)+2)
	args = append(args, userID)
	for _, nodeID := range nodeIDs {
		args = append(args, nodeID)
	}
	inClause := dbutilstore.Placeholders(2, len(nodeIDs))
	generationArgIndex := len(args) + 1
	generationClause := recordutilstore.GenerationFilterClause("n.generation_id", generationArgIndex, userID, generationID, false)
	args = recordutilstore.AppendGenerationArg(args, userID, generationID)
	query := fmt.Sprintf(`
        SELECT id, node_id, generation_id, conversation_id, content, importance, decay_factor,
               activity_at, time_tier, created_at, updated_at, metadata, memory_type
        FROM (
            SELECT s.id, s.node_id, s.generation_id, s.conversation_id, s.summary AS content,
                   COALESCE(n.importance, 1) AS importance,
                   COALESCE(n.decay_factor, 1) AS decay_factor,
                   COALESCE(n.activity_at, s.updated_at) AS activity_at,
                   COALESCE(n.time_tier, '') AS time_tier,
                   s.created_at, s.updated_at,
                   COALESCE(s.metadata, '{}'::jsonb) || jsonb_build_object(
                       'topic_label', s.topic_label,
                       'keywords', COALESCE((
                           SELECT jsonb_agg(k.keyword ORDER BY k.weight DESC)
                           FROM memory_keywords k
                           WHERE k.summary_id = s.id AND k.user_id = s.user_id
                       ), '[]'::jsonb)
                   ) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                   'summary' AS memory_type
            FROM memory_nodes n
            JOIN memory_summaries s ON s.user_id = n.user_id AND s.node_id = n.id
            WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND n.user_id = $1 AND n.id IN (%[1]s) AND n.node_type = 'summary'%[2]s

            UNION ALL

            SELECT cs.id, n.id AS node_id, cs.generation_id, cs.conversation_id, cs.content,
                   COALESCE(n.importance, 1) AS importance,
                   COALESCE(n.decay_factor, 1) AS decay_factor,
                   COALESCE(n.activity_at, cs.updated_at) AS activity_at,
                   COALESCE(n.time_tier, '') AS time_tier,
                   cs.created_at, cs.updated_at,
                   jsonb_build_object(
                       'segment_index', cs.segment_index,
                       'start_sequence', cs.start_sequence,
                       'end_sequence', cs.end_sequence,
                       'message_count', cs.message_count,
                       'overlap_from_sequence', cs.overlap_from_sequence,
                       'overlap_message_count', cs.overlap_message_count,
                       'content_hash', cs.content_hash
                   ) || COALESCE(cs.metadata, '{}'::jsonb) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                   'segment' AS memory_type
            FROM memory_nodes n
            JOIN conversation_segments cs
              ON cs.user_id = n.user_id
             AND cs.id = n.entity_id
             AND n.node_type = 'segment'
            WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND n.user_id = $1 AND n.id IN (%[1]s)%[2]s

            UNION ALL

            SELECT i.id, i.node_id, i.generation_id, i.conversation_id, i.description AS content,
                   COALESCE(n.importance, 1) AS importance,
                   COALESCE(n.decay_factor, 1) AS decay_factor,
                   COALESCE(n.activity_at, i.updated_at) AS activity_at,
                   COALESCE(n.time_tier, '') AS time_tier,
                   i.created_at, i.updated_at,
                   COALESCE(i.metadata, '{}'::jsonb) || jsonb_build_object('subject', i.subject, 'subject_type', i.subject_type, 'evaluation', i.evaluation, 'sentiment', i.sentiment, 'familiarity', i.familiarity) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                   'impression' AS memory_type
            FROM memory_nodes n
            JOIN impressions i ON i.user_id = n.user_id AND i.node_id = n.id
            WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND n.user_id = $1 AND n.id IN (%[1]s) AND n.node_type = 'impression'%[2]s

            UNION ALL

            SELECT f.id, f.node_id, f.generation_id, f.conversation_id, f.content,
                   COALESCE(n.importance, 1) AS importance,
                   COALESCE(n.decay_factor, 1) AS decay_factor,
                   COALESCE(n.activity_at, f.updated_at) AS activity_at,
                   COALESCE(n.time_tier, '') AS time_tier,
                   f.created_at, f.updated_at,
                   COALESCE(f.metadata, '{}'::jsonb) || jsonb_build_object(
                       'category', f.category,
                       'subject', f.subject,
                       'fact_key', f.fact_key,
                       'lineage_group_id', f.lineage_group_id,
                       'revision_seq', f.revision_seq,
                       'confidence', f.confidence,
                       'is_superseded', f.is_superseded,
                       'valid_from', f.valid_from,
                       'valid_to', f.valid_to,
                       'superseded_by_id', f.superseded_by_id,
                       'keywords', COALESCE((
                           SELECT jsonb_agg(k.keyword ORDER BY k.weight DESC)
                           FROM memory_keywords k
                           WHERE k.summary_id = f.id AND k.user_id = f.user_id
                       ), '[]'::jsonb)
                   ) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                   'fact' AS memory_type
            FROM memory_nodes n
            JOIN facts f ON f.user_id = n.user_id AND f.node_id = n.id
            WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND n.user_id = $1 AND n.id IN (%[1]s) AND n.node_type = 'fact'%[2]s

            UNION ALL

            SELECT mi.id, mi.node_id, mi.generation_id, mi.conversation_id, mi.content,
                   COALESCE(n.importance, 1) AS importance,
                   COALESCE(n.decay_factor, 1) AS decay_factor,
                   COALESCE(n.activity_at, mi.updated_at) AS activity_at,
                   COALESCE(n.time_tier, '') AS time_tier,
                   mi.created_at, mi.updated_at,
                   jsonb_build_object(
                       'item_type', mi.item_type,
                       'title', mi.title,
                       'status', mi.status,
                       'confidence', mi.confidence,
                       'valid_from', mi.valid_from,
                       'valid_to', mi.valid_to,
                       'review_status', mi.review_status
                   ) || COALESCE(mi.metadata, '{}'::jsonb) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                   mi.item_type AS memory_type
            FROM memory_nodes n
            JOIN memory_items mi ON mi.user_id = n.user_id AND mi.node_id = n.id
            WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND n.user_id = $1
              AND n.id IN (%[1]s)
              AND n.node_type IN ('episode', 'event', 'task', 'procedure', 'artifact', 'entity', 'agent_action')
              AND mi.deleted_at IS NULL%[2]s

            UNION ALL

            SELECT k.id, k.node_id, '' AS generation_id, '' AS conversation_id, CONCAT(k.key, ': ', k.value) AS content,
                   COALESCE(n.importance, 1) AS importance,
                   COALESCE(n.decay_factor, 1) AS decay_factor,
                   COALESCE(n.activity_at, k.updated_at) AS activity_at,
                   COALESCE(n.time_tier, '') AS time_tier,
                   k.created_at, k.updated_at,
                   jsonb_build_object('key', k.key, 'value', k.value, 'category', k.category, 'source', k.source, 'review_status', k.review_status, 'last_confirmed_at', k.last_confirmed_at) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                   'key_memory' AS memory_type
            FROM memory_nodes n
            JOIN key_memories k ON k.user_id = n.user_id AND k.node_id = n.id
            WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND n.user_id = $1 AND n.id IN (%[1]s) AND n.node_type = 'key_memory' AND k.deleted_at IS NULL
        ) docs
        ORDER BY updated_at DESC
    `, inClause, generationClause)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTypedMemoryDocumentRows(ctx, rows, userID)
}

func (r *PostgresMemoryRepository) listByEntityIDsInGeneration(ctx context.Context, userID, generationID string, kind domain.MemoryType, ids []string) ([]domain.MemoryDocument, error) {
	ids = dbutilstore.UniqueNonEmptyStrings(ids)
	if len(ids) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(ids)+2)
	args = append(args, userID)
	for _, id := range ids {
		args = append(args, id)
	}
	inClause := dbutilstore.Placeholders(2, len(ids))
	generationArgIndex := len(args) + 1

	var query string
	switch kind {
	case domain.MemoryTypeSummary:
		generationClause := recordutilstore.GenerationFilterClause("s.generation_id", generationArgIndex, userID, generationID, false)
		args = recordutilstore.AppendGenerationArg(args, userID, generationID)
		query = fmt.Sprintf(`
            SELECT s.id, s.node_id, s.generation_id, s.conversation_id, s.summary AS content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, s.updated_at), COALESCE(n.time_tier, ''),
                   s.created_at, s.updated_at,
                   COALESCE(s.metadata, '{}'::jsonb) || jsonb_build_object(
                       'topic_label', s.topic_label,
                       'keywords', COALESCE((
                           SELECT json_agg(k.keyword ORDER BY k.weight DESC)
                           FROM memory_keywords k
                           WHERE k.summary_id = s.id AND k.user_id = s.user_id
                       ), '[]'::json)
                   ) || `+scopestore.NodeAccessMetadataSQL()+`,
                   'summary'
            FROM memory_summaries s
            JOIN memory_nodes n ON n.user_id = s.user_id AND n.id = s.node_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=s.user_id AND scope_node.id=s.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND s.user_id = $1 AND s.id IN (%s)%s
            ORDER BY s.updated_at DESC
        `, inClause, generationClause)
	case domain.MemoryTypeSegment:
		generationClause := recordutilstore.GenerationFilterClause("cs.generation_id", generationArgIndex, userID, generationID, false)
		args = recordutilstore.AppendGenerationArg(args, userID, generationID)
		query = fmt.Sprintf(`
            SELECT cs.id, n.id AS node_id, cs.generation_id, cs.conversation_id, cs.content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, cs.updated_at), COALESCE(n.time_tier, ''),
                   cs.created_at, cs.updated_at,
                   jsonb_build_object(
                       'segment_index', cs.segment_index,
                       'start_sequence', cs.start_sequence,
                       'end_sequence', cs.end_sequence,
                       'message_count', cs.message_count,
                       'overlap_from_sequence', cs.overlap_from_sequence,
                       'overlap_message_count', cs.overlap_message_count,
                       'content_hash', cs.content_hash
                   ) || COALESCE(cs.metadata, '{}'::jsonb) || `+scopestore.NodeAccessMetadataSQL()+`,
                   'segment'
            FROM conversation_segments cs
            JOIN memory_nodes n
              ON n.user_id = cs.user_id
             AND n.entity_id = cs.id
             AND n.node_type = 'segment'
            WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND cs.user_id = $1 AND cs.id IN (%s)%s
            ORDER BY cs.updated_at DESC, cs.segment_index DESC
        `, inClause, generationClause)
	case domain.MemoryTypeImpression:
		generationClause := recordutilstore.GenerationFilterClause("i.generation_id", generationArgIndex, userID, generationID, false)
		args = recordutilstore.AppendGenerationArg(args, userID, generationID)
		query = fmt.Sprintf(`
            SELECT i.id, i.node_id, i.generation_id, i.conversation_id, i.description AS content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, i.updated_at), COALESCE(n.time_tier, ''),
                   i.created_at, i.updated_at,
                   COALESCE(i.metadata, '{}'::jsonb) || jsonb_build_object('subject', i.subject, 'subject_type', i.subject_type, 'evaluation', i.evaluation, 'sentiment', i.sentiment, 'familiarity', i.familiarity) || `+scopestore.NodeAccessMetadataSQL()+`,
                   'impression'
            FROM impressions i
            JOIN memory_nodes n ON n.user_id = i.user_id AND n.id = i.node_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=i.user_id AND scope_node.id=i.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND i.user_id = $1 AND i.id IN (%s)%s
            ORDER BY i.updated_at DESC
        `, inClause, generationClause)
	case domain.MemoryTypeFact:
		generationClause := recordutilstore.GenerationFilterClause("f.generation_id", generationArgIndex, userID, generationID, false)
		args = recordutilstore.AppendGenerationArg(args, userID, generationID)
		query = fmt.Sprintf(`
            SELECT f.id, f.node_id, f.generation_id, f.conversation_id, f.content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, f.updated_at), COALESCE(n.time_tier, ''),
                   f.created_at, f.updated_at,
                   COALESCE(f.metadata, '{}'::jsonb) || jsonb_build_object(
                       'category', f.category,
                       'subject', f.subject,
                       'fact_key', f.fact_key,
                       'lineage_group_id', f.lineage_group_id,
                       'revision_seq', f.revision_seq,
                       'confidence', f.confidence,
                       'is_superseded', f.is_superseded,
                       'valid_from', f.valid_from,
                       'valid_to', f.valid_to,
                       'superseded_by_id', f.superseded_by_id,
                       'keywords', COALESCE((
                           SELECT json_agg(k.keyword ORDER BY k.weight DESC)
                           FROM memory_keywords k
                           WHERE k.summary_id = f.id AND k.user_id = f.user_id
                       ), '[]'::json)
                   ) || `+scopestore.NodeAccessMetadataSQL()+`,
                   'fact'
            FROM facts f
            JOIN memory_nodes n ON n.user_id = f.user_id AND n.id = f.node_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=f.user_id AND scope_node.id=f.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND f.user_id = $1 AND f.id IN (%s)%s
            ORDER BY f.updated_at DESC
        `, inClause, generationClause)
	case domain.MemoryTypeKeyMemory:
		query = fmt.Sprintf(`
            SELECT k.id, k.node_id, '' AS generation_id, '' AS conversation_id, CONCAT(k.key, ': ', k.value) AS content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, k.updated_at), COALESCE(n.time_tier, ''),
                   k.created_at, k.updated_at,
                   jsonb_build_object('key', k.key, 'value', k.value, 'category', k.category, 'source', k.source, 'review_status', k.review_status, 'last_confirmed_at', k.last_confirmed_at) || `+scopestore.NodeAccessMetadataSQL()+`,
                   'key_memory'
            FROM key_memories k
            JOIN memory_nodes n ON n.user_id = k.user_id AND n.id = k.node_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=k.user_id AND scope_node.id=k.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND k.user_id = $1 AND k.id IN (%s) AND k.deleted_at IS NULL
            ORDER BY k.updated_at DESC
        `, inClause)
	case domain.MemoryTypeEpisode, domain.MemoryTypeEvent,
		domain.MemoryTypeTask,
		domain.MemoryTypeProcedure,
		domain.MemoryTypeArtifact,
		domain.MemoryTypeEntity,
		domain.MemoryTypeAgentAction:
		generationClause := recordutilstore.GenerationFilterClause("mi.generation_id", generationArgIndex, userID, generationID, false)
		args = recordutilstore.AppendGenerationArg(args, userID, generationID)
		query = fmt.Sprintf(`
            SELECT mi.id, mi.node_id, mi.generation_id, mi.conversation_id, mi.content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, mi.updated_at), COALESCE(n.time_tier, ''),
                   mi.created_at, mi.updated_at,
                   jsonb_build_object(
                       'item_type', mi.item_type,
                       'title', mi.title,
                       'status', mi.status,
                       'confidence', mi.confidence,
                       'valid_from', mi.valid_from,
                       'valid_to', mi.valid_to,
                       'review_status', mi.review_status
                   ) || COALESCE(mi.metadata, '{}'::jsonb) || `+scopestore.NodeAccessMetadataSQL()+`,
                   mi.item_type
            FROM memory_items mi
            JOIN memory_nodes n ON n.user_id = mi.user_id AND n.id = mi.node_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=mi.user_id AND scope_node.id=mi.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND mi.user_id = $1 AND mi.id IN (%s) AND mi.item_type = '%s' AND mi.deleted_at IS NULL%s
            ORDER BY mi.updated_at DESC
        `, inClause, string(kind), generationClause)
	default:
		return nil, fmt.Errorf("unsupported memory type: %s", kind)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTypedMemoryDocumentRows(ctx, rows, userID)
}

func (r *PostgresMemoryRepository) ListDocumentsByConversation(ctx context.Context, userID, conversationID string) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	return r.ListDocumentsByConversationInGeneration(ctx, userID, "", conversationID)
}

func (r *PostgresMemoryRepository) ListDocumentsByConversationInGeneration(ctx context.Context, userID, generationID, conversationID string) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	return r.ListConversationDocumentsInGeneration(ctx, userID, generationID, conversationID, false)
}

func (r *PostgresMemoryRepository) ListConversationDocumentsInGeneration(ctx context.Context, userID, generationID, conversationID string, includeSegments bool) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	limit := 500
	args := []any{userID, conversationID, limit}
	generationArgIndex := len(args) + 1
	summaryClause := recordutilstore.GenerationFilterClause("s.generation_id", generationArgIndex, userID, generationID, false)
	impressionClause := recordutilstore.GenerationFilterClause("i.generation_id", generationArgIndex, userID, generationID, false)
	factClause := recordutilstore.GenerationFilterClause("f.generation_id", generationArgIndex, userID, generationID, false)
	segmentClause := recordutilstore.GenerationFilterClause("cs.generation_id", generationArgIndex, userID, generationID, false)
	itemClause := recordutilstore.GenerationFilterClause("mi.generation_id", generationArgIndex, userID, generationID, false)
	args = recordutilstore.AppendGenerationArg(args, userID, generationID)

	parts := []string{
		fmt.Sprintf(`
            SELECT s.id, s.node_id, s.generation_id, s.conversation_id, s.summary AS content,
                   COALESCE(n.importance, 1) AS importance, COALESCE(n.decay_factor, 1) AS decay_factor,
                   COALESCE(n.activity_at, s.updated_at) AS activity_at, COALESCE(n.time_tier, '') AS time_tier,
                   s.created_at, s.updated_at,
                   COALESCE(s.metadata, '{}'::jsonb) || jsonb_build_object(
                       'topic_label', s.topic_label,
                       'keywords', COALESCE((
                           SELECT jsonb_agg(k.keyword ORDER BY k.weight DESC)
                           FROM memory_keywords k
                           WHERE k.summary_id = s.id AND k.user_id = s.user_id
                       ), '[]'::jsonb)
                   ) || `+scopestore.NodeAccessMetadataSQL()+` AS metadata,
                   'summary' AS memory_type
            FROM memory_summaries s
            LEFT JOIN memory_nodes n ON n.user_id = s.user_id AND n.id = s.node_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=s.user_id AND scope_node.id=s.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND s.user_id = $1 AND s.conversation_id = $2%s
            ORDER BY s.updated_at DESC
            LIMIT $3
        `, summaryClause),
		fmt.Sprintf(`
            SELECT i.id, i.node_id, i.generation_id, i.conversation_id, i.description AS content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, i.updated_at), COALESCE(n.time_tier, ''),
                   i.created_at, i.updated_at,
                   COALESCE(i.metadata, '{}'::jsonb) || jsonb_build_object('subject', i.subject, 'subject_type', i.subject_type, 'evaluation', i.evaluation, 'sentiment', i.sentiment, 'familiarity', i.familiarity) || `+scopestore.NodeAccessMetadataSQL()+`,
                   'impression'
            FROM impressions i
            LEFT JOIN memory_nodes n ON n.user_id = i.user_id AND n.id = i.node_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=i.user_id AND scope_node.id=i.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND i.user_id = $1 AND i.conversation_id = $2%s
            ORDER BY i.updated_at DESC
            LIMIT $3
        `, impressionClause),
		fmt.Sprintf(`
            SELECT f.id, f.node_id, f.generation_id, f.conversation_id, f.content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, f.updated_at), COALESCE(n.time_tier, ''),
                   f.created_at, f.updated_at,
                   COALESCE(f.metadata, '{}'::jsonb) || jsonb_build_object(
                       'category', f.category,
                       'subject', f.subject,
                       'fact_key', f.fact_key,
                       'lineage_group_id', f.lineage_group_id,
                       'revision_seq', f.revision_seq,
                       'confidence', f.confidence,
                       'is_superseded', f.is_superseded,
                       'valid_from', f.valid_from,
                       'valid_to', f.valid_to,
                       'superseded_by_id', f.superseded_by_id,
                       'keywords', COALESCE((
                           SELECT jsonb_agg(k.keyword ORDER BY k.weight DESC)
                           FROM memory_keywords k
                           WHERE k.summary_id = f.id AND k.user_id = f.user_id
                       ), '[]'::jsonb)
                   ) || `+scopestore.NodeAccessMetadataSQL()+`,
                   'fact'
            FROM facts f
            LEFT JOIN memory_nodes n ON n.user_id = f.user_id AND n.id = f.node_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=f.user_id AND scope_node.id=f.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND f.user_id = $1 AND f.conversation_id = $2%s
            ORDER BY f.updated_at DESC
            LIMIT $3
        `, factClause),
		fmt.Sprintf(`
            SELECT mi.id, mi.node_id, mi.generation_id, mi.conversation_id, mi.content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, mi.updated_at), COALESCE(n.time_tier, ''),
                   mi.created_at, mi.updated_at,
                   jsonb_build_object(
                       'item_type', mi.item_type,
                       'title', mi.title,
                       'status', mi.status,
                       'confidence', mi.confidence,
                       'valid_from', mi.valid_from,
                       'valid_to', mi.valid_to,
                       'review_status', mi.review_status
                   ) || COALESCE(mi.metadata, '{}'::jsonb) || `+scopestore.NodeAccessMetadataSQL()+`,
                   mi.item_type
            FROM memory_items mi
            LEFT JOIN memory_nodes n ON n.user_id = mi.user_id AND n.id = mi.node_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=mi.user_id AND scope_node.id=mi.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND mi.user_id = $1 AND mi.conversation_id = $2 AND mi.deleted_at IS NULL%s
            ORDER BY mi.updated_at DESC
            LIMIT $3
        `, itemClause),
	}
	if includeSegments {
		parts = append(parts, fmt.Sprintf(`
            SELECT cs.id, n.id AS node_id, cs.generation_id, cs.conversation_id, cs.content,
                   COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
                   COALESCE(n.activity_at, cs.updated_at), COALESCE(n.time_tier, ''),
                   cs.created_at, cs.updated_at,
                   jsonb_build_object(
                       'segment_index', cs.segment_index,
                       'start_sequence', cs.start_sequence,
                       'end_sequence', cs.end_sequence,
                       'message_count', cs.message_count,
                       'overlap_from_sequence', cs.overlap_from_sequence,
                       'overlap_message_count', cs.overlap_message_count,
                       'content_hash', cs.content_hash
                   ) || COALESCE(cs.metadata, '{}'::jsonb) || `+scopestore.NodeAccessMetadataSQL()+`,
                   'segment'
            FROM conversation_segments cs
            JOIN memory_nodes n
              ON n.user_id = cs.user_id
             AND n.entity_id = cs.id
             AND n.node_type = 'segment'
            WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND cs.user_id = $1 AND cs.conversation_id = $2%s
            ORDER BY cs.updated_at DESC, cs.segment_index DESC
            LIMIT $3
        `, segmentClause))
	}

	for i := range parts {
		parts[i] = "(" + parts[i] + ")"
	}

	query := fmt.Sprintf(`
        SELECT id, node_id, generation_id, conversation_id, content, importance, decay_factor,
               activity_at, time_tier, created_at, updated_at, metadata, memory_type
        FROM (
            %s
        ) docs
        ORDER BY updated_at DESC
        LIMIT $3
    `, strings.Join(parts, " UNION ALL "))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTypedMemoryDocumentRows(ctx, rows, userID)
}

func (r *PostgresMemoryRepository) SearchKeywordMatches(ctx context.Context, userID, generationID, query string, limit int, allowedTypes ...domain.MemoryType) ([]domain.KeywordMatch, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	terms := normalizeKeywordTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	querySQL, args := lexicalCandidateQuery(ctx, userID, generationID, terms, limit, allowedTypes)

	rows, err := r.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.KeywordMatch, 0)
	for rows.Next() {
		var nodeID string
		var summary string
		var keywordsJSON []byte
		if err := rows.Scan(&nodeID, &summary, &keywordsJSON); err != nil {
			return nil, err
		}
		var keywords []string
		if len(keywordsJSON) > 0 {
			if err := json.Unmarshal(keywordsJSON, &keywords); err != nil {
				return nil, err
			}
		}
		item, ok := buildSummaryKeywordMatch(nodeID, query, summary, keywords)
		if !ok {
			continue
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			if out[i].TotalWeight != out[j].TotalWeight {
				return out[i].TotalWeight > out[j].TotalWeight
			}
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *PostgresMemoryRepository) UpdateDocument(ctx context.Context, userID, id string, kind domain.MemoryType, content string) (domain.MemoryDocument, error) {
	return r.updateDocument(ctx, userID, id, kind, content, nil)
}

func (r *PostgresMemoryRepository) UpdateDocumentConditional(ctx context.Context, userID, id string, kind domain.MemoryType, content string, condition domain.MemoryWriteCondition) (domain.MemoryDocument, error) {
	return r.updateDocument(ctx, userID, id, kind, content, &condition)
}

func (r *PostgresMemoryRepository) updateDocument(ctx context.Context, userID, id string, kind domain.MemoryType, content string, condition *domain.MemoryWriteCondition) (domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryDocument{}, err
	}
	now := time.Now().UTC()
	var query string
	switch kind {
	case domain.MemoryTypeSummary:
		query = `UPDATE memory_summaries SET summary = $1, updated_at = $2 WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=memory_summaries.user_id AND scope_node.id=memory_summaries.node_id AND ` + storeaccess.NodeSQL(ctx, "scope_node") + `) AND id = $3 AND user_id = $4`
	case domain.MemoryTypeImpression:
		query = `UPDATE impressions SET description = $1, updated_at = $2 WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=impressions.user_id AND scope_node.id=impressions.node_id AND ` + storeaccess.NodeSQL(ctx, "scope_node") + `) AND id = $3 AND user_id = $4`
	case domain.MemoryTypeFact:
		query = `UPDATE facts SET content = $1, updated_at = $2 WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=facts.user_id AND scope_node.id=facts.node_id AND ` + storeaccess.NodeSQL(ctx, "scope_node") + `) AND id = $3 AND user_id = $4`
	case domain.MemoryTypeEpisode, domain.MemoryTypeEvent,
		domain.MemoryTypeTask,
		domain.MemoryTypeProcedure,
		domain.MemoryTypeArtifact,
		domain.MemoryTypeEntity,
		domain.MemoryTypeAgentAction:
		query = `UPDATE memory_items SET content = $1, updated_at = $2 WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=memory_items.user_id AND scope_node.id=memory_items.node_id AND ` + storeaccess.NodeSQL(ctx, "scope_node") + `) AND id = $3 AND user_id = $4 AND item_type = $5 AND deleted_at IS NULL`
	default:
		return domain.MemoryDocument{}, fmt.Errorf("unsupported memory type: %s", kind)
	}
	generation := ""
	if condition != nil {
		generation = condition.GenerationID
	}
	document, err := r.GetDocumentInGeneration(ctx, userID, generation, id, kind)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	nodeID := document.NodeID
	generationID := document.GenerationID
	embeddingRevision := now.UnixNano()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	defer dbutilstore.Rollback(tx)
	if condition != nil {
		writable, err := memorygeneration.LockWritableGeneration(ctx, tx, userID, security.WorkspaceID(ctx, userID), generation)
		if err != nil {
			return domain.MemoryDocument{}, err
		}
		if !writable {
			return domain.MemoryDocument{}, domain.ErrAccessConflict
		}
	}
	if err := scopestore.LockMemoryIdentityTx(ctx, tx, userID, security.WorkspaceID(ctx, userID), nodeID, id, string(kind)); err != nil {
		return domain.MemoryDocument{}, err
	}
	args := []any{content, now, id, userID}
	if domain.IsGenericMemoryType(kind) {
		args = append(args, string(kind))
	}
	if condition != nil {
		if condition.UpdatedAt.IsZero() || condition.GenerationID == "" {
			return domain.MemoryDocument{}, domain.ErrAccessConflict
		}
		args = append(args, condition.UpdatedAt, condition.GenerationID)
		query += fmt.Sprintf(" AND updated_at=$%d AND generation_id=$%d", len(args)-1, len(args))
		if kind == domain.MemoryTypeFact {
			query += " AND NOT is_superseded AND superseded_by_id IS NULL"
		}
	}
	query = strings.Replace(query, "updated_at = $2", "updated_at = GREATEST($2, updated_at + interval '1 microsecond')", 1)
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return domain.MemoryDocument{}, err
	} else if affected != 1 {
		return domain.MemoryDocument{}, domain.ErrAccessConflict
	}

	if _, err := tx.ExecContext(ctx, `
	        UPDATE memory_nodes
	        SET activity_at = $1,
	            time_tier = $2,
	            embedding_status = 'pending',
             embedding_revision = $6,
             revision=revision+1,
	            updated_at = $1
	        WHERE user_id = $3 AND entity_id = $4 AND node_type = $5 AND workspace_id=`+storeaccess.WorkspaceSQL(ctx, "user_id")+`
	    `, now, string(domain.ClassifyTimeTier(now, now)), userID, id, string(kind), embeddingRevision); err != nil {
		return domain.MemoryDocument{}, err
	}
	if err := dbutilstore.InsertMemoryMutationOutboxTx(ctx, tx, domain.OutboxEvent{
		ID:            fmt.Sprintf("%s:%s:%d", domain.TaskTypeEmbeddingGenerate, nodeID, embeddingRevision),
		UserID:        userID,
		EventType:     domain.TaskTypeEmbeddingGenerate,
		AggregateType: "memory_node",
		AggregateID:   nodeID,
		Payload: map[string]any{
			"user_id":            userID,
			"generation_id":      generationID,
			"node_id":            nodeID,
			"node_type":          string(kind),
			"embedding_revision": embeddingRevision,
		},
		CreatedAt: now,
	}); err != nil {
		return domain.MemoryDocument{}, err
	}
	if condition != nil {
		if err := memoryEditAuditTx(ctx, tx, userID, "update_memory_document", string(kind), id, condition); err != nil {
			return domain.MemoryDocument{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.MemoryDocument{}, err
	}
	return r.GetDocumentInGeneration(ctx, userID, generation, id, kind)
}

func (r *PostgresMemoryRepository) HasTransactionalMemoryOutbox() bool { return true }

func (r *PostgresMemoryRepository) ListFactLineage(ctx context.Context, userID, generationID, factID string) (domain.FactLineage, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.FactLineage{}, err
	}
	anchor, err := r.GetDocumentInGeneration(ctx, userID, generationID, factID, domain.MemoryTypeFact)
	if err != nil {
		return domain.FactLineage{}, err
	}
	lineageGroupID := factLineageGroupIDFromMetadata(anchor.Metadata)
	if lineageGroupID == "" {
		entry := factLineageEntryFromDocument(anchor)
		entry.IsHead = true
		return domain.FactLineage{
			CurrentHeadID:  anchor.ID,
			FactKey:        factKeyFromMetadata(anchor.Metadata),
			LineageGroupID: lineageGroupID,
			Entries:        []domain.FactLineageEntry{entry},
			RevisionDepth:  1,
			LineagePath:    []string{anchor.ID},
		}, nil
	}
	return r.ListFactLineageByKey(ctx, userID, anchor.GenerationID, lineageGroupID)
}

func (r *PostgresMemoryRepository) ListFactLineageByKey(ctx context.Context, userID, generationID, factKey string) (domain.FactLineage, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.FactLineage{}, err
	}
	lineageGroupID := strings.TrimSpace(factKey)
	if !strings.HasPrefix(lineageGroupID, "fact-lineage:") {
		lineageGroupID = "fact-lineage:" + strings.TrimSpace(userID) + ":" + sanitizeLineageKey(factKey)
	}
	docs, err := r.listFactsByLineageGroup(ctx, userID, generationID, lineageGroupID)
	if err != nil {
		return domain.FactLineage{}, err
	}
	entries := make([]domain.FactLineageEntry, 0, len(docs))
	for _, doc := range docs {
		entries = append(entries, factLineageEntryFromDocument(doc))
	}
	lineage := factLineageFromEntries(entries)
	for idx := range lineage.Entries {
		lineage.Entries[idx].IsHead = lineage.Entries[idx].FactID == lineage.CurrentHeadID
	}
	return lineage, nil
}

func (r *PostgresMemoryRepository) listFactsByLineageGroup(ctx context.Context, userID, generationID, lineageGroupID string) ([]domain.MemoryDocument, error) {
	query := `
        SELECT f.id, f.node_id, f.generation_id, f.conversation_id, f.content,
               COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1),
               COALESCE(n.activity_at, f.updated_at), COALESCE(n.time_tier, ''),
               f.created_at, f.updated_at,
               COALESCE(f.metadata, '{}'::jsonb) || jsonb_build_object(
                   'category', f.category,
                   'subject', f.subject,
                   'fact_key', f.fact_key,
                   'lineage_group_id', f.lineage_group_id,
                   'revision_seq', f.revision_seq,
                   'confidence', f.confidence,
                   'is_superseded', f.is_superseded,
                   'valid_from', f.valid_from,
                   'valid_to', f.valid_to,
                   'superseded_by_id', f.superseded_by_id,
                   'keywords', COALESCE((
                       SELECT json_agg(k.keyword ORDER BY k.weight DESC)
                       FROM memory_keywords k
                       WHERE k.summary_id = f.id AND k.user_id = f.user_id
                   ), '[]'::json)
               ) || ` + scopestore.NodeAccessMetadataSQL() + `
        FROM facts f
        LEFT JOIN memory_nodes n ON n.user_id = f.user_id AND n.id = f.node_id
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=f.user_id AND scope_node.id=f.node_id AND ` + storeaccess.NodeSQL(ctx, "scope_node") + `) AND f.user_id = $1 AND COALESCE(f.lineage_group_id, '') = $2` + recordutilstore.GenerationFilterClause("f.generation_id", 3, userID, generationID, false) + `
        ORDER BY f.revision_seq ASC, f.created_at ASC, f.id ASC
    `
	args := []any{userID, lineageGroupID}
	args = recordutilstore.AppendGenerationArg(args, userID, generationID)
	rows, err := r.db.QueryContext(ctx, query, args...)
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
		item.WorkspaceID = security.WorkspaceID(ctx, userID)
		item.Type = domain.MemoryTypeFact
		if err := rows.Scan(&item.ID, &item.NodeID, &item.GenerationID, &item.ConversationID, &item.Content, &item.Importance, &item.DecayFactor, &item.ActivityAt, &timeTier, &item.CreatedAt, &item.UpdatedAt, &metadataBytes); err != nil {
			return nil, err
		}
		if len(metadataBytes) > 0 {
			item.Metadata = map[string]any{}
			if err := json.Unmarshal(metadataBytes, &item.Metadata); err != nil {
				return nil, err
			}
		}
		item.TimeTier = domain.ParseTimeTier(timeTier)
		item = attachDocumentRuntimeMetadata(ctx, item)
		out = append(out, item)
	}
	return out, rows.Err()
}

func nextFactRevisionSeqTx(ctx context.Context, tx *sqlpkg.Tx, userID, generationID, lineageGroupID, factID string, createdAt time.Time) (int, error) {
	if lineageGroupID == "" {
		return 1, nil
	}
	var existingSeq int
	switch err := tx.QueryRowContext(ctx, `
        SELECT COALESCE(revision_seq, 0)
        FROM facts
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=facts.user_id AND scope_node.id=facts.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND generation_id = $2 AND id = $3 AND COALESCE(lineage_group_id, '') = $4
    `, userID, generationID, factID, lineageGroupID).Scan(&existingSeq); err {
	case nil:
		if existingSeq > 0 {
			return existingSeq, nil
		}
	case sqlpkg.ErrNoRows:
	default:
		return 0, err
	}

	var maxSeq int
	if err := tx.QueryRowContext(ctx, `
        SELECT COALESCE(MAX(revision_seq), 0)
        FROM facts
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=facts.user_id AND scope_node.id=facts.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND generation_id = $2 AND COALESCE(lineage_group_id, '') = $3
    `, userID, generationID, lineageGroupID).Scan(&maxSeq); err != nil {
		return 0, err
	}
	if maxSeq <= 0 && !createdAt.IsZero() {
		return 1, nil
	}
	return maxSeq + 1, nil
}

func lockFactLineageTx(ctx context.Context, tx *sqlpkg.Tx, userID, generationID, lineageGroupID string) error {
	if tx == nil || strings.TrimSpace(lineageGroupID) == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
        SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))
	`, strings.TrimSpace(userID)+"\x1f"+strings.TrimSpace(generationID), strings.TrimSpace(lineageGroupID))
	return err
}

func existingFactSupersessionStateTx(ctx context.Context, tx *sqlpkg.Tx, userID, generationID, factID string) (bool, string, error) {
	var isSuperseded bool
	var supersededByID string
	switch err := tx.QueryRowContext(ctx, `
        SELECT is_superseded, COALESCE(superseded_by_id, '')
        FROM facts
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=facts.user_id AND scope_node.id=facts.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND generation_id = $2 AND id = $3
    `, userID, generationID, factID).Scan(&isSuperseded, &supersededByID); err {
	case nil:
		return isSuperseded, supersededByID, nil
	case sqlpkg.ErrNoRows:
		return false, "", nil
	default:
		return false, "", err
	}
}

func factSupersessionWouldCycleTx(ctx context.Context, tx *sqlpkg.Tx, userID, generationID, newerFactID, olderFactID string) (bool, error) {
	newerFactID = strings.TrimSpace(newerFactID)
	olderFactID = strings.TrimSpace(olderFactID)
	if newerFactID == "" || olderFactID == "" {
		return false, nil
	}
	if newerFactID == olderFactID {
		return true, nil
	}
	var exists bool
	err := tx.QueryRowContext(ctx, `
        WITH RECURSIVE chain(id, superseded_by_id, depth) AS (
            SELECT id, superseded_by_id, 0
            FROM facts
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=facts.user_id AND scope_node.id=facts.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND generation_id = $2 AND id = $3
            UNION ALL
            SELECT f.id, f.superseded_by_id, chain.depth + 1
            FROM facts f
            JOIN chain ON f.id = chain.superseded_by_id
            WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=f.user_id AND scope_node.id=f.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND f.user_id = $1
              AND f.generation_id = $2
              AND chain.depth < 128
              AND chain.superseded_by_id IS NOT NULL
              AND chain.superseded_by_id <> ''
        )
        SELECT EXISTS(SELECT 1 FROM chain WHERE id = $4)
    `, userID, generationID, newerFactID, olderFactID).Scan(&exists)
	return exists, err
}

func currentFactLineageHeadIDsTx(ctx context.Context, tx *sqlpkg.Tx, userID, generationID, lineageGroupID, excludeFactID string) ([]string, error) {
	if lineageGroupID == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `
        SELECT id
        FROM facts
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=facts.user_id AND scope_node.id=facts.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1
          AND generation_id = $2
          AND COALESCE(lineage_group_id, '') = $3
          AND id <> $4
          AND is_superseded = FALSE
          AND superseded_by_id IS NULL
        ORDER BY revision_seq DESC, updated_at DESC
    `, userID, generationID, lineageGroupID, excludeFactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func syncFactKeywordsTx(ctx context.Context, tx *sqlpkg.Tx, doc domain.MemoryDocument) error {
	keywords := uniqueSortedStrings(append(dbutilstore.StringSlice(doc.Metadata, "keywords"), factKeyFromMetadata(doc.Metadata)))
	rows := make([]memoryKeywordRow, 0, len(keywords))
	for idx, keyword := range keywords {
		rows = append(rows, memoryKeywordRow{
			ID:        fmt.Sprintf("%s:fact:%d", doc.ID, idx),
			UserID:    doc.UserID,
			SummaryID: doc.ID,
			Keyword:   keyword,
			Weight:    1.0 - float64(idx)*0.08,
			CreatedAt: doc.CreatedAt,
			UpdatedAt: doc.UpdatedAt,
		})
	}
	return replaceMemoryKeywordsTx(ctx, tx, doc.UserID, doc.ID, rows)
}

type memoryKeywordRow struct {
	ID        string
	UserID    string
	SummaryID string
	Keyword   string
	Weight    float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func replaceMemoryKeywordsTx(ctx context.Context, tx *sqlpkg.Tx, userID, summaryID string, rows []memoryKeywordRow) error {
	if tx == nil || userID == "" || summaryID == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_keywords WHERE summary_id = $1 AND user_id = $2`, summaryID, userID); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	for start := 0; start < len(rows); start += memoryKeywordInsertBatchSize {
		end := min(start+memoryKeywordInsertBatchSize, len(rows))
		args := make([]any, 0, (end-start)*7)
		valueGroups := make([]string, 0, end-start)
		for _, row := range rows[start:end] {
			base := len(args) + 1
			valueGroups = append(valueGroups, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d)", base, base+1, base+2, base+3, base+4, base+5, base+6))
			args = append(args, row.ID, row.UserID, row.SummaryID, row.Keyword, row.Weight, row.CreatedAt, row.UpdatedAt)
		}
		query := `
            INSERT INTO memory_keywords (id, user_id, summary_id, keyword, weight, created_at, updated_at)
            VALUES ` + strings.Join(valueGroups, ",")
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

func insertMemoryEvidencesTx(ctx context.Context, tx *sqlpkg.Tx, evidences []domain.MemoryEvidence, merge bool) error {
	if tx == nil || len(evidences) == 0 {
		return nil
	}
	for start := 0; start < len(evidences); start += memoryEvidenceInsertBatchSize {
		end := min(start+memoryEvidenceInsertBatchSize, len(evidences))
		args := make([]any, 0, (end-start)*16)
		valueGroups := make([]string, 0, end-start)
		for _, evidence := range evidences[start:end] {
			base := len(args) + 1
			valueGroups = append(valueGroups, fmt.Sprintf(
				"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base, base+1, base+2, base+3, base+4, base+5, base+6, base+7,
				base+8, base+9, base+10, base+11, base+12, base+13, base+14, base+15,
			))
			args = append(args,
				evidence.ID,
				evidence.GenerationID,
				evidence.UserID,
				evidence.NodeID,
				evidence.ConversationID,
				evidence.SequenceNum,
				dbutilstore.NullString(evidence.PartID),
				dbutilstore.NullString(evidence.ArtifactID),
				evidence.PageIndex,
				evidence.StartMS,
				evidence.EndMS,
				evidence.EvidenceKind,
				dbutilstore.NullString(evidence.ExcerptText),
				evidence.Weight,
				dbutilstore.MarshalJSON(evidence.Metadata),
				evidence.CreatedAt,
			)
		}
		query := `
	            INSERT INTO memory_evidences (
                id, generation_id, user_id, node_id, conversation_id, sequence_num, part_id, artifact_id,
                page_index, start_ms, end_ms, evidence_kind, excerpt_text, weight, metadata, created_at
	            ) VALUES ` + strings.Join(valueGroups, ",")
		if merge {
			query += ` ON CONFLICT (id) DO UPDATE SET
				generation_id = EXCLUDED.generation_id,
				conversation_id = EXCLUDED.conversation_id,
				sequence_num = EXCLUDED.sequence_num,
				part_id = EXCLUDED.part_id,
				artifact_id = EXCLUDED.artifact_id,
				page_index = EXCLUDED.page_index,
				start_ms = EXCLUDED.start_ms,
				end_ms = EXCLUDED.end_ms,
				evidence_kind = EXCLUDED.evidence_kind,
				excerpt_text = EXCLUDED.excerpt_text,
				weight = EXCLUDED.weight,
				metadata = EXCLUDED.metadata
			WHERE memory_evidences.user_id = EXCLUDED.user_id AND memory_evidences.node_id = EXCLUDED.node_id`
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

func attachDocumentRuntimeMetadata(ctx context.Context, item domain.MemoryDocument) domain.MemoryDocument {
	item.WorkspaceID = security.WorkspaceID(ctx, item.UserID)
	// Reads decorate a snapshot, never the repository's shared metadata map.
	item.Metadata = recordutilstore.CloneMetadata(item.Metadata)
	scopestore.HydrateDocumentAccess(&item)
	item.Metadata["activity_at"] = item.ActivityAt.Format(time.RFC3339)
	item.Metadata["time_tier"] = item.TimeTier
	item.Metadata["temporal"] = temporalMetadata(ctx, item.UserID, item.ActivityAt, item.TimeTier)
	if item.GenerationID != "" {
		item.Metadata["generation_id"] = item.GenerationID
	}
	if item.Type == domain.MemoryTypeFact {
		item.Lineage = &domain.MemoryLineageMeta{
			FactKey:        factKeyFromMetadata(item.Metadata),
			LineageGroupID: factLineageGroupIDFromMetadata(item.Metadata),
			RevisionSeq:    factLineageRevisionSeqFromMetadata(item.Metadata),
			GenerationID:   item.GenerationID,
			CurrentHeadID:  item.ID,
		}
	}
	return item
}

func (r *PostgresMemoryRepository) ListKeyMemories(ctx context.Context, userID string, limit int) ([]domain.KeyMemory, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
	        SELECT k.id, k.node_id, k.key, k.value, k.category, k.source, k.review_status, k.last_confirmed_at, k.created_at, k.updated_at,
	               COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1), COALESCE(n.activity_at, k.updated_at), COALESCE(n.time_tier, ''),n.partition_id
	        FROM key_memories k
	        LEFT JOIN memory_nodes n ON n.user_id = k.user_id AND n.id = k.node_id
	        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=k.user_id AND scope_node.id=k.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND k.user_id = $1 AND k.deleted_at IS NULL
	        ORDER BY k.updated_at DESC
	        LIMIT $2
	    `, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.KeyMemory, 0)
	for rows.Next() {
		item, err := scanKeyMemoryRow(ctx, rows, userID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PostgresMemoryRepository) GetKeyMemory(ctx context.Context, userID, memoryKey string) (domain.KeyMemory, bool, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.KeyMemory{}, false, err
	}
	row := r.db.QueryRowContext(ctx, `
	        SELECT k.id, k.node_id, k.key, k.value, k.category, k.source, k.review_status, k.last_confirmed_at, k.created_at, k.updated_at,
	               COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1), COALESCE(n.activity_at, k.updated_at), COALESCE(n.time_tier, ''),n.partition_id
	        FROM key_memories k
	        LEFT JOIN memory_nodes n ON n.user_id = k.user_id AND n.id = k.node_id
	        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=k.user_id AND scope_node.id=k.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND k.user_id = $1 AND k.key = $2 AND k.deleted_at IS NULL
	    `, userID, strings.TrimSpace(memoryKey))
	item, err := scanKeyMemoryRow(ctx, row, userID)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.KeyMemory{}, false, nil
	}
	if err != nil {
		return domain.KeyMemory{}, false, err
	}
	return item, true, nil
}

func (r *PostgresMemoryRepository) GetKeyMemoryByID(ctx context.Context, userID, id string) (domain.KeyMemory, bool, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.KeyMemory{}, false, err
	}
	row := r.db.QueryRowContext(ctx, `
	        SELECT k.id, k.node_id, k.key, k.value, k.category, k.source, k.review_status, k.last_confirmed_at, k.created_at, k.updated_at,
	               COALESCE(n.importance, 1), COALESCE(n.decay_factor, 1), COALESCE(n.activity_at, k.updated_at), COALESCE(n.time_tier, ''),n.partition_id
	        FROM key_memories k
	        LEFT JOIN memory_nodes n ON n.user_id = k.user_id AND n.id = k.node_id
	        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=k.user_id AND scope_node.id=k.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND k.user_id = $1 AND k.id = $2 AND k.deleted_at IS NULL
	    `, userID, strings.TrimSpace(id))
	item, err := scanKeyMemoryRow(ctx, row, userID)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.KeyMemory{}, false, nil
	}
	if err != nil {
		return domain.KeyMemory{}, false, err
	}
	return item, true, nil
}

func (r *PostgresMemoryRepository) CountKeyMemories(ctx context.Context, userID string) (int, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return 0, err
	}
	var count int
	if err := r.db.QueryRowContext(ctx, `
	        SELECT COUNT(*)
	        FROM key_memories
	        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=key_memories.user_id AND scope_node.id=key_memories.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND deleted_at IS NULL
	    `, userID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *PostgresMemoryRepository) SearchKeyMemories(ctx context.Context, input domain.KeyMemoryQuery) ([]domain.KeyMemory, error) {
	if _, err := security.DataScope(ctx, input.UserID, input.WorkspaceID); err != nil {
		return nil, err
	}
	input.WorkspaceID = security.WorkspaceID(ctx, input.UserID)
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

type keyMemoryScanner interface {
	Scan(dest ...any) error
}

func scanKeyMemoryRow(ctx context.Context, row keyMemoryScanner, userID string) (domain.KeyMemory, error) {
	var item domain.KeyMemory
	var nodeID string
	var lastConfirmedAt sqlpkg.NullTime
	var timeTier string
	item.UserID = userID
	item.WorkspaceID = security.WorkspaceID(ctx, userID)
	if err := row.Scan(&item.ID, &nodeID, &item.Key, &item.Value, &item.Category, &item.Source, &item.ReviewStatus, &lastConfirmedAt, &item.CreatedAt, &item.UpdatedAt, &item.Importance, &item.DecayFactor, &item.ActivityAt, &timeTier, &item.PartitionID); err != nil {
		return domain.KeyMemory{}, err
	}
	item.Metadata = map[string]any{"node_id": nodeID}
	item.TimeTier = domain.ParseTimeTier(timeTier)
	item.EffectiveScore = item.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, item.ActivityAt, item.DecayFactor, item.Importance, time.Now().UTC())
	if lastConfirmedAt.Valid {
		item.LastConfirmedAt = &lastConfirmedAt.Time
	}
	return item, nil
}

func (r *PostgresMemoryRepository) UpsertKeyMemory(ctx context.Context, km domain.KeyMemory) error {
	return r.upsertKeyMemory(ctx, km, nil)
}

func (r *PostgresMemoryRepository) UpsertKeyMemoryConditional(ctx context.Context, km domain.KeyMemory, condition domain.MemoryWriteCondition) error {
	return r.upsertKeyMemory(ctx, km, &condition)
}

func (r *PostgresMemoryRepository) upsertKeyMemory(ctx context.Context, km domain.KeyMemory, condition *domain.MemoryWriteCondition) error {
	if _, err := security.DataScope(ctx, km.UserID, km.WorkspaceID); err != nil {
		return err
	}
	km.WorkspaceID = security.WorkspaceID(ctx, km.UserID)
	km.PartitionID = security.WritePartition(ctx)
	km.Key = strings.TrimSpace(km.Key)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	if err = lockKeyMemoryEditTx(ctx, tx, km.UserID, km.Key); err != nil {
		return err
	}
	var existingID string
	var existingNodeID string
	var existingCreatedAt time.Time
	var existingCategory sqlpkg.NullString
	var existingSource sqlpkg.NullString
	var existingReviewStatus sqlpkg.NullString
	var existingLastConfirmedAt sqlpkg.NullTime
	var existingUpdatedAt time.Time
	var deletedAt sqlpkg.NullTime
	switch err := tx.QueryRowContext(ctx, `
        SELECT id, node_id, created_at, category, source, review_status, last_confirmed_at, updated_at, deleted_at
        FROM key_memories
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=key_memories.user_id AND scope_node.id=key_memories.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND key = $2
    `, km.UserID, km.Key).Scan(&existingID, &existingNodeID, &existingCreatedAt, &existingCategory, &existingSource, &existingReviewStatus, &existingLastConfirmedAt, &existingUpdatedAt, &deletedAt); err {
	case nil:
		if condition != nil {
			if condition.Create {
				if !deletedAt.Valid {
					return domain.ErrAccessConflict
				}
			} else if deletedAt.Valid || condition.ID != existingID || !condition.UpdatedAt.Equal(existingUpdatedAt) {
				return domain.ErrAccessConflict
			}
		}
		km.ID = existingID
		if km.CreatedAt.IsZero() {
			km.CreatedAt = existingCreatedAt
		}
		if km.Category == "" && condition == nil {
			km.Category = existingCategory.String
		}
		if km.Source == "" {
			km.Source = existingSource.String
		}
		if km.ReviewStatus == "" {
			km.ReviewStatus = existingReviewStatus.String
		}
		if km.LastConfirmedAt == nil && existingLastConfirmedAt.Valid {
			km.LastConfirmedAt = &existingLastConfirmedAt.Time
		}
	case sqlpkg.ErrNoRows:
		if condition != nil && !condition.Create {
			return domain.ErrAccessConflict
		}
	default:
		return err
	}
	if km.ID == "" {
		km.ID = uuid.NewString()
	}
	nodeID := dbutilstore.StringValue(km.Metadata, "node_id")
	if existingNodeID != "" {
		nodeID = existingNodeID
	}
	if nodeID == "" {
		nodeID = keyMemoryNodeID(ctx, km.UserID, km.Key)
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

	sourceDoc := domain.MemoryDocument{UserID: km.UserID, WorkspaceID: km.WorkspaceID, PartitionID: km.PartitionID, NodeID: nodeID, Type: domain.MemoryTypeKeyMemory, ConversationID: dbutilstore.StringValue(km.Metadata, "conversation_id"), SourceRefs: km.SourceRefs, SubjectActorIDs: km.SubjectActorIDs}
	if err = ingestionstore.BindIngestEvidenceTx(ctx, tx, &sourceDoc); err != nil {
		return err
	}
	if err = scopestore.LockMemoryIdentityTx(ctx, tx, km.UserID, km.WorkspaceID, nodeID, km.ID, string(domain.MemoryTypeKeyMemory)); err != nil {
		return err
	}
	if err = scopestore.EnsureMemoryWorkspaceTx(ctx, tx, km.UserID); err != nil {
		return err
	}

	if existingNodeID != "" && existingNodeID != nodeID {
		if err := deleteMemoryEvidencesByNodeIDsTx(ctx, tx, km.UserID, []string{existingNodeID}); err != nil {
			return err
		}
		if err := deleteRelationsByNodeIDsTx(ctx, tx, km.UserID, []string{existingNodeID}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
            DELETE FROM memory_embeddings
            WHERE user_id = $1 AND node_id = $2
        `, km.UserID, existingNodeID); err != nil {
			return err
		}
		if err := deleteMemoryNodesByIDsTx(ctx, tx, km.UserID, []string{existingNodeID}); err != nil {
			return err
		}
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(existingUpdatedAt) {
		now = existingUpdatedAt.Add(time.Microsecond)
	}
	if km.CreatedAt.IsZero() {
		km.CreatedAt = now
	}
	embeddingRevision := now.UnixNano()
	_, err = tx.ExecContext(ctx, `
		        INSERT INTO memory_nodes (id, generation_id, user_id, node_type, entity_id, importance, decay_factor, activity_at, time_tier, embedding_status, embedding_revision, created_at, updated_at, workspace_id,partition_id)
		        VALUES ($1,'',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		        ON CONFLICT (id) DO UPDATE SET
		            importance = EXCLUDED.importance,
		            decay_factor = EXCLUDED.decay_factor,
		            activity_at = EXCLUDED.activity_at,
		            time_tier = EXCLUDED.time_tier,
		            embedding_status = 'pending',
		            embedding_revision = EXCLUDED.embedding_revision,
		            updated_at = EXCLUDED.updated_at
		        WHERE memory_nodes.user_id = EXCLUDED.user_id AND memory_nodes.workspace_id = EXCLUDED.workspace_id
		`, nodeID, km.UserID, string(domain.MemoryTypeKeyMemory), km.ID, km.Importance, km.DecayFactor, km.ActivityAt, string(km.TimeTier), "pending", embeddingRevision, km.CreatedAt, now, km.WorkspaceID, km.PartitionID)
	if err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `UPDATE memory_nodes SET source_conversation_id=$2,source_refs=$3,subject_actor_ids=$4 WHERE id=$1`, nodeID, sourceDoc.ConversationID, dbutilstore.MarshalJSON(sourceDoc.SourceRefs), dbutilstore.MarshalJSON(sourceDoc.SubjectActorIDs)); err != nil {
		return err
	}
	if err = ingestionstore.PersistIngestDependenciesTx(ctx, tx, sourceDoc); err != nil {
		return err
	}

	keyResult, err := tx.ExecContext(ctx, `
        INSERT INTO key_memories (id, user_id, node_id, key, value, category, source, review_status, last_confirmed_at, created_at, updated_at,workspace_id,partition_id)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	        ON CONFLICT (user_id, workspace_id, partition_id, key) DO UPDATE SET
	            value = EXCLUDED.value,
	            category = EXCLUDED.category,
	            source = EXCLUDED.source,
	            review_status = EXCLUDED.review_status,
	            last_confirmed_at = EXCLUDED.last_confirmed_at,
	            deleted_at = NULL,
	            updated_at = EXCLUDED.updated_at
            WHERE key_memories.node_id = EXCLUDED.node_id
	    `, km.ID, km.UserID, nodeID, km.Key, km.Value, km.Category, km.Source, dbutilstore.DefaultString(km.ReviewStatus, "pending"), km.LastConfirmedAt, now, now, km.WorkspaceID, km.PartitionID)
	if err != nil {
		return err
	}
	if count, countErr := keyResult.RowsAffected(); countErr != nil {
		return countErr
	} else if count != 1 {
		return domain.ErrAccessDenied
	}
	if err = syncTemporalRelationsTx(ctx, tx, km.UserID, "", nodeID, km.ActivityAt, km.TimeTier, now); err != nil {
		return err
	}
	for _, event := range []domain.OutboxEvent{
		{
			ID:            fmt.Sprintf("%s:%s:%d", domain.TaskTypeEmbeddingGenerate, nodeID, embeddingRevision),
			UserID:        km.UserID,
			EventType:     domain.TaskTypeEmbeddingGenerate,
			AggregateType: "memory_node",
			AggregateID:   nodeID,
			Payload: map[string]any{
				"user_id":            km.UserID,
				"node_id":            nodeID,
				"node_type":          string(domain.MemoryTypeKeyMemory),
				"embedding_revision": embeddingRevision,
			},
			CreatedAt: now,
		},
		{
			ID:            fmt.Sprintf("%s:%s:%d", domain.TaskTypeKeyMemoryGraphSync, nodeID, embeddingRevision),
			UserID:        km.UserID,
			EventType:     domain.TaskTypeKeyMemoryGraphSync,
			AggregateType: "key_memory",
			AggregateID:   nodeID,
			Payload: map[string]any{
				"user_id": km.UserID,
				"node_id": nodeID,
				"key":     km.Key,
			},
			CreatedAt: now,
		},
	} {
		if err = dbutilstore.InsertMemoryMutationOutboxTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if condition != nil {
		action := "update_key_memory"
		if condition.Create {
			action = "create_key_memory"
		}
		if err = memoryEditAuditTx(ctx, tx, km.UserID, action, "key_memory", km.ID, condition); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *PostgresMemoryRepository) DeleteKeyMemory(ctx context.Context, userID, key string) error {
	return r.deleteKeyMemory(ctx, userID, key, nil)
}

func (r *PostgresMemoryRepository) DeleteKeyMemoryConditional(ctx context.Context, userID, key string, condition domain.MemoryWriteCondition) error {
	return r.deleteKeyMemory(ctx, userID, key, &condition)
}

func (r *PostgresMemoryRepository) deleteKeyMemory(ctx context.Context, userID, key string, condition *domain.MemoryWriteCondition) error {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	if err = lockKeyMemoryEditTx(ctx, tx, userID, key); err != nil {
		return err
	}
	var nodeID, id string
	var updatedAt time.Time
	if err = tx.QueryRowContext(ctx, `SELECT node_id, id, updated_at FROM key_memories WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=key_memories.user_id AND scope_node.id=key_memories.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND key = $2 AND deleted_at IS NULL`, userID, key).Scan(&nodeID, &id, &updatedAt); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			if condition != nil {
				return domain.ErrAccessConflict
			}
			return tx.Commit()
		}
		return err
	}
	if condition != nil && (condition.ID != id || !condition.UpdatedAt.Equal(updatedAt)) {
		return domain.ErrAccessConflict
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE key_memories SET deleted_at = $1, updated_at = $1 WHERE user_id = $2 AND key = $3`, now, userID, key); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM memory_relations WHERE user_id = $1 AND (src_node_id = $2 OR dst_node_id = $2)`, userID, nodeID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM memory_embeddings WHERE user_id = $1 AND node_id = $2`, userID, nodeID); err != nil {
		return err
	}
	var deletionRevision int64
	if err = tx.QueryRowContext(ctx, `UPDATE memory_nodes SET embedding_status = 'deleted', embedding_revision = embedding_revision + 1, updated_at = $1 WHERE user_id = $2 AND id = $3 RETURNING embedding_revision`, now, userID, nodeID).Scan(&deletionRevision); err != nil {
		return err
	}
	if err = dbutilstore.InsertMemoryMutationOutboxTx(ctx, tx, domain.OutboxEvent{
		ID:            fmt.Sprintf("%s:%s:%d", domain.TaskTypeEmbeddingDelete, nodeID, now.UnixNano()),
		UserID:        userID,
		EventType:     domain.TaskTypeEmbeddingDelete,
		AggregateType: "memory_node",
		AggregateID:   nodeID,
		Payload: map[string]any{
			"user_id":            userID,
			"node_id":            nodeID,
			"embedding_revision": deletionRevision,
		},
		CreatedAt: now,
	}); err != nil {
		return err
	}
	if condition != nil {
		if err = memoryEditAuditTx(ctx, tx, userID, "delete_key_memory", "key_memory", id, condition); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *PostgresMemoryRepository) LinkDocuments(ctx context.Context, relation domain.MemoryRelation) error {
	if _, err := security.DataScope(ctx, relation.UserID, relation.WorkspaceID); err != nil {
		return err
	}
	relation.WorkspaceID = security.WorkspaceID(ctx, relation.UserID)
	relation.SrcNodeID = strings.TrimSpace(relation.SrcNodeID)
	relation.DstNodeID = strings.TrimSpace(relation.DstNodeID)
	relation.RelationType = strings.TrimSpace(relation.RelationType)
	if relation.UserID == "" || relation.SrcNodeID == "" || relation.DstNodeID == "" || relation.RelationType == "" {
		return errors.New("relation user, endpoints, and type are required")
	}
	if relation.SrcNodeID == relation.DstNodeID {
		return errors.New("memory relation cannot link a node to itself")
	}
	if relation.Weight < 0 || relation.Weight > 1 {
		return errors.New("memory relation weight must be between 0 and 1")
	}
	if relation.ID == "" {
		relation.ID = uuid.NewString()
	}
	if relation.CreatedAt.IsZero() {
		relation.CreatedAt = time.Now().UTC()
	}
	if stale, err := r.shouldSkipRelationStore(ctx, relation); err != nil {
		return err
	} else if stale {
		return nil
	}
	var srcGeneration, dstGeneration string
	if err := r.db.QueryRowContext(ctx, `
		SELECT src.generation_id, dst.generation_id
		FROM memory_nodes src JOIN memory_nodes dst ON dst.user_id = src.user_id
		WHERE src.user_id = $1 AND src.id = $2 AND dst.id = $3 AND src.workspace_id=dst.workspace_id AND `+storeaccess.NodeSQL(ctx, "src")+` AND `+storeaccess.NodeSQL(ctx, "dst")+`
	`, relation.UserID, relation.SrcNodeID, relation.DstNodeID).Scan(&srcGeneration, &dstGeneration); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ErrAccessDenied
		}
		return fmt.Errorf("resolve relation endpoints: %w", err)
	}
	generationID, err := resolveRelationGeneration(relation.GenerationID, srcGeneration, dstGeneration)
	if err != nil {
		return err
	}
	relation.GenerationID = generationID
	_, err = r.db.ExecContext(ctx, `
        INSERT INTO memory_relations (id, generation_id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
        ON CONFLICT (user_id, src_node_id, dst_node_id, relation_type) DO UPDATE SET
            generation_id = EXCLUDED.generation_id,
            weight = EXCLUDED.weight,
            source = EXCLUDED.source,
            metadata = EXCLUDED.metadata,
            updated_at = EXCLUDED.updated_at
	`, relation.ID, relation.GenerationID, relation.UserID, relation.SrcNodeID, relation.DstNodeID, relation.RelationType, relation.Weight, relation.Source, dbutilstore.MarshalJSON(relation.Metadata), relation.CreatedAt)
	return err
}

func (r *PostgresMemoryRepository) DeleteRelationsByTarget(ctx context.Context, userID, targetNodeID, relationType string) error {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
        DELETE FROM memory_relations
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=memory_relations.user_id AND scope_node.id=memory_relations.dst_node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND dst_node_id = $2 AND relation_type = $3
    `, userID, targetNodeID, relationType)
	return err
}

func (r *PostgresMemoryRepository) ListRelations(ctx context.Context, userID, generationID string, nodeIDs []string, depth int) ([]domain.MemoryRelation, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
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
		query, args := relationQuery(ctx, userID, generationID, frontier)
		rows, err := r.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		nextFrontier := make([]string, 0)
		for rows.Next() {
			var item domain.MemoryRelation
			var metadataBytes []byte
			if err := rows.Scan(&item.ID, &item.GenerationID, &item.UserID, &item.SrcNodeID, &item.DstNodeID, &item.RelationType, &item.Weight, &item.Source, &metadataBytes, &item.CreatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			if _, ok := seenRelations[item.ID]; ok {
				continue
			}
			_ = json.Unmarshal(metadataBytes, &item.Metadata)
			seenRelations[item.ID] = struct{}{}
			item.WorkspaceID = security.WorkspaceID(ctx, item.UserID)
			collected = append(collected, item)
			if isTemporalRelationType(item.RelationType) {
				continue
			}
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

func (r *PostgresMemoryRepository) ReplaceEvidences(ctx context.Context, userID, nodeID string, evidences []domain.MemoryEvidence) error {
	if err := validateEvidenceIdentity(userID, nodeID, evidences); err != nil {
		return err
	}
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	if err = scopestore.RequireMemoryNodeTx(ctx, tx, userID, nodeID); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `
        DELETE FROM memory_evidences
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=memory_evidences.user_id AND scope_node.id=memory_evidences.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND node_id = $2
    `, userID, nodeID); err != nil {
		return err
	}
	normalized := normalizeMemoryEvidences(userID, nodeID, evidences)
	if err = insertMemoryEvidencesTx(ctx, tx, normalized, false); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeMemoryEvidences(userID, nodeID string, evidences []domain.MemoryEvidence) []domain.MemoryEvidence {
	now := time.Now().UTC()
	normalized := append([]domain.MemoryEvidence(nil), evidences...)
	for idx := range normalized {
		if normalized[idx].ID == "" {
			normalized[idx].ID = uuid.NewString()
		}
		if normalized[idx].UserID == "" {
			normalized[idx].UserID = userID
		}
		if normalized[idx].NodeID == "" {
			normalized[idx].NodeID = nodeID
		}
		if normalized[idx].Weight == 0 {
			normalized[idx].Weight = 1
		}
		if normalized[idx].CreatedAt.IsZero() {
			normalized[idx].CreatedAt = now
		}
	}
	return normalized
}

func (r *PostgresMemoryRepository) MergeEvidences(ctx context.Context, userID, nodeID string, evidences []domain.MemoryEvidence) error {
	if err := validateEvidenceIdentity(userID, nodeID, evidences); err != nil {
		return err
	}
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	if err = scopestore.RequireMemoryNodeTx(ctx, tx, userID, nodeID); err != nil {
		return err
	}
	normalized := normalizeMemoryEvidences(userID, nodeID, evidences)
	if err = insertMemoryEvidencesTx(ctx, tx, normalized, true); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PostgresMemoryRepository) ListEvidences(ctx context.Context, userID, nodeID string) ([]domain.MemoryEvidence, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, generation_id, user_id, node_id, conversation_id, sequence_num, part_id, artifact_id, page_index,
               start_ms, end_ms, evidence_kind, excerpt_text, weight, metadata, created_at
        FROM memory_evidences
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=memory_evidences.user_id AND scope_node.id=memory_evidences.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND node_id = $2
        ORDER BY sequence_num ASC, start_ms ASC, created_at ASC
    `, userID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryEvidenceRows(rows)
}

func (r *PostgresMemoryRepository) ListEvidencesByNodeIDs(ctx context.Context, userID string, nodeIDs []string) ([]domain.MemoryEvidence, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	nodeIDs = dbutilstore.UniqueNonEmptyStrings(nodeIDs)
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(nodeIDs)+1)
	args = append(args, userID)
	inClause := dbutilstore.Placeholders(2, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		args = append(args, nodeID)
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
        SELECT id, generation_id, user_id, node_id, conversation_id, sequence_num, part_id, artifact_id, page_index,
               start_ms, end_ms, evidence_kind, excerpt_text, weight, metadata, created_at
        FROM memory_evidences
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=memory_evidences.user_id AND scope_node.id=memory_evidences.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND node_id IN (%s)
        ORDER BY node_id ASC, sequence_num ASC, start_ms ASC, created_at ASC
    `, inClause), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryEvidenceRows(rows)
}

func scanMemoryEvidenceRows(rows *sqlpkg.Rows) ([]domain.MemoryEvidence, error) {
	items := make([]domain.MemoryEvidence, 0)
	for rows.Next() {
		var item domain.MemoryEvidence
		var generationID sqlpkg.NullString
		var userID sqlpkg.NullString
		var nodeID sqlpkg.NullString
		var partID sqlpkg.NullString
		var artifactID sqlpkg.NullString
		var excerptText sqlpkg.NullString
		var metadataBytes []byte
		if err := rows.Scan(&item.ID, &generationID, &userID, &nodeID, &item.ConversationID, &item.SequenceNum, &partID, &artifactID, &item.PageIndex, &item.StartMS, &item.EndMS, &item.EvidenceKind, &excerptText, &item.Weight, &metadataBytes, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.GenerationID = generationID.String
		item.UserID = userID.String
		item.NodeID = nodeID.String
		item.PartID = partID.String
		item.ArtifactID = artifactID.String
		item.ExcerptText = excerptText.String
		_ = json.Unmarshal(metadataBytes, &item.Metadata)
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanTypedMemoryDocumentRows(ctx context.Context, rows *sqlpkg.Rows, userID string) ([]domain.MemoryDocument, error) {
	out := make([]domain.MemoryDocument, 0)
	for rows.Next() {
		var item domain.MemoryDocument
		var metadataBytes []byte
		var timeTier string
		var memoryType string
		item.UserID = userID
		item.WorkspaceID = security.WorkspaceID(ctx, userID)
		if err := rows.Scan(&item.ID, &item.NodeID, &item.GenerationID, &item.ConversationID, &item.Content, &item.Importance, &item.DecayFactor, &item.ActivityAt, &timeTier, &item.CreatedAt, &item.UpdatedAt, &metadataBytes, &memoryType); err != nil {
			return nil, err
		}
		if len(metadataBytes) > 0 {
			item.Metadata = map[string]any{}
			if err := json.Unmarshal(metadataBytes, &item.Metadata); err != nil {
				return nil, err
			}
		}
		item.Type = domain.MemoryType(memoryType)
		item.TimeTier = domain.ParseTimeTier(timeTier)
		item = attachDocumentRuntimeMetadata(ctx, item)
		out = append(out, item)
	}
	return out, rows.Err()
}

func syncTemporalRelationsTx(ctx context.Context, tx *sqlpkg.Tx, userID, generationID, nodeID string, activityAt time.Time, tier domain.TimeTier, updatedAt time.Time) error {
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
	        WHERE user_id = $1 AND generation_id = $2 AND src_node_id = $3 AND relation_type IN ('indexed_in_tier', 'occurred_on_day')
	    `, userID, generationID, nodeID); err != nil {
		return err
	}
	tierNodeID := temporalTierNodeID(ctx, userID, tier)
	dayNodeID := temporalDayNodeID(ctx, userID, activityAt)
	monthNodeID := temporalMonthNodeID(ctx, userID, activityAt)
	yearNodeID := temporalYearNodeID(ctx, userID, activityAt)
	if err := upsertTemporalNodeTx(ctx, tx, tierNodeID, userID, generationID, "time_tier", string(tier), activityAt, updatedAt); err != nil {
		return err
	}
	if err := upsertTemporalNodeTx(ctx, tx, dayNodeID, userID, generationID, "calendar_day", activityAt.UTC().Format("2006-01-02"), activityAt, updatedAt); err != nil {
		return err
	}
	if err := upsertTemporalNodeTx(ctx, tx, monthNodeID, userID, generationID, "calendar_month", activityAt.UTC().Format("2006-01"), activityAt, updatedAt); err != nil {
		return err
	}
	if err := upsertTemporalNodeTx(ctx, tx, yearNodeID, userID, generationID, "calendar_year", activityAt.UTC().Format("2006"), activityAt, updatedAt); err != nil {
		return err
	}
	for _, relation := range []domain.MemoryRelation{
		{GenerationID: generationID, UserID: userID, SrcNodeID: nodeID, DstNodeID: tierNodeID, RelationType: "indexed_in_tier", Weight: 1, Source: "temporal", CreatedAt: updatedAt},
		{GenerationID: generationID, UserID: userID, SrcNodeID: nodeID, DstNodeID: dayNodeID, RelationType: "occurred_on_day", Weight: 1, Source: "temporal", CreatedAt: updatedAt},
		{GenerationID: generationID, UserID: userID, SrcNodeID: dayNodeID, DstNodeID: monthNodeID, RelationType: "day_in_month", Weight: 1, Source: "temporal", CreatedAt: updatedAt},
		{GenerationID: generationID, UserID: userID, SrcNodeID: monthNodeID, DstNodeID: yearNodeID, RelationType: "month_in_year", Weight: 1, Source: "temporal", CreatedAt: updatedAt},
	} {
		if _, err := tx.Exec(`
	            INSERT INTO memory_relations (id, generation_id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at, updated_at)
	            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
	            ON CONFLICT (user_id, src_node_id, dst_node_id, relation_type) DO UPDATE SET
	                generation_id = EXCLUDED.generation_id,
	                weight = EXCLUDED.weight,
	                source = EXCLUDED.source,
	                metadata = EXCLUDED.metadata,
	                updated_at = EXCLUDED.updated_at
	        `, uuid.NewString(), relation.GenerationID, relation.UserID, relation.SrcNodeID, relation.DstNodeID, relation.RelationType, relation.Weight, relation.Source, dbutilstore.MarshalJSON(map[string]any{
			"activity_at": activityAt.Format(time.RFC3339),
		}), relation.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func deleteConversationSegmentsByConversationTx(ctx context.Context, tx *sqlpkg.Tx, userID, generationID, conversationID string, keepIDs []string) error {
	if tx == nil || userID == "" || conversationID == "" {
		return nil
	}
	args := recordutilstore.GenerationFilterArgs(userID, generationID, userID, conversationID)
	query := `
        DELETE FROM conversation_segments
        WHERE user_id = $1 AND conversation_id = $2
    ` + recordutilstore.GenerationFilterClause("generation_id", 3, userID, generationID, false)
	if len(keepIDs) > 0 {
		query += fmt.Sprintf(" AND id NOT IN (%s)", dbutilstore.Placeholders(len(args)+1, len(keepIDs)))
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
    `, dbutilstore.Placeholders(2, len(nodeIDs)), dbutilstore.Placeholders(2, len(nodeIDs)))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func deleteMemoryEvidencesByNodeIDsTx(ctx context.Context, tx *sqlpkg.Tx, userID string, nodeIDs []string) error {
	if tx == nil || userID == "" || len(nodeIDs) == 0 {
		return nil
	}
	args := []any{userID}
	for _, nodeID := range nodeIDs {
		args = append(args, nodeID)
	}
	query := fmt.Sprintf(`
        DELETE FROM memory_evidences
        WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=memory_evidences.user_id AND scope_node.id=memory_evidences.node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND user_id = $1 AND node_id IN (%s)
    `, dbutilstore.Placeholders(2, len(nodeIDs)))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func deleteMemoryEmbeddingsByNodeIDsTx(ctx context.Context, tx *sqlpkg.Tx, userID string, nodeIDs []string) error {
	if tx == nil || userID == "" || len(nodeIDs) == 0 {
		return nil
	}
	args := []any{userID}
	for _, nodeID := range nodeIDs {
		args = append(args, nodeID)
	}
	query := fmt.Sprintf(`
        DELETE FROM memory_embeddings
        WHERE user_id = $1 AND node_id IN (%s)
    `, dbutilstore.Placeholders(2, len(nodeIDs)))
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
        WHERE `+storeaccess.NodeSQL(ctx, "memory_nodes")+` AND user_id = $1 AND id IN (%s)
    `, dbutilstore.Placeholders(2, len(nodeIDs)))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func upsertTemporalNodeTx(ctx context.Context, tx *sqlpkg.Tx, nodeID, userID, generationID, nodeType, entityID string, activityAt, updatedAt time.Time) error {
	_, err := tx.Exec(`
	        INSERT INTO memory_nodes (id, generation_id, user_id, node_type, entity_id, importance, decay_factor, activity_at, time_tier, embedding_status, created_at, updated_at, workspace_id,partition_id)
	        VALUES ($1,$2,$3,$4,$5,1,1,$6,'',$7,$8,$8,$9,$10)
	        ON CONFLICT (id) DO UPDATE SET
	            generation_id = EXCLUDED.generation_id,
	            activity_at = EXCLUDED.activity_at,
	            updated_at = EXCLUDED.updated_at
	        WHERE memory_nodes.user_id = EXCLUDED.user_id AND memory_nodes.workspace_id = EXCLUDED.workspace_id
	    `, nodeID, generationID, userID, nodeType, entityID, activityAt, "ready", updatedAt, security.WorkspaceID(ctx, userID), security.WritePartition(ctx))
	return err
}

func temporalMetadata(ctx context.Context, userID string, activityAt time.Time, tier domain.TimeTier) map[string]any {
	activityAt = domain.DeriveActivityAt(activityAt, time.Time{}, time.Now().UTC())
	if tier == "" {
		tier = domain.ClassifyTimeTier(activityAt, time.Now().UTC())
	}
	return map[string]any{
		"time_tier":     tier,
		"tier_node_id":  temporalTierNodeID(ctx, userID, tier),
		"day_node_id":   temporalDayNodeID(ctx, userID, activityAt),
		"month_node_id": temporalMonthNodeID(ctx, userID, activityAt),
		"year_node_id":  temporalYearNodeID(ctx, userID, activityAt),
	}
}

func defaultMemoryItemStatus(kind domain.MemoryType) string {
	if kind == domain.MemoryTypeTask {
		return "pending"
	}
	return "current"
}

func relationQuery(ctx context.Context, userID, generationID string, nodeIDs []string) (string, []any) {
	args := make([]any, 0, len(nodeIDs)*2+2)
	args = append(args, userID)
	placeholders := make([]string, 0, len(nodeIDs))
	for idx, nodeID := range nodeIDs {
		placeholders = append(placeholders, fmt.Sprintf("$%d", idx+2))
		args = append(args, nodeID)
	}
	generationArgIndex := len(args) + 1
	// Relations are generation-scoped graph state. Including empty-generation
	// rows here allows stale edges from a rebuild  to cross the
	// active generation boundary. Key-memory edges are written with their
	// resolved generation as well, so strict matching is safe.
	generationClause := recordutilstore.GenerationFilterClause("generation_id", generationArgIndex, userID, generationID, false)
	generationClause += ` AND EXISTS (SELECT 1 FROM memory_nodes scope_src WHERE scope_src.user_id=memory_relations.user_id AND scope_src.id=memory_relations.src_node_id AND scope_src.workspace_id = ` + storeaccess.WorkspaceSQL(ctx, "scope_src.user_id") + `)
 AND EXISTS (SELECT 1 FROM memory_nodes scope_dst WHERE scope_dst.user_id=memory_relations.user_id AND scope_dst.id=memory_relations.dst_node_id AND scope_dst.workspace_id = ` + storeaccess.WorkspaceSQL(ctx, "scope_dst.user_id") + `)`
	if strings.TrimSpace(generationID) != "" {
		// Conflicting edges cannot join scoped retrieval,
		// but must not carry retrieval across either endpoint's generation.
		generationClause += `
          AND EXISTS (SELECT 1 FROM memory_nodes src WHERE src.user_id = memory_relations.user_id
              AND src.id = memory_relations.src_node_id AND (src.generation_id = '' OR src.generation_id = memory_relations.generation_id))
          AND EXISTS (SELECT 1 FROM memory_nodes dst WHERE dst.user_id = memory_relations.user_id
              AND dst.id = memory_relations.dst_node_id AND (dst.generation_id = '' OR dst.generation_id = memory_relations.generation_id))`
	}
	args = recordutilstore.AppendGenerationArg(args, userID, generationID)
	inClause := strings.Join(placeholders, ",")
	query := fmt.Sprintf(`
        SELECT id, generation_id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata, created_at
        FROM memory_relations
        WHERE user_id = $1 AND (src_node_id IN (%s) OR dst_node_id IN (%s))%s
        ORDER BY created_at DESC
    `, inClause, inClause, generationClause)
	return query, args
}

func firstToken(query string) string {
	parts := strings.Fields(query)
	if len(parts) == 0 {
		return query
	}
	return parts[0]
}

func (r *PostgresMemoryRepository) shouldSkipDocumentStore(ctx context.Context, doc domain.MemoryDocument) (bool, error) {
	if conversationstore.SourceSnapshotVersionSQL(doc.Metadata) <= 0 {
		return false, nil
	}
	existing, err := r.GetDocumentInGeneration(ctx, doc.UserID, doc.GenerationID, doc.ID, doc.Type)
	if err != nil {
		if isMemoryDocumentNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return conversationstore.ShouldKeepExistingSnapshotSQL(existing.Metadata, doc.Metadata), nil
}

func (r *PostgresMemoryRepository) shouldSkipRelationStore(ctx context.Context, relation domain.MemoryRelation) (bool, error) {
	if conversationstore.SourceSnapshotVersionSQL(relation.Metadata) <= 0 {
		return false, nil
	}
	row := r.db.QueryRowContext(ctx, `
        SELECT metadata
        FROM memory_relations
        WHERE user_id = $1 AND src_node_id = $2 AND dst_node_id = $3 AND relation_type = $4
    `, relation.UserID, relation.SrcNodeID, relation.DstNodeID, relation.RelationType)
	var metadataBytes []byte
	if err := row.Scan(&metadataBytes); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	existingMetadata := map[string]any{}
	if len(metadataBytes) > 0 {
		if err := json.Unmarshal(metadataBytes, &existingMetadata); err != nil {
			return false, err
		}
	}
	return conversationstore.ShouldKeepExistingSnapshotSQL(existingMetadata, relation.Metadata), nil
}

func isMemoryDocumentNotFound(err error) bool {
	return errors.Is(err, domain.ErrAccessNotFound) || err != nil && err.Error() == "memory document not found"
}
