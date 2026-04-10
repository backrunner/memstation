package grpctransport

import (
	"context"

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
