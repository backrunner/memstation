package grpctransport

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	memorycontrolservice "memstation/apps/server/internal/service/memorycontrol"

	"memstation/apps/server/internal/domain"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcHealthV1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/security"
	adminservice "memstation/apps/server/internal/service/admin"
	"memstation/apps/server/internal/service/aibroker"
	auditsvc "memstation/apps/server/internal/service/audit"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	identityservice "memstation/apps/server/internal/service/identity"
	ingestionservice "memstation/apps/server/internal/service/ingestion"
	lifecycleservice "memstation/apps/server/internal/service/lifecycle"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	pb "memstation/apps/server/proto/memstation/v1"
)

type Dependencies struct {
	MemoryControl *memorycontrolservice.Service
	AIUsage       *aibroker.UsageService
	DataReady     func(context.Context) error
	Ingestion     *ingestionservice.Service
	Lifecycle     *lifecycleservice.Service
	Identity      *identityservice.Service
	Config        bootstrap.Config
	Logger        *slog.Logger
	Health        *observability.Registry
	Auth          security.Authenticator
	RateLimiter   *security.RateLimiter
	Models        *modelcap.Registry
	Gateway       *gatewayservice.Service
	Memory        *memoryservice.Service
	Generations   *memorygenservice.Service
	Admin         *adminservice.Service
	Audit         *auditsvc.Service
}

var proxyMethodSet = map[string]struct{}{
	pb.GatewayService_ListModels_FullMethodName:     {},
	pb.GatewayService_Complete_FullMethodName:       {},
	pb.MemoryService_Search_FullMethodName:          {},
	pb.MemoryService_ListKeyMemories_FullMethodName: {},
	pb.MemoryService_UpsertKeyMemory_FullMethodName: {},
	pb.MemoryService_DeleteKeyMemory_FullMethodName: {},
}

var adminMethodSet = map[string]struct{}{
	pb.AdminService_Overview_FullMethodName:              {},
	pb.AdminService_ListConversations_FullMethodName:     {},
	pb.AdminService_GetConversation_FullMethodName:       {},
	pb.AdminService_GetConversationMemory_FullMethodName: {},
}

func NewServer(deps Dependencies) *grpc.Server {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoveryUnaryInterceptor(logger),
			logUnaryInterceptor(logger),
			authUnaryInterceptor(deps.Auth, deps.RateLimiter),
			memoryRecoveryInterceptor(deps.DataReady),
		),
	)

	pb.RegisterHealthServiceServer(server, &healthServiceServer{
		config: deps.Config,
		health: deps.Health,
	})
	pb.RegisterGatewayServiceServer(server, &gatewayServiceServer{
		models:  deps.Models,
		gateway: deps.Gateway,
	})
	pb.RegisterMemoryDataServiceServer(server, &memoryDataServiceServer{deps: deps})
	pb.RegisterWorkspaceAdminServiceServer(server, &workspaceAdminServiceServer{deps: deps})
	pb.RegisterMemoryServiceServer(server, &memoryServiceServer{
		memory: deps.Memory,
		audit:  deps.Audit,
	})
	pb.RegisterAdminServiceServer(server, &adminServiceServer{
		admin:       deps.Admin,
		memory:      deps.Memory,
		generations: deps.Generations,
	})

	healthServer := &readinessHealthService{health: deps.Health}
	grpcHealthV1.RegisterHealthServer(server, healthServer)
	reflection.Register(server)

	return server
}

func recoveryUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (_ any, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(ctx, "grpc request panicked", "method", info.FullMethod, "panic", recovered)
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

func logUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logger.InfoContext(ctx, "grpc request completed",
			"method", info.FullMethod,
			"duration", time.Since(start).String(),
			"code", status.Code(err).String(),
		)
		return resp, err
	}
}

func authUnaryInterceptor(auth security.Authenticator, limiter *security.RateLimiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		switch {
		case isProxyMethod(info.FullMethod):
			md, _ := metadata.FromIncomingContext(ctx)
			token := firstCredential(md, "authorization", "x-api-key")
			if strings.HasPrefix(strings.ToLower(token), "bearer ") {
				token = strings.TrimSpace(token[7:])
			}
			var err error
			audience, parseErr := security.ParseAudience(firstCredential(md, "x-memstation-audience"))
			if parseErr != nil || len(md.Get("x-memstation-audience")) > 1 {
				return nil, status.Error(codes.PermissionDenied, "audience access denied")
			}
			ctx, err = auth.AuthenticateAudienceContext(ctx, token, firstCredential(md, "x-memstation-workspace"), audience)
			if len(md.Get("x-memstation-workspace")) > 1 {
				err = domain.ErrAccessDenied
			}
			if err != nil {
				if errors.Is(err, domain.ErrAccessDenied) {
					return nil, status.Error(codes.PermissionDenied, "workspace access denied")
				}
				return nil, status.Error(codes.Unauthenticated, "invalid api key")
			}
			userID := security.UserIDFromContext(ctx)
			if capability := grpcProxyCapability(info.FullMethod); capability != "" {
				if err := security.RequireCapability(ctx, capability); err != nil {
					return nil, status.Error(codes.PermissionDenied, "capability required")
				}
			}
			if limiter != nil && !limiter.Allow(security.RateLimitKeyFromToken(userID, token, info.FullMethod)) {
				return nil, status.Error(codes.ResourceExhausted, "proxy rate limit exceeded")
			}
			ctx = security.WithUserID(ctx, userID)
		case isAdminMethod(info.FullMethod):
			if !authenticateAdminMetadata(ctx, auth) {
				return nil, status.Error(codes.Unauthenticated, "admin authentication required")
			}
			ctx = security.WithAdmin(ctx, true)
		}
		return handler(ctx, req)
	}
}

func isProxyMethod(method string) bool {
	if strings.HasPrefix(method, "/memstation.v1.MemoryDataService/") {
		return true
	}
	_, ok := proxyMethodSet[method]
	return ok
}

func isAdminMethod(method string) bool {
	if strings.HasPrefix(method, "/memstation.v1.WorkspaceAdminService/") {
		return true
	}
	_, ok := adminMethodSet[method]
	return ok
}

func authenticateAdminMetadata(ctx context.Context, auth security.Authenticator) bool {
	md, _ := metadata.FromIncomingContext(ctx)
	token := firstCredential(md, "authorization", "x-admin-token")
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	return auth.AuthenticateAdminToken(token)
}

func firstCredential(md metadata.MD, keys ...string) string {
	for _, key := range keys {
		values := md.Get(strings.ToLower(key))
		for _, value := range values {
			trimmed := strings.TrimSpace(value)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func grpcProxyCapability(method string) string {
	if strings.HasPrefix(method, "/memstation.v1.MemoryDataService/") {
		switch strings.TrimPrefix(method, "/memstation.v1.MemoryDataService/") {
		case "Probe", "Operation":
			return ""
		case "Observe", "Progress", "OpenWindow", "GetWindow", "Finalize":
			return domain.CapabilityEventsWrite
		case "Usage", "ReconcileUsage":
			return domain.CapabilityUsageRead
		case "Source":
			return domain.CapabilitySourcesRead
		case "Propose", "Candidates":
			return domain.CapabilityCandidatesWrite
		case "Retract":
			return domain.CapabilitySourcesRetract
		case "Forget":
			return domain.CapabilityMemoryForget
		case "Export":
			return domain.CapabilityMemoryExport
		default:
			return domain.CapabilityMemoryRead
		}
	}

	switch method {
	case pb.GatewayService_Complete_FullMethodName, pb.GatewayService_ListModels_FullMethodName:
		return domain.CapabilityGateway
	case pb.MemoryService_UpsertKeyMemory_FullMethodName, pb.MemoryService_DeleteKeyMemory_FullMethodName:
		return domain.CapabilityMemoryEdit
	default:
		return domain.CapabilityMemoryRead
	}
}

func memoryRecoveryInterceptor(check func(context.Context) error) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if check != nil && (isProxyMethod(info.FullMethod) || isAdminMethod(info.FullMethod)) && info.FullMethod != pb.GatewayService_ListModels_FullMethodName && info.FullMethod != pb.MemoryDataService_Probe_FullMethodName && info.FullMethod != pb.WorkspaceAdminService_Capabilities_FullMethodName {
			if err := check(ctx); err != nil {
				return nil, status.Error(codes.Unavailable, "memory_recovery_pending")
			}
		}
		return next(ctx, req)
	}
}
