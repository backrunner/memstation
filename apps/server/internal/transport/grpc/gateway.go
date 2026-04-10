package grpctransport

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"

	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/security"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	pb "memstation/apps/server/proto/memstation/v1"
)

type gatewayServiceServer struct {
	pb.UnimplementedGatewayServiceServer
	models  *modelcap.Registry
	gateway *gatewayservice.Service
}

func (s *gatewayServiceServer) ListModels(context.Context, *emptypb.Empty) (*pb.ListModelsResponse, error) {
	if s.models == nil {
		return nil, status.Error(codes.Unavailable, "model registry is not configured")
	}
	return &pb.ListModelsResponse{Items: toProtoModelCapabilities(s.models.List())}, nil
}

func (s *gatewayServiceServer) Complete(ctx context.Context, req *pb.CompleteRequest) (*pb.CompleteResponse, error) {
	if s.gateway == nil {
		return nil, status.Error(codes.Unavailable, "gateway service is not configured")
	}
	unified, err := fromProtoUnifiedRequest(req.GetRequest())
	if err != nil {
		return nil, err
	}
	if unified.Stream {
		return nil, status.Error(codes.InvalidArgument, "request.stream is not supported by the unary gRPC Complete RPC")
	}
	unified.UserID = security.UserIDFromContext(ctx)
	response, injection, err := s.gateway.Proxy(ctx, unified)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.CompleteResponse{
		Response:  toProtoUnifiedResponse(response),
		Injection: toProtoInjectionResult(injection),
	}, nil
}
