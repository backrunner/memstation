package grpctransport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/apikey"
	pb "memstation/apps/server/proto/memstation/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestIndependentMemoryRPCPreservesUnicodeRevisionAndTime(t *testing.T) {
	in := &pb.IngestBatch{WorkspaceId: "group", IntegrationId: "integration", ExternalSpaceId: "room", Events: []*pb.SourceEvent{{EventId: "event", SourceMessageId: "source", Type: "message.created", SourceRevision: 9007199254740993, OccurredAt: "2026-10-25T10:00:00+08:00", Speaker: &pb.SourceSpeaker{Kind: "human", ExternalId: "person"}, Parts: []*pb.SourcePart{{Type: "quote", Text: "中文🙂", Source: &pb.SourceRef{WorkspaceId: "group", MessageId: "parent", Revision: 2, Span: &pb.TextSpan{Start: 2, End: 5}}}}}}}
	var out domain.ObserveBatch
	if err := decodeContract(in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Events[0].SourceRevision != 9007199254740993 || out.Events[0].OccurredAt.Hour() != 10 || out.Events[0].Parts[0].Source.Span.End != 5 {
		t.Fatalf("lossy protocol conversion %+v", out)
	}
	receipt := domain.IngestionReceipt{ID: "receipt", AcceptedSequence: 9007199254740993, NextRetryAt: func() *time.Time { v := time.Now().UTC(); return &v }(), Events: []domain.EventAcceptance{}}
	encoded := &pb.IngestionReceipt{}
	if err := contractResponse(receipt, encoded, nil); err != nil {
		t.Fatal(err)
	}
	if encoded.AcceptedSequence != receipt.AcceptedSequence {
		t.Fatal("lossy watermark")
	}
	search := toProtoSearchResult(domain.SearchResult{Trace: &domain.RecallTrace{Watermarks: []domain.StreamWatermark{{Accepted: receipt.AcceptedSequence, Indexed: receipt.AcceptedSequence - 1}}}})
	if search.RecallTrace.Watermarks[0].Accepted != receipt.AcceptedSequence || search.RecallTrace.Watermarks[0].Indexed != receipt.AcceptedSequence-1 {
		t.Fatal("lossy recall watermark")
	}
}

func TestMemoryDataCapabilitiesAndAudienceAreEnforced(t *testing.T) {
	ctx := context.Background()
	keys := apikey.NewService(nil)
	agent, err := keys.Identities().Create(ctx, "owner", "Event observer", []string{domain.CapabilityEventsWrite})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := keys.Identities().Workspace(ctx, "owner", domain.PersonalWorkspaceID("owner"))
	if err != nil {
		t.Fatal(err)
	}
	workspace.Capabilities = domain.AllCapabilities()
	if _, err = keys.Identities().UpdateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	_, token, err := keys.CreateWithOptions(ctx, "owner", apikey.CreateOptions{WorkspaceID: domain.PersonalWorkspaceID("owner"), Name: "Observer", AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	intercept := authUnaryInterceptor(security.Authenticator{LookupPrincipal: keys.LookupPrincipalByHash}, nil)
	handler := func(context.Context, any) (any, error) { return nil, nil }
	for method, allowed := range map[string]bool{pb.MemoryDataService_Probe_FullMethodName: true, pb.MemoryDataService_Observe_FullMethodName: true, pb.MemoryDataService_Forget_FullMethodName: false, pb.MemoryDataService_Source_FullMethodName: false} {
		bound := metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
		_, err = intercept(bound, nil, &grpc.UnaryServerInfo{FullMethod: method}, handler)
		if allowed && err != nil || !allowed && status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s %v", method, err)
		}
	}
	raw, _ := json.Marshal(domain.Audience{Kind: "workspace"})
	for _, audience := range []string{string(raw) + "{}", `{"kind":"direct","actor_ids":["unbound"]}`} {
		bound := metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+token, "x-memstation-audience", audience))
		_, err = intercept(bound, nil, &grpc.UnaryServerInfo{FullMethod: pb.MemoryDataService_Observe_FullMethodName}, handler)
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("invalid audience accepted %v", err)
		}
	}
}
