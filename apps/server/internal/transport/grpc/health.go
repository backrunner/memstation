package grpctransport

import (
	"context"

	grpcHealthV1 "google.golang.org/grpc/health/grpc_health_v1"

	emptypb "google.golang.org/protobuf/types/known/emptypb"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/observability"
	pb "memstation/apps/server/proto/memstation/v1"
)

type healthServiceServer struct {
	pb.UnimplementedHealthServiceServer
	config bootstrap.Config
	health *observability.Registry
}

func (s *healthServiceServer) GetStatus(ctx context.Context, _ *emptypb.Empty) (*pb.HealthStatusResponse, error) {
	components := []domainHealthComponent{}
	statusValue := "ready"
	if s.health != nil {
		checked, err := s.health.Run(ctx)
		if err != nil {
			statusValue = "degraded"
		}
		for _, component := range checked {
			components = append(components, domainHealthComponent{
				Name:   component.Name,
				Status: component.Status,
				Error:  component.Error,
			})
		}
	}
	return &pb.HealthStatusResponse{
		Status:     statusValue,
		Version:    s.config.Version,
		Mode:       "gateway",
		Components: toProtoHealthComponents(components),
	}, nil
}

type readinessHealthService struct {
	grpcHealthV1.UnimplementedHealthServer
	health *observability.Registry
}

func (s *readinessHealthService) Check(ctx context.Context, _ *grpcHealthV1.HealthCheckRequest) (*grpcHealthV1.HealthCheckResponse, error) {
	out := &grpcHealthV1.HealthCheckResponse{Status: grpcHealthV1.HealthCheckResponse_SERVING}
	if s.health != nil {
		if _, err := s.health.Run(ctx); err != nil {
			out.Status = grpcHealthV1.HealthCheckResponse_NOT_SERVING
		}
	}
	return out, nil
}
