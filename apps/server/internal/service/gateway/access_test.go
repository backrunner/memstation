package gateway

import (
	"context"
	"errors"
	"maps"
	"testing"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	conversationservice "memstation/apps/server/internal/service/conversation"
	settingsservice "memstation/apps/server/internal/service/settings"
)

func TestRequestDefaultsUseAuthenticatedOwner(t *testing.T) {
	ctx := context.Background()
	settings := settingsservice.NewService(nil, nil, "default-model", "summary-model", "analysis-model", "embedding-model", 2048)
	for owner, model := range map[string]string{"owner": "owner-model", "foreign": "foreign-model"} {
		if _, err := settings.UpdateForUser(ctx, owner, map[string]any{"chat_model": model}); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(nil, nil, nil, settings, domain.ProviderOpenAICompatible)
	access := domain.AccessContext{UserID: "owner", AgentID: "agent", WorkspaceID: domain.PersonalWorkspaceID("owner"), ScopeVersion: 2, PartitionIDs: []string{""}}
	request, err := service.ResolveRequestDefaults(security.WithAccess(ctx, access), domain.UnifiedRequest{UserID: "foreign", AgentID: "fixture-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if request.Model != "owner-model" || request.UserID != access.UserID || request.WorkspaceID != access.WorkspaceID || request.AgentID != access.AgentID {
		t.Fatalf("defaults used untrusted request identity: %#v", request)
	}
}

func TestPrepareRequestDoesNotMutateCallerMetadata(t *testing.T) {
	service := NewService(nil, nil, nil, nil, domain.ProviderOpenAICompatible)
	metadata := map[string]any{"agent_id": "untrusted", "caller": "original"}
	original := maps.Clone(metadata)
	request := domain.UnifiedRequest{Mode: domain.ModeTransparent, RequestMetadata: metadata}
	access := domain.AccessContext{UserID: "owner", AgentID: "agent-a", CredentialID: "key", WorkspaceID: domain.PersonalWorkspaceID("owner"), Capabilities: domain.PersonalCapabilities(), ScopeVersion: 2, PartitionIDs: []string{""}}
	first, _, err := service.prepareRequest(security.WithAccess(context.Background(), access), request)
	if err != nil {
		t.Fatal(err)
	}
	access.AgentID = "agent-b"
	if _, _, err = service.prepareRequest(security.WithAccess(context.Background(), access), request); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(metadata, original) || first.RequestMetadata["agent_id"] != "agent-a" {
		t.Fatalf("request reuse contaminated provenance: caller=%v first=%v", metadata, first.RequestMetadata)
	}
}

func TestAgentSessionsAndRevisionAcrossProtocols(t *testing.T) {
	ctx := context.Background()
	repo := conversationstore.NewInMemoryConversationRepository()
	conversations := conversationservice.NewService(repo, conversationstore.NewInMemoryOutboxRepository(), 100, 10000)
	service := NewService(nil, nil, conversations, nil, domain.ProviderOpenAICompatible)
	access := domain.AccessContext{UserID: "owner", AgentID: "agent-a", CredentialID: "key-a", WorkspaceID: domain.PersonalWorkspaceID("owner"), Capabilities: domain.PersonalCapabilities(), ScopeVersion: 2, PartitionIDs: []string{""}}
	request := domain.UnifiedRequest{UserID: "owner", SessionID: "main", Protocol: domain.ProtocolOpenAIChat, Mode: domain.ModeTransparent, Model: "test-model", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "hello"}}, AgentID: "fixture-agent"}
	first, _, err := service.prepareRequest(security.WithAccess(ctx, access), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConversationID == "main" || first.RequestMetadata["agent_id"] != "agent-a" {
		t.Fatal("missing session mapping or agent provenance")
	}
	for _, protocol := range []domain.Protocol{domain.ProtocolOpenAIResponses, domain.ProtocolAnthropic} {
		request.Protocol = protocol
		access.CredentialID = "rotated-key"
		next, _, err := service.prepareRequest(security.WithAccess(ctx, access), request)
		if err != nil {
			t.Fatal(err)
		}
		if next.ConversationID != first.ConversationID {
			t.Fatal("protocol or credential rotation changed agent session")
		}
	}
	access.AgentID = "agent-b"
	other, _, err := service.prepareRequest(security.WithAccess(ctx, access), request)
	if err != nil {
		t.Fatal(err)
	}
	if other.ConversationID == first.ConversationID {
		t.Fatal("agents collided on an external session name")
	}
	if _, err = conversations.PersistExchange(ctx, first, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = conversations.PersistExchange(ctx, first, nil); !errors.Is(err, domain.ErrAccessConflict) {
		t.Fatalf("stale request overwrote history: %v", err)
	}
	access.AgentID = "agent-a"
	fresh, _, err := service.prepareRequest(security.WithAccess(ctx, access), request)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ExpectedConversationRevision == nil || *fresh.ExpectedConversationRevision != 1 {
		t.Fatal("new request did not read committed revision")
	}
}

func TestToolRuntimeCannotBypassCredentialCeiling(t *testing.T) {
	access := domain.AccessContext{UserID: "owner", Capabilities: []string{domain.CapabilityGateway}, ScopeVersion: 2, PartitionIDs: []string{""}}
	runtime := NewToolRuntime(nil, nil, nil, 3)
	for _, tool := range []string{"recall_memory", "get_key_memory", "write_key_memory"} {
		_, err := runtime.executeCall(security.WithAccess(context.Background(), access), domain.UnifiedRequest{UserID: "owner", ToolWriteEnabled: true, AgentID: "fixture-agent"}, domain.ToolCall{Name: tool})
		if !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatalf("tool bypassed authority: %s %v", tool, err)
		}
	}
}
