package grpctransport

import (
	"context"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	pb "memstation/apps/server/proto/memstation/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *workspaceAdminServiceServer) admin(ctx context.Context) error {
	if !security.AdminFromContext(ctx) {
		return contractError(domain.ErrAccessDenied)
	}
	if s.deps.Identity == nil {
		return status.Error(codes.Unavailable, "identity unavailable")
	}
	return nil
}

func (s *workspaceAdminServiceServer) CreateWorkspace(ctx context.Context, req *pb.WorkspaceConfig) (*pb.WorkspaceConfig, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	var input domain.Workspace
	if err := decodeContract(req, &input); err != nil {
		return nil, err
	}
	result, err := s.deps.Identity.CreateWorkspace(ctx, input)
	out := &pb.WorkspaceConfig{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) UpdateWorkspace(ctx context.Context, req *pb.WorkspaceConfig) (*pb.WorkspaceConfig, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	var input domain.Workspace
	if err := decodeContract(req, &input); err != nil {
		return nil, err
	}
	if input.Capabilities == nil {
		input.Capabilities = []string{}
	}
	result, err := s.deps.Identity.UpdateWorkspace(ctx, input)
	out := &pb.WorkspaceConfig{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) PutIntegration(ctx context.Context, req *pb.IntegrationConfig) (*pb.IntegrationConfig, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	var input domain.IntegrationConfig
	if err := decodeContract(req, &input); err != nil {
		return nil, err
	}
	result, err := s.deps.Identity.PutIntegration(ctx, input)
	out := &pb.IntegrationConfig{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) PutPartition(ctx context.Context, req *pb.PartitionConfig) (*pb.PartitionConfig, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	var input domain.MemoryPartition
	if err := decodeContract(req, &input); err != nil {
		return nil, err
	}
	result, err := s.deps.Identity.PutPartition(ctx, input)
	out := &pb.PartitionConfig{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) SetGrant(ctx context.Context, req *pb.WorkspaceGrant) (*pb.WorkspaceGrant, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	var input domain.WorkspaceGrant
	if err := decodeContract(req, &input); err != nil {
		return nil, err
	}
	if input.Capabilities == nil {
		input.Capabilities = []string{}
	}
	result, err := s.deps.Identity.SetGrant(ctx, input)
	out := &pb.WorkspaceGrant{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) Workspaces(ctx context.Context, req *pb.WorkspaceListRequest) (*pb.WorkspaceListResponse, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	result, err := s.deps.Identity.Workspaces(ctx, domain.WorkspaceQuery{UserID: req.GetUserId(), After: req.GetAfter(), Query: req.GetQuery(), Limit: int(req.GetLimit())})
	out := &pb.WorkspaceListResponse{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) Grant(ctx context.Context, req *pb.WorkspaceGrantRequest) (*pb.WorkspaceGrant, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Identity.WorkspaceGrant(ctx, req.GetScope().GetUserId(), req.GetScope().GetWorkspaceId(), req.GetAgentId())
	out := &pb.WorkspaceGrant{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) Integrations(ctx context.Context, req *pb.AdminWorkspaceScope) (*pb.IntegrationList, error) {
	ctx, err := s.scope(ctx, req)
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Identity.Integrations(ctx, req.GetUserId(), req.GetWorkspaceId())
	out := &pb.IntegrationList{}
	return out, contractResponse(map[string]any{"items": result}, out, err)
}

func (s *workspaceAdminServiceServer) Partitions(ctx context.Context, req *pb.AdminWorkspaceScope) (*pb.PartitionList, error) {
	ctx, err := s.scope(ctx, req)
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Identity.Partitions(ctx, req.GetUserId(), req.GetWorkspaceId())
	out := &pb.PartitionList{}
	return out, contractResponse(map[string]any{"items": result}, out, err)
}

func (s *workspaceAdminServiceServer) Control(ctx context.Context, req *pb.MemoryControlRequest) (*pb.MemoryControlOverview, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	if s.deps.MemoryControl == nil {
		return nil, status.Error(codes.Unavailable, "control unavailable")
	}
	result, err := s.deps.MemoryControl.Overview(ctx, req.GetAfter(), int(req.GetLimit()))
	out := &pb.MemoryControlOverview{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) ReplayJob(ctx context.Context, req *pb.AdminRecordRequest) (*emptypb.Empty, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	if s.deps.MemoryControl == nil {
		return nil, status.Error(codes.Unavailable, "control unavailable")
	}
	return &emptypb.Empty{}, contractError(s.deps.MemoryControl.Replay(ctx, req.GetId()))
}

func (s *workspaceAdminServiceServer) Inspect(ctx context.Context, req *pb.AdminRecordRequest) (*pb.MemoryDocument, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Inspect(ctx, &pb.RecordRequest{Id: req.GetId(), After: req.GetAfter()})
}

func (s *workspaceAdminServiceServer) Source(ctx context.Context, req *pb.AdminRecordRequest) (*pb.SourceRecord, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Source(ctx, &pb.RecordRequest{Id: req.GetId(), After: req.GetAfter()})
}

func (s *workspaceAdminServiceServer) Progress(ctx context.Context, req *pb.AdminRecordRequest) (*pb.IngestionReceipt, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Progress(ctx, &pb.RecordRequest{Id: req.GetId(), After: req.GetAfter()})
}

func (s *workspaceAdminServiceServer) Candidates(ctx context.Context, req *pb.AdminRecordRequest) (*pb.CandidateList, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Candidates(ctx, &pb.RecordRequest{Id: req.GetId(), After: req.GetAfter()})
}

func (s *workspaceAdminServiceServer) Operation(ctx context.Context, req *pb.AdminRecordRequest) (*pb.MemoryOperation, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Operation(ctx, &pb.RecordRequest{Id: req.GetId(), After: req.GetAfter()})
}

func (s *workspaceAdminServiceServer) Propose(ctx context.Context, req *pb.AdminCandidateRequest) (*pb.MemoryCandidate, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Propose(ctx, req.GetCandidate())
}

func (s *workspaceAdminServiceServer) Forget(ctx context.Context, req *pb.AdminForgetRequest) (*pb.MemoryOperation, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Forget(ctx, req.GetRequest())
}

func (s *workspaceAdminServiceServer) Retract(ctx context.Context, req *pb.AdminForgetRequest) (*pb.MemoryOperation, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Retract(ctx, req.GetRequest())
}

func (s *workspaceAdminServiceServer) Export(ctx context.Context, req *pb.AdminExportRequest) (*pb.MemoryOperation, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Export(ctx, &pb.ExportRequest{IdempotencyKey: req.GetIdempotencyKey()})
}

func (s *workspaceAdminServiceServer) Usage(ctx context.Context, req *pb.AdminUsageRequest) (*pb.AIUsageResponse, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Usage(ctx, req.GetRequest())
}

func (s *workspaceAdminServiceServer) CreateAgent(ctx context.Context, req *pb.WorkspaceAgentCreate) (*pb.WorkspaceAgent, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Identity.CreateWorkspaceAgent(ctx, req.GetScope().GetUserId(), req.GetScope().GetWorkspaceId(), req.GetName(), req.GetCapabilities())
	out := &pb.WorkspaceAgent{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) Agent(ctx context.Context, req *pb.WorkspaceGrantRequest) (*pb.WorkspaceAgentDetail, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Identity.WorkspaceAgentDetail(ctx, req.GetScope().GetUserId(), req.GetScope().GetWorkspaceId(), req.GetAgentId())
	out := &pb.WorkspaceAgentDetail{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) Generations(ctx context.Context, req *pb.AdminWorkspaceScope) (*pb.GenerationList, error) {
	ctx, err := s.scope(ctx, req)
	if err != nil {
		return nil, err
	}
	if s.deps.Generations == nil {
		return nil, status.Error(codes.Unavailable, "generation service unavailable")
	}
	result, err := s.deps.Generations.ListGenerations(ctx, req.GetUserId(), 100)
	out := &pb.GenerationList{}
	return out, contractResponse(map[string]any{"items": result}, out, err)
}

func (s *workspaceAdminServiceServer) Rebuild(ctx context.Context, req *pb.GenerationRebuildRequest) (*pb.MemoryGeneration, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	if s.deps.Generations == nil || !s.deps.Generations.DurableTasks() {
		return nil, status.Error(codes.Unavailable, "durable generation service unavailable")
	}
	result, _, err := s.deps.Generations.CreateRebuildGeneration(ctx, domain.CreateMemoryRebuildInput{UserID: req.GetScope().GetUserId(), WorkspaceID: req.GetScope().GetWorkspaceId(), Scope: "user", EmbeddingModelOverride: req.GetEmbeddingModelOverride(), ActivateOnSuccess: req.GetActivateOnSuccess()})
	out := &pb.MemoryGeneration{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) ActivateGeneration(ctx context.Context, req *pb.AdminRecordRequest) (*pb.MemoryGeneration, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	if s.deps.Generations == nil {
		return nil, status.Error(codes.Unavailable, "generation service unavailable")
	}
	result, err := s.deps.Generations.ActivateGeneration(ctx, req.GetScope().GetUserId(), req.GetId())
	out := &pb.MemoryGeneration{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) DiscardGeneration(ctx context.Context, req *pb.AdminRecordRequest) (*pb.MemoryGeneration, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	if s.deps.Generations == nil {
		return nil, status.Error(codes.Unavailable, "generation service unavailable")
	}
	result, err := s.deps.Generations.DiscardGeneration(ctx, req.GetScope().GetUserId(), req.GetId())
	out := &pb.MemoryGeneration{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) Capabilities(ctx context.Context, req *pb.AdminWorkspaceScope) (*pb.MemoryCapabilities, error) {
	ctx, err := s.scope(ctx, req)
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).Probe(ctx, &emptypb.Empty{})
}
