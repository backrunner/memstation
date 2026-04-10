package grpctransport

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"memstation/apps/server/internal/domain"
	adminservice "memstation/apps/server/internal/service/admin"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	pb "memstation/apps/server/proto/memstation/v1"
)

type adminServiceServer struct {
	pb.UnimplementedAdminServiceServer
	admin       *adminservice.Service
	memory      *memoryservice.Service
	generations *memorygenservice.Service
}

func (s *adminServiceServer) Overview(ctx context.Context, req *pb.OverviewRequest) (*pb.OverviewResponse, error) {
	if s.admin == nil {
		return nil, status.Error(codes.Unavailable, "admin service is not configured")
	}
	overview, err := s.admin.Overview(ctx, defaultAdminUserID(req.GetUserId()))
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.OverviewResponse{Overview: toProtoStruct(overview)}, nil
}

func (s *adminServiceServer) ListConversations(ctx context.Context, req *pb.ListConversationsRequest) (*pb.ListConversationsResponse, error) {
	if s.admin == nil {
		return nil, status.Error(codes.Unavailable, "admin service is not configured")
	}
	items, err := s.admin.ListConversations(ctx, defaultAdminUserID(req.GetUserId()), int(req.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	out := &pb.ListConversationsResponse{Items: make([]*pb.ConversationListItem, 0, len(items))}
	for _, item := range items {
		out.Items = append(out.Items, toProtoConversationListItem(adminConversationListItem{
			ConversationID: item.ConversationID,
			UserID:         item.UserID,
			Provider:       item.Provider,
			Protocol:       item.Protocol,
			Mode:           item.Mode,
			FinalizeStatus: item.FinalizeStatus,
			SummaryStatus:  item.SummaryStatus,
			AnalysisStatus: item.AnalysisStatus,
			MessageCount:   item.MessageCount,
			TotalTokens:    item.TotalTokens,
			UpdatedAt:      item.UpdatedAt,
			Metadata:       item.Metadata,
		}))
	}
	return out, nil
}

func (s *adminServiceServer) GetConversation(ctx context.Context, req *pb.GetConversationRequest) (*pb.ConversationRecord, error) {
	if s.admin == nil {
		return nil, status.Error(codes.Unavailable, "admin service is not configured")
	}
	if strings.TrimSpace(req.GetConversationId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "conversation_id is required")
	}
	item, err := s.admin.ConversationDetail(ctx, defaultAdminUserID(req.GetUserId()), req.GetConversationId(), strings.TrimSpace(req.GetGenerationId()))
	if err != nil {
		return nil, mapError(err)
	}
	return toProtoConversationRecord(item), nil
}

func (s *adminServiceServer) GetConversationMemory(ctx context.Context, req *pb.GetConversationMemoryRequest) (*pb.ConversationMemoryResponse, error) {
	if strings.TrimSpace(req.GetConversationId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "conversation_id is required")
	}
	userID := defaultAdminUserID(req.GetUserId())
	if s.memory == nil {
		if s.admin == nil {
			return nil, status.Error(codes.Unavailable, "conversation memory service is not configured")
		}
		item, err := s.admin.ConversationMemory(ctx, userID, req.GetConversationId())
		if err != nil {
			return nil, mapError(err)
		}
		out := &pb.ConversationMemoryResponse{
			ConversationId: req.GetConversationId(),
			KeyMemories:    make([]*pb.KeyMemory, 0, len(item.KeyMemories)),
			Documents:      make([]*pb.MemoryDocument, 0, len(item.Documents)),
		}
		for _, document := range item.Documents {
			out.Documents = append(out.Documents, toProtoMemoryDocument(document))
		}
		for _, memory := range item.KeyMemories {
			out.KeyMemories = append(out.KeyMemories, toProtoKeyMemory(memory))
		}
		return out, nil
	}

	resolvedGenerationID := strings.TrimSpace(req.GetGenerationId())
	if resolvedGenerationID == "" {
		if s.generations != nil {
			generation, err := s.generations.GetActiveGeneration(ctx, userID)
			if err != nil {
				return nil, mapError(err)
			}
			resolvedGenerationID = generation.GenerationID
		} else {
			resolvedGenerationID = memorygenservice.CurrentGenerationID(userID)
		}
	}

	documents, err := s.memory.ListDocumentsByConversationInGeneration(ctx, userID, resolvedGenerationID, req.GetConversationId())
	if err != nil {
		return nil, mapError(err)
	}
	keyMemories, err := s.memory.ListKeyMemories(ctx, userID, 200)
	if err != nil {
		return nil, mapError(err)
	}
	filtered := make([]domain.KeyMemory, 0)
	for _, item := range keyMemories {
		if item.Value == req.GetConversationId() || anyString(item.Metadata["conversation_id"]) == req.GetConversationId() {
			filtered = append(filtered, item)
		}
	}

	out := &pb.ConversationMemoryResponse{
		ConversationId: req.GetConversationId(),
		GenerationId:   resolvedGenerationID,
		Documents:      make([]*pb.MemoryDocument, 0, len(documents)),
		KeyMemories:    make([]*pb.KeyMemory, 0, len(filtered)),
	}
	for _, item := range documents {
		out.Documents = append(out.Documents, toProtoMemoryDocument(item))
	}
	for _, item := range filtered {
		out.KeyMemories = append(out.KeyMemories, toProtoKeyMemory(item))
	}
	return out, nil
}

func anyString(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}
