package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

type parentTaskScopeKey struct{}

type taskScope struct {
	ReceiptID            string `json:"receipt_id"`
	OperationID          string `json:"operation_id"`
	PartitionID          string `json:"partition_id"`
	NodeID               string `json:"node_id"`
	GenerationID         string `json:"generation_id"`
	ConversationRevision int64  `json:"conversation_revision"`
	Version              int    `json:"schema_version"`
	UserID               string `json:"user_id"`
	WorkspaceID          string `json:"workspace_id"`
	AgentID              string `json:"agent_id"`
	ConversationID       string `json:"conversation_id"`
}

// Consumers accept only the current, authority-verified task envelope.
func bindTaskScope(ctx context.Context, task *asynq.Task, services Services) (context.Context, error) {
	var scope taskScope
	if err := json.Unmarshal(task.Payload(), &scope); err != nil {
		return ctx, fmt.Errorf("invalid task scope: %w", asynq.SkipRetry)
	}
	if scope.Version != 2 || scope.WorkspaceID == "" {
		return ctx, errors.Join(domain.ErrAccessDenied, asynq.SkipRetry)
	}
	var bound context.Context
	var err error
	if services.TaskAuthority == nil {
		return ctx, errors.Join(domain.ErrAccessDenied, asynq.SkipRetry)
	}
	if err = services.TaskAuthority.ValidateMemoryTask(ctx, domain.MemoryTaskScope{ReceiptID: scope.ReceiptID, OperationID: scope.OperationID, UserID: scope.UserID, WorkspaceID: scope.WorkspaceID, PartitionID: scope.PartitionID, ConversationID: scope.ConversationID, NodeID: scope.NodeID, GenerationID: scope.GenerationID, ConversationRevision: scope.ConversationRevision}); err != nil {
		// Obsolete is returned only after persisted identity and partition were
		// verified. Preserve that scope for the terminal job ledger, never run it.
		if errors.Is(err, domain.ErrMemoryTaskObsolete) {
			bound, bindErr := security.WithDataScope(ctx, scope.UserID, scope.WorkspaceID, scope.PartitionID)
			if bindErr != nil {
				return ctx, bindErr
			}
			return bound, err
		}
		if errors.Is(err, domain.ErrAccessDenied) {
			return ctx, errors.Join(err, asynq.SkipRetry)
		}
		return ctx, err
	}
	bound, err = security.WithDataScope(ctx, scope.UserID, scope.WorkspaceID, scope.PartitionID)
	if err != nil {
		return ctx, errors.Join(err, asynq.SkipRetry)
	}
	if scope.ConversationID != "" {
		if services.Conversations == nil {
			return ctx, errors.New("conversation scope resolver unavailable")
		}
		c, err := services.Conversations.GetConversation(bound, scope.UserID, scope.ConversationID)
		if errors.Is(err, domain.ErrConversationNotFound) {
			return ctx, errors.Join(err, asynq.SkipRetry)
		}
		if err != nil {
			return ctx, err
		}
		if c.WorkspaceID != security.WorkspaceID(bound, scope.UserID) || scope.AgentID != "" && c.AgentID != scope.AgentID {
			return ctx, errors.Join(domain.ErrAccessDenied, asynq.SkipRetry)
		}
	}
	return context.WithValue(bound, parentTaskScopeKey{}, scope), nil
}

func prepareScopedTask(ctx context.Context, task domain.TaskMessage) (domain.TaskMessage, error) {
	raw, err := json.Marshal(task.Payload)
	if err != nil {
		return task, err
	}
	var scope taskScope
	if err = json.Unmarshal(raw, &scope); err != nil {
		return task, domain.ErrAccessDenied
	}
	if _, versioned := task.Payload["schema_version"]; versioned && scope.Version != 2 || scope.Version == 2 && scope.WorkspaceID == "" {
		return task, domain.ErrAccessDenied
	}
	user, ok := task.Payload["user_id"].(string)
	if !ok {
		return task, domain.ErrAccessDenied
	}
	var explicit string
	if raw, exists := task.Payload["workspace_id"]; exists {
		var valid bool
		explicit, valid = raw.(string)
		if !valid {
			return task, domain.ErrAccessDenied
		}
	}
	workspace, err := security.DataScope(ctx, user, explicit)
	if err != nil {
		return task, err
	}
	payload := make(map[string]any, len(task.Payload)+2)
	for k, v := range task.Payload {
		payload[k] = v
	}
	payload["schema_version"] = 2
	payload["partition_id"] = security.WritePartition(ctx)
	if parent, ok := ctx.Value(parentTaskScopeKey{}).(taskScope); ok {
		payload["schema_version"] = 2
		if _, exists := payload["conversation_id"]; !exists && parent.ConversationID != "" {
			payload["conversation_id"] = parent.ConversationID
			payload["conversation_revision"] = parent.ConversationRevision
		}
	}
	delete(payload, "content")
	payload["workspace_id"] = workspace
	task.Payload = payload
	if task.UniqueKey != "" {
		components := []string{user, workspace, task.Type, task.UniqueKey}
		components = append(components, security.WritePartition(ctx))
		identity, _ := json.Marshal(components)
		task.UniqueKey = domain.AccessID("task", string(identity))
	}
	return task, nil
}
