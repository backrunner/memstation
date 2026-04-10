package grpctransport

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	auditsvc "memstation/apps/server/internal/service/audit"
	memoryservice "memstation/apps/server/internal/service/memory"
	pb "memstation/apps/server/proto/memstation/v1"
)

type memoryServiceServer struct {
	pb.UnimplementedMemoryServiceServer
	memory *memoryservice.Service
	audit  *auditsvc.Service
}

func (s *memoryServiceServer) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	if s.memory == nil {
		return nil, status.Error(codes.Unavailable, "memory service is not configured")
	}
	memoryTypes, err := parseMemoryTypes(req.GetMemoryTypes())
	if err != nil {
		return nil, err
	}
	timeTiers, err := parseTimeTiers(req.GetTimeTiers())
	if err != nil {
		return nil, err
	}
	dateFrom, err := parseOptionalTimestamp(req.GetDateFrom(), "date_from")
	if err != nil {
		return nil, err
	}
	dateTo, err := parseOptionalTimestamp(req.GetDateTo(), "date_to")
	if err != nil {
		return nil, err
	}
	result, err := s.memory.Search(ctx, domain.SearchInput{
		UserID:              security.UserIDFromContext(ctx),
		GenerationID:        strings.TrimSpace(req.GetGenerationId()),
		Query:               req.GetQuery(),
		TopN:                int(req.GetTopN()),
		IncludeConversation: req.GetIncludeConversation(),
		IncludeSegments:     req.GetIncludeSegments(),
		MemoryTypes:         memoryTypes,
		TimeTiers:           timeTiers,
		DateFrom:            dateFrom,
		DateTo:              dateTo,
		MinScore:            req.GetMinScore(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return toProtoSearchResult(result), nil
}

func (s *memoryServiceServer) ListKeyMemories(ctx context.Context, req *pb.ListKeyMemoriesRequest) (*pb.ListKeyMemoriesResponse, error) {
	if s.memory == nil {
		return nil, status.Error(codes.Unavailable, "memory service is not configured")
	}
	timeTiers, err := parseTimeTiers(req.GetTimeTiers())
	if err != nil {
		return nil, err
	}
	dateFrom, err := parseOptionalTimestamp(req.GetDateFrom(), "date_from")
	if err != nil {
		return nil, err
	}
	dateTo, err := parseOptionalTimestamp(req.GetDateTo(), "date_to")
	if err != nil {
		return nil, err
	}
	items, err := s.memory.SearchKeyMemories(ctx, domain.KeyMemoryQuery{
		UserID:    security.UserIDFromContext(ctx),
		Query:     req.GetQuery(),
		TopK:      int(req.GetTopK()),
		TimeTiers: timeTiers,
		DateFrom:  dateFrom,
		DateTo:    dateTo,
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := &pb.ListKeyMemoriesResponse{Items: make([]*pb.KeyMemory, 0, len(items))}
	for _, item := range items {
		out.Items = append(out.Items, toProtoKeyMemory(item))
	}
	return out, nil
}

func (s *memoryServiceServer) UpsertKeyMemory(ctx context.Context, req *pb.UpsertKeyMemoryRequest) (*pb.KeyMemory, error) {
	if s.memory == nil {
		return nil, status.Error(codes.Unavailable, "memory service is not configured")
	}
	key := strings.TrimSpace(req.GetKey())
	if key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}
	if strings.TrimSpace(req.GetValue()) == "" {
		return nil, status.Error(codes.InvalidArgument, "value is required")
	}

	userID := security.UserIDFromContext(ctx)
	action := "create_key_memory"
	items, err := s.memory.ListKeyMemories(ctx, userID, 200)
	if err == nil {
		for _, item := range items {
			if item.Key == key {
				action = "update_key_memory"
				break
			}
		}
	}

	lastConfirmedAt, err := parseOptionalTimestamp(req.GetLastConfirmedAt(), "last_confirmed_at")
	if err != nil {
		return nil, err
	}
	item, err := s.memory.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID:          userID,
		Key:             key,
		Value:           req.GetValue(),
		Category:        req.GetCategory(),
		Source:          defaultKeyMemorySource(req.GetSource()),
		ReviewStatus:    req.GetReviewStatus(),
		LastConfirmedAt: lastConfirmedAt,
		Metadata:        fromProtoStruct(req.GetMetadata()),
	})
	if err != nil {
		return nil, mapError(err)
	}
	if s.audit != nil {
		s.audit.Record(ctx, domain.AuditEvent{
			ActorType:  "user",
			ActorID:    userID,
			Action:     action,
			TargetType: "key_memory",
			TargetID:   item.ID,
			Payload: map[string]any{
				"key":      item.Key,
				"category": item.Category,
			},
			CreatedAt: time.Now().UTC(),
		})
	}
	return toProtoKeyMemory(item), nil
}

func (s *memoryServiceServer) DeleteKeyMemory(ctx context.Context, req *pb.DeleteKeyMemoryRequest) (*pb.DeleteKeyMemoryResponse, error) {
	if s.memory == nil {
		return nil, status.Error(codes.Unavailable, "memory service is not configured")
	}
	key := strings.TrimSpace(req.GetKey())
	if key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}
	userID := security.UserIDFromContext(ctx)
	if err := s.memory.DeleteKeyMemory(ctx, userID, key); err != nil {
		return nil, mapError(err)
	}
	if s.audit != nil {
		s.audit.Record(ctx, domain.AuditEvent{
			ActorType:  "user",
			ActorID:    userID,
			Action:     "delete_key_memory",
			TargetType: "key_memory",
			TargetID:   key,
			Payload:    map[string]any{"key": key},
			CreatedAt:  time.Now().UTC(),
		})
	}
	return &pb.DeleteKeyMemoryResponse{Deleted: true}, nil
}

func defaultKeyMemorySource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "grpc"
	}
	return source
}
