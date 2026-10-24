package grpctransport

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/apikey"
	pb "memstation/apps/server/proto/memstation/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAgentPrincipalAcrossGRPCMethods(t *testing.T) {
	ctx := context.Background()
	keys := apikey.NewService(nil)
	agent, err := keys.Identities().Create(ctx, "owner", "Reader", []string{domain.CapabilityMemoryRead})
	if err != nil {
		t.Fatal(err)
	}
	key, raw, err := keys.CreateWithOptions(ctx, "owner", apikey.CreateOptions{Name: "Reader", AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	interceptor := authUnaryInterceptor(security.Authenticator{LookupPrincipal: keys.LookupPrincipalByHash}, nil)
	handler := func(ctx context.Context, _ any) (any, error) {
		a, ok := security.AccessFromContext(ctx)
		if !ok || a.AgentID != agent.ID || a.CredentialID != key.ID {
			t.Fatal("lost principal in gRPC")
		}
		return nil, nil
	}
	for method := range proxyMethodSet {
		ctx := metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+raw))
		_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, handler)
		allowed := method == pb.MemoryService_Search_FullMethodName || method == pb.MemoryService_ListKeyMemories_FullMethodName
		if allowed && err != nil || !allowed && status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: %v", method, err)
		}
	}
	for _, workspace := range []string{"other-owner-space", "other-space"} {
		scoped := metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+raw, "x-memstation-workspace", workspace))
		_, err = interceptor(scoped, nil, &grpc.UnaryServerInfo{FullMethod: pb.MemoryService_Search_FullMethodName}, handler)
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("workspace override: %v", err)
		}
	}
}
