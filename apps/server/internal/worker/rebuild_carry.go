package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"memstation/apps/server/internal/domain"

	"github.com/hibiken/asynq"
)

func handleMemoryRebuildCarry(services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		ctx, err := bindTaskScope(ctx, task, services)
		if err != nil {
			return err
		}
		if services.Generations == nil || services.Memories == nil || services.Vectors == nil {
			return errors.New("rebuild services unavailable")
		}
		var payload taskScope
		if err = json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		g, err := services.Generations.GetGeneration(ctx, payload.UserID, payload.GenerationID)
		if err != nil {
			return err
		}
		if g.IsActive || g.Status == domain.MemoryGenerationStatusDiscarded {
			return nil
		}
		source, revision, err := services.Generations.CarryDocument(ctx, payload.UserID, payload.GenerationID, payload.NodeID)
		if err != nil {
			return fmt.Errorf("resolve carried revision: %w", err)
		}
		docs, err := services.Memories.ListDocumentsByNodeIDsInGeneration(ctx, payload.UserID, source, []string{payload.NodeID})
		if err != nil {
			return err
		}
		if len(docs) != 1 {
			return fmt.Errorf("read carried document: %w", domain.ErrMemoryTaskObsolete)
		}
		d := docs[0]
		err = services.Vectors.Upsert(ctx, domain.VectorUpsertRequest{UserID: d.UserID, WorkspaceID: d.WorkspaceID, PartitionID: d.PartitionID, GenerationID: g.GenerationID, NodeID: d.NodeID, NodeType: string(d.Type), Content: d.Content, EmbeddingVersion: g.EmbeddingVersion, EmbeddingModel: g.EmbeddingModel, EmbeddingRevision: revision})
		if completionErr := services.Generations.CompleteCarry(ctx, payload.UserID, payload.GenerationID, payload.NodeID, err); completionErr != nil {
			return errors.Join(err, completionErr)
		}
		return err
	}
}
