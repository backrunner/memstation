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

type taskScope struct {
	Version        int    `json:"schema_version"`
	UserID         string `json:"user_id"`
	WorkspaceID    string `json:"workspace_id"`
	AgentID        string `json:"agent_id"`
	ConversationID string `json:"conversation_id"`
}

// Legacy payloads can resolve only to the deterministic Personal workspace.
// No unsupported scope or malformed payload is silently treated as owner-wide.
func bindTaskScope(ctx context.Context, task *asynq.Task, services Services) (context.Context, error) {
	var scope taskScope
	if err := json.Unmarshal(task.Payload(), &scope); err != nil {
		return ctx, fmt.Errorf("invalid task scope: %w", asynq.SkipRetry)
	}
	if scope.Version < 0 || scope.Version > 1 || scope.Version == 1 && scope.WorkspaceID == "" {
		return ctx, errors.Join(domain.ErrAccessDenied, asynq.SkipRetry)
	}
	bound, err := security.WithPersonalDataScope(ctx, scope.UserID, scope.WorkspaceID)
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
		if c.WorkspaceID != domain.PersonalWorkspaceID(scope.UserID) || scope.AgentID != "" && c.AgentID != scope.AgentID {
			return ctx, errors.Join(domain.ErrAccessDenied, asynq.SkipRetry)
		}
	}
	return bound, nil
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
	if scope.Version < 0 || scope.Version > 1 || scope.Version == 1 && scope.WorkspaceID == "" {
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
	workspace, err := security.PersonalScope(ctx, user, explicit)
	if err != nil {
		return task, err
	}
	payload := make(map[string]any, len(task.Payload)+2)
	for k, v := range task.Payload {
		payload[k] = v
	}
	payload["schema_version"] = 1
	payload["workspace_id"] = workspace
	task.Payload = payload
	if task.UniqueKey != "" {
		identity, _ := json.Marshal([]string{user, workspace, task.Type, task.UniqueKey})
		task.UniqueKey = domain.AccessID("task", string(identity))
	}
	return task, nil
}
