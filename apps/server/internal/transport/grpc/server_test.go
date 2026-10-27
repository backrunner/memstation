package grpctransport_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	mediastore "memstation/apps/server/internal/store/sql/media"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/testutil/authfixture"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	emptypb "google.golang.org/protobuf/types/known/emptypb"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/security"
	adminservice "memstation/apps/server/internal/service/admin"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	conversationservice "memstation/apps/server/internal/service/conversation"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	memoryservice "memstation/apps/server/internal/service/memory"
	settingsservice "memstation/apps/server/internal/service/settings"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
	"memstation/apps/server/internal/store/vector"
	grpctransport "memstation/apps/server/internal/transport/grpc"
	pb "memstation/apps/server/proto/memstation/v1"
	"memstation/apps/server/tests/testutil/mockprovider"
)

const grpcTestAdminToken = "grpc-test-admin-token"

func TestHealthAndAdminAuth(t *testing.T) {
	t.Parallel()

	conn := newClient(t)
	healthClient := pb.NewHealthServiceClient(conn)
	adminClient := pb.NewAdminServiceClient(conn)

	health, err := healthClient.GetStatus(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.GetStatus() != "ready" {
		t.Fatalf("expected ready health, got %q", health.GetStatus())
	}

	_, err = adminClient.Overview(context.Background(), &pb.OverviewRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated admin call, got %v", err)
	}
}

func TestGatewayMemoryAndAdminLifecycle(t *testing.T) {
	t.Parallel()

	conn := newClient(t)
	gatewayClient := pb.NewGatewayServiceClient(conn)
	memoryClient := pb.NewMemoryServiceClient(conn)
	adminClient := pb.NewAdminServiceClient(conn)

	proxyCtx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+authfixture.Token)
	complete, err := gatewayClient.Complete(proxyCtx, &pb.CompleteRequest{
		Request: &pb.UnifiedRequest{
			Protocol: string(domain.ProtocolOpenAIChat),
			Model:    "gpt-4.1-mini",
			Messages: []*pb.UnifiedMessage{{
				Role: string(domain.RoleUser),
				Text: "hello grpc",
			}},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got := complete.GetResponse().GetMessage().GetText(); !strings.Contains(got, "mock adapter handled") {
		t.Fatalf("unexpected gateway response text: %q", got)
	}
	raw := complete.GetResponse().GetRaw()
	if raw == nil {
		t.Fatal("expected gateway response raw payload")
	}
	conversationID, _ := raw.AsMap()["conversation_id"].(string)
	if conversationID == "" {
		t.Fatal("expected conversation_id in gateway response raw payload")
	}

	created, err := memoryClient.UpsertKeyMemory(proxyCtx, &pb.UpsertKeyMemoryRequest{
		Key:          "favorite_food",
		Value:        "dumplings",
		Category:     "profile",
		ReviewStatus: "approved",
	})
	if err != nil {
		t.Fatalf("upsert key memory: %v", err)
	}
	if created.GetKey() != "favorite_food" {
		t.Fatalf("unexpected key memory key: %q", created.GetKey())
	}

	memories, err := memoryClient.ListKeyMemories(proxyCtx, &pb.ListKeyMemoriesRequest{TopK: 10})
	if err != nil {
		t.Fatalf("list key memories: %v", err)
	}
	if len(memories.GetItems()) == 0 {
		t.Fatal("expected key memories to be returned")
	}

	adminCtx := metadata.AppendToOutgoingContext(context.Background(), "x-admin-token", grpcTestAdminToken)
	userID := security.DeriveUserIDFromAPIKey(authfixture.Token)
	conversations, err := adminClient.ListConversations(adminCtx, &pb.ListConversationsRequest{
		UserId: userID,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(conversations.GetItems()) == 0 {
		t.Fatal("expected conversations to be returned for proxy user")
	}
	if conversations.GetItems()[0].GetConversationId() != conversationID {
		t.Fatalf("expected conversation %q, got %q", conversationID, conversations.GetItems()[0].GetConversationId())
	}
}

func newClient(t *testing.T) *grpc.ClientConn {
	t.Helper()

	server, listener := newTestServer()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("dial grpc test server: %v", err)
	}
	conn.Connect()
	for state := conn.GetState(); state != connectivity.Ready; state = conn.GetState() {
		if !conn.WaitForStateChange(ctx, state) {
			t.Fatalf("connect grpc test server: %v", ctx.Err())
		}
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func newTestServer() (*grpc.Server, *bufconn.Listener) {
	memoryRepo := memorystore.NewInMemoryMemoryRepository()
	conversations := conversationstore.NewInMemoryConversationRepository()
	mediaRepo := mediastore.NewInMemoryMediaRepository()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	admin := adminservice.NewService(conversations, mediaRepo, memoryRepo, settings, audit, modelcap.NewRegistry(domain.ProviderOpenAICompatible, "gpt-4.1-mini", "", "", ""))
	users := userservice.NewService(nil)
	apiKeys := apikeyservice.NewService(nil)
	usage := usageservice.NewService(nil, conversations)
	admin.AttachUserRuntime(users, apiKeys, usage)

	engine := memoryservice.NewEngine(memoryRepo, vector.NewPGVectorStore(nil))
	memorySvc := memoryservice.NewService(engine, memoryRepo)
	gateway := gatewayservice.NewService(
		provider.NewRegistry(
			domain.ProviderOpenAICompatible,
			mockprovider.New(domain.ProviderOpenAICompatible),
		),
		engine,
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAICompatible,
	)

	server := grpctransport.NewServer(grpctransport.Dependencies{
		Config: bootstrap.Config{
			Version:         "0.1.0",
			DefaultProvider: domain.ProviderOpenAICompatible,
			GRPCPort:        9090,
		},
		Health:      observability.NewRegistry(),
		Auth:        security.Authenticator{AdminToken: grpcTestAdminToken, LookupPrincipal: authfixture.LookupPrincipal},
		RateLimiter: security.NewRateLimiter(20, 40),
		Models:      modelcap.NewRegistry(domain.ProviderOpenAICompatible, "gpt-4.1-mini", "", "", ""),
		Gateway:     gateway,
		Memory:      memorySvc,
		Admin:       admin,
		Audit:       audit,
	})

	listener := bufconn.Listen(1024 * 1024)
	go func() {
		_ = server.Serve(listener)
	}()
	return server, listener
}
