package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	analysisservice "memstation/apps/server/internal/service/analysis"
)

const llmCheckpointVersion = 1

type summaryCheckpoint struct {
	Version    int                     `json:"version"`
	SourceHash string                  `json:"source_hash"`
	Chunks     int                     `json:"chunks"`
	Result     analysisservice.Summary `json:"result"`
}
type analysisCheckpoint struct {
	Version    int                    `json:"version"`
	SourceHash string                 `json:"source_hash"`
	Result     analysisservice.Result `json:"result"`
}

type analysisBatchCheckpoint struct {
	Version    int                        `json:"version"`
	SourceHash string                     `json:"source_hash"`
	Result     analysisservice.Extraction `json:"result"`
}

type analysisBatchStore struct {
	services Services
	summary  domain.MemoryDocument
}

func (s analysisBatchStore) LoadAnalysisBatch(ctx context.Context, index int, hash string) (analysisservice.Extraction, bool, error) {
	stored, err := s.services.Memories.GetDocumentInGeneration(ctx, s.summary.UserID, s.summary.GenerationID, s.summary.ID, domain.MemoryTypeSummary)
	if err != nil {
		return analysisservice.Extraction{}, false, err
	}
	if stored.Content != s.summary.Content || stored.ConversationID != s.summary.ConversationID {
		return analysisservice.Extraction{}, false, domain.ErrAccessConflict
	}
	value := stored.Metadata[fmt.Sprintf("_llm_analysis_batch_%02d", index)]
	if value == nil {
		return analysisservice.Extraction{}, false, nil
	}
	raw, err := json.Marshal(value)
	var checkpoint analysisBatchCheckpoint
	if err == nil {
		err = json.Unmarshal(raw, &checkpoint)
	}
	if err != nil {
		return checkpoint.Result, false, errors.New("invalid persisted analysis batch")
	}
	return checkpoint.Result, checkpoint.Version == 1 && checkpoint.SourceHash == hash, nil
}

func (s analysisBatchStore) StoreAnalysisBatch(ctx context.Context, index int, hash string, result analysisservice.Extraction) error {
	checkpoint := analysisBatchCheckpoint{Version: 1, SourceHash: hash, Result: result}
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if len(raw) > 256*1024 {
		return errors.New("analysis batch checkpoint exceeds budget")
	}
	stored, err := storeCheckpoint(ctx, s.services, s.summary, fmt.Sprintf("_llm_analysis_batch_%02d", index), checkpoint)
	if err != nil {
		return err
	}
	var authoritative analysisBatchCheckpoint
	if json.Unmarshal(stored, &authoritative) != nil {
		return errors.New("invalid persisted analysis batch")
	}
	a, _ := json.Marshal(authoritative)
	if string(a) != string(raw) {
		return analysisservice.ErrBatchCheckpointConflict
	}
	return nil
}

type keyMemoryWriteSnapshot struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
	Create    bool      `json:"create"`
}

type keyMemoryCheckpoint struct {
	Version    int                                  `json:"version"`
	SourceHash string                               `json:"source_hash"`
	ID         string                               `json:"id"`
	Candidates []analysisservice.KeyMemoryCandidate `json:"candidates"`
	Conditions map[string]keyMemoryWriteSnapshot    `json:"conditions"`
}

func reconcileWithCheckpoint(ctx context.Context, services Services, input analysisservice.Input, documents []domain.MemoryDocument, current []domain.KeyMemory, allowed []string) (keyMemoryCheckpoint, error) {
	var summary domain.MemoryDocument
	type factSnapshot struct {
		ID, Content, Subject string
		Confidence           any
	}
	facts := []factSnapshot{}
	for _, doc := range documents {
		if doc.Type == domain.MemoryTypeSummary {
			summary = doc
		}
		if doc.Type == domain.MemoryTypeFact {
			if stringValue(doc.Metadata, "superseded_by_id") != "" || doc.Metadata["is_superseded"] == true {
				continue
			}
			facts = append(facts, factSnapshot{doc.ID, doc.Content, stringValue(doc.Metadata, "subject"), doc.Metadata["confidence"]})
		}
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].ID < facts[j].ID })
	raw, err := json.Marshal(struct {
		Facts   []factSnapshot
		Allowed []string
	}{facts, allowed})
	if err != nil {
		return keyMemoryCheckpoint{}, err
	}
	sum := sha256.Sum256(raw)
	id := hex.EncodeToString(sum[:])
	if value := summary.Metadata["_llm_keymemory_checkpoint"]; value != nil {
		raw, err := json.Marshal(value)
		if err != nil {
			return keyMemoryCheckpoint{}, err
		}
		var cached keyMemoryCheckpoint
		if err := json.Unmarshal(raw, &cached); err != nil {
			return cached, err
		}
		if cached.Version == llmCheckpointVersion && cached.ID == id {
			return cached, nil
		}
	}
	candidates, err := services.Analysis.ReconcileKeyMemories(ctx, input, documents, current, allowed)
	if err != nil {
		return keyMemoryCheckpoint{}, err
	}
	checkpoint := keyMemoryCheckpoint{Version: llmCheckpointVersion, SourceHash: id, ID: id, Candidates: candidates, Conditions: map[string]keyMemoryWriteSnapshot{}}
	for _, candidate := range candidates {
		condition := keyMemoryWriteSnapshot{Create: true}
		for _, item := range current {
			if item.Key == candidate.Key {
				condition = keyMemoryWriteSnapshot{ID: item.ID, UpdatedAt: item.UpdatedAt}
				break
			}
		}
		checkpoint.Conditions[candidate.Key] = condition
	}
	if summary.ID != "" {
		raw, err := storeCheckpoint(ctx, services, summary, "_llm_keymemory_checkpoint", checkpoint)
		if err != nil {
			return keyMemoryCheckpoint{}, err
		}
		var persisted keyMemoryCheckpoint
		if err := json.Unmarshal(raw, &persisted); err != nil {
			return persisted, err
		}
		if persisted.SourceHash != id || persisted.Version != llmCheckpointVersion {
			return persisted, errors.New("key memory checkpoint changed during processing")
		}
		checkpoint = persisted
	}
	return checkpoint, nil
}

func llmSourceHash(input analysisservice.Input) string {
	raw, _ := json.Marshal(struct {
		Sources              []analysisservice.Source
		Summary              string
		ConversationRevision int64  `json:"conversation_revision,omitempty"`
		SourceSessionTime    string `json:"source_session_time,omitempty"`
	}{analysisservice.Sources(input.Conversation), input.Summary.Content, input.Conversation.Revision, stringValue(input.Conversation.Metadata, "source_session_time")})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// A successfully validated model result is persisted before derived writes.
// Retrying a storage failure must not ask the model for a different set of facts.
func analyzeWithCheckpoint(ctx context.Context, services Services, input analysisservice.Input) (analysisservice.Result, error) {
	if services.Analysis == nil {
		return analysisservice.Result{}, errors.New("memory LLM is not configured")
	}
	// Rebuild retries may hold a newly assembled summary while the repository
	// already contains its validated analysis checkpoint.
	if input.Summary.ID != "" {
		stored, err := services.Memories.GetDocumentInGeneration(ctx, input.UserID, input.Summary.GenerationID, input.Summary.ID, domain.MemoryTypeSummary)
		if err != nil {
			return analysisservice.Result{}, err
		}
		if stored.Content == input.Summary.Content {
			input.Summary = stored
		}
	}
	input.WritePolicy.EnableSemanticEpisodes = input.WritePolicy.EnableSemanticEpisodes || services.Analysis.SemanticEpisodesEnabled()
	hash := llmSourceHash(input)
	if input.WritePolicy.EnableSemanticEpisodes || services.Analysis.GenerationPolicyID() != "" {
		policy, err := json.Marshal([]any{hash, analysisservice.SemanticEpisodePolicyHash(), services.Analysis.GenerationPolicyID(), input.WritePolicy})
		if err != nil {
			return analysisservice.Result{}, err
		}
		sum := sha256.Sum256(policy)
		hash = hex.EncodeToString(sum[:])
	}
	if value := input.Summary.Metadata["_llm_analysis_checkpoint"]; value != nil {
		raw, err := json.Marshal(value)
		if err != nil {
			return analysisservice.Result{}, err
		}
		var cached analysisCheckpoint
		if err := json.Unmarshal(raw, &cached); err != nil {
			return analysisservice.Result{}, errors.New("invalid persisted LLM checkpoint")
		}
		if cached.Version == llmCheckpointVersion && cached.SourceHash == hash {
			all := append(append([]domain.MemoryDocument{}, cached.Result.Facts...), cached.Result.GenericItems...)
			if cached.Result.Impression.ID != "" {
				all = append(all, cached.Result.Impression)
			}
			for _, doc := range all {
				if doc.UserID != input.UserID || doc.ConversationID != input.ConversationID {
					return analysisservice.Result{}, errors.New("persisted LLM checkpoint scope mismatch")
				}
			}
			return cached.Result, nil
		}
	}
	if input.Summary.ID != "" {
		input.BatchCheckpoints = analysisBatchStore{services: services, summary: input.Summary}
	}
	result, err := services.Analysis.AnalyzeConversation(ctx, input)
	if errors.Is(err, analysisservice.ErrBatchCheckpointConflict) {
		// Adopt the first validated result, rather than publish the losing one.
		result, err = services.Analysis.AnalyzeConversation(ctx, input)
	}
	if err != nil {
		return result, err
	}
	checkpoint := analysisCheckpoint{Version: llmCheckpointVersion, SourceHash: hash, Result: result}
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return analysisservice.Result{}, err
	}
	if len(raw) > 256*1024 {
		return analysisservice.Result{}, errors.New("LLM checkpoint exceeds 256 KiB budget")
	}
	raw, err = storeCheckpoint(ctx, services, input.Summary, "_llm_analysis_checkpoint", checkpoint)
	if err != nil {
		return analysisservice.Result{}, err
	}
	var persisted analysisCheckpoint
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return analysisservice.Result{}, err
	}
	if persisted.Version != llmCheckpointVersion || persisted.SourceHash != hash {
		return analysisservice.Result{}, errors.New("analysis checkpoint changed during processing")
	}
	return persisted.Result, nil
}

func summarizeWithCheckpoint(ctx context.Context, services Services, input analysisservice.Input, summaryID, generationID string) (analysisservice.Summary, error) {
	if services.Analysis == nil {
		return analysisservice.Summary{}, errors.New("memory LLM is not configured")
	}
	documents, err := services.Memories.ListDocumentsByConversationInGeneration(ctx, input.UserID, generationID, input.ConversationID)
	if err != nil {
		return analysisservice.Summary{}, err
	}
	for _, doc := range documents {
		if doc.ID != summaryID || doc.Type != domain.MemoryTypeSummary {
			continue
		}
		value := doc.Metadata["_llm_summary_checkpoint"]
		if value == nil {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return analysisservice.Summary{}, err
		}
		var checkpoint summaryCheckpoint
		if err := json.Unmarshal(raw, &checkpoint); err != nil {
			return analysisservice.Summary{}, errors.New("invalid persisted LLM summary")
		}
		if checkpoint.Version == llmCheckpointVersion && checkpoint.SourceHash == summarySourceHash(input, services.Analysis.GenerationPolicyID()) {
			checkpoint.Result.ChunkCount = checkpoint.Chunks
			checkpoint.Result.GenerationPolicy = services.Analysis.GenerationPolicyID()
			return checkpoint.Result, nil
		}
	}
	return services.Analysis.SummarizeConversation(ctx, input)
}

func newSummaryCheckpoint(conversation domain.ConversationRecord, generated analysisservice.Summary) summaryCheckpoint {
	return summaryCheckpoint{Version: llmCheckpointVersion, SourceHash: summarySourceHash(analysisservice.Input{Conversation: conversation}, generated.GenerationPolicy), Chunks: generated.ChunkCount, Result: generated}
}

func summarySourceHash(input analysisservice.Input, policy string) string {
	raw, _ := json.Marshal([]string{llmSourceHash(input), policy})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func storeSummaryWithCheckpoint(ctx context.Context, services Services, document domain.MemoryDocument) (domain.MemoryDocument, error) {
	documents, err := services.Memories.ListDocumentsByConversationInGeneration(ctx, document.UserID, document.GenerationID, document.ConversationID)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	for _, previous := range documents {
		if previous.ID != document.ID || previous.Content != document.Content {
			continue
		}
		before, _ := json.Marshal(previous.Metadata["_llm_summary_checkpoint"])
		after, _ := json.Marshal(document.Metadata["_llm_summary_checkpoint"])
		if string(before) == string(after) {
			document.Metadata = cloneMap(document.Metadata)
			for _, key := range []string{"_llm_analysis_checkpoint", "_llm_keymemory_checkpoint"} {
				if value := previous.Metadata[key]; value != nil {
					document.Metadata[key] = value
				}
			}
			for key, value := range previous.Metadata {
				if strings.HasPrefix(key, "_llm_analysis_batch_") {
					document.Metadata[key] = value
				}
			}
		}
	}
	if err := services.Memories.StoreDocument(ctx, document); err != nil {
		return domain.MemoryDocument{}, err
	}
	return services.Memories.GetDocumentInGeneration(ctx, document.UserID, document.GenerationID, document.ID, document.Type)
}

func storeCheckpoint(ctx context.Context, services Services, summary domain.MemoryDocument, key string, value any) (json.RawMessage, error) {
	repo, ok := services.Memories.(ports.SummaryCheckpointRepository)
	if !ok {
		return nil, errors.New("summary checkpoint persistence is required")
	}
	if err := repo.StoreSummaryCheckpoint(ctx, summary.UserID, summary.GenerationID, summary.ID, summary.Content, key, value); err != nil {
		return nil, err
	}
	stored, err := services.Memories.GetDocumentInGeneration(ctx, summary.UserID, summary.GenerationID, summary.ID, summary.Type)
	if err != nil {
		return nil, err
	}
	return json.Marshal(stored.Metadata[key])
}
