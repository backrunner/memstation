package grpctransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	ingestionservice "memstation/apps/server/internal/service/ingestion"
	lifecycleservice "memstation/apps/server/internal/service/lifecycle"
	pb "memstation/apps/server/proto/memstation/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type memoryDataServiceServer struct {
	pb.UnimplementedMemoryDataServiceServer
	deps Dependencies
}

func decodeContract(in proto.Message, out any) error {
	if in == nil || !in.ProtoReflect().IsValid() {
		return status.Error(codes.InvalidArgument, "request payload required")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid request")
	}
	if len(raw) > 2<<20 {
		return status.Error(codes.ResourceExhausted, "request too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		return status.Error(codes.InvalidArgument, "invalid request payload")
	}
	return nil
}

func contractResponse(in any, out proto.Message, err error) error {
	if err != nil {
		return contractError(err)
	}
	raw, err := json.Marshal(in)
	if err == nil {
		err = protojson.Unmarshal(raw, out)
	}
	if err != nil {
		return status.Error(codes.Internal, "invalid memory response")
	}
	return nil
}

func contractError(err error) error {
	if errors.Is(err, ingestionservice.ErrIdempotencyConflict) {
		return status.Error(codes.AlreadyExists, "idempotency_conflict")
	}
	if errors.Is(err, ingestionservice.ErrInvalidEvent) || errors.Is(err, lifecycleservice.ErrInvalidInput) {
		return status.Error(codes.InvalidArgument, "invalid_request")
	}
	if errors.Is(err, domain.ErrAccessDenied) {
		return status.Error(codes.PermissionDenied, "access_denied")
	}
	if errors.Is(err, domain.ErrAccessNotFound) {
		return status.Error(codes.NotFound, "not_found")
	}
	if errors.Is(err, domain.ErrAccessConflict) {
		return status.Error(codes.Aborted, "revision_conflict")
	}
	if err != nil {
		return status.Error(codes.Unavailable, "memory_unavailable")
	}
	return nil
}

func (s *memoryDataServiceServer) ready() error {
	if s.deps.Ingestion == nil || s.deps.Lifecycle == nil {
		return status.Error(codes.Unavailable, "memory_disabled")
	}
	return nil
}

func (s *memoryDataServiceServer) Probe(ctx context.Context, _ *emptypb.Empty) (*pb.MemoryCapabilities, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	out := &pb.MemoryCapabilities{}
	err := contractResponse(s.deps.Lifecycle.Probe(ctx, s.deps.Config.Version), out, nil)
	return out, err
}

func (s *memoryDataServiceServer) Observe(ctx context.Context, req *pb.ObserveRequest) (*pb.IngestionReceipt, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var batch domain.ObserveBatch
	if err := decodeContract(req.GetBatch(), &batch); err != nil {
		return nil, err
	}
	result, err := s.deps.Ingestion.Observe(ctx, batch, req.GetIdempotencyKey())
	out := &pb.IngestionReceipt{}
	if err == nil {
		err = security.RequireCapability(ctx, domain.CapabilityEventsWrite)
	}
	return out, contractResponse(result, out, err)
}

func (s *memoryDataServiceServer) Progress(ctx context.Context, req *pb.RecordRequest) (*pb.IngestionReceipt, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result, err := s.deps.Ingestion.Progress(ctx, req.GetId())
	out := &pb.IngestionReceipt{}
	return out, contractResponse(result, out, err)
}

func (s *memoryDataServiceServer) OpenWindow(ctx context.Context, req *pb.OpenWindowRequest) (*pb.ConversationWindow, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var in domain.ConversationWindowInput
	if err := decodeContract(req.GetWindow(), &in); err != nil {
		return nil, err
	}
	result, err := s.deps.Ingestion.OpenWindow(ctx, in, req.GetIdempotencyKey())
	out := &pb.ConversationWindow{}
	return out, contractResponse(result, out, err)
}

func (s *memoryDataServiceServer) GetWindow(ctx context.Context, req *pb.RecordRequest) (*pb.ConversationWindow, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result, err := s.deps.Ingestion.Window(ctx, req.GetId())
	out := &pb.ConversationWindow{}
	return out, contractResponse(result, out, err)
}

func (s *memoryDataServiceServer) Finalize(ctx context.Context, req *pb.FinalizeWindowRequest) (*pb.ConversationWindow, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.deps.Ingestion.Finalize(ctx, req.GetId(), req.GetExpectedRevision()); err != nil {
		return nil, contractError(err)
	}
	return s.GetWindow(ctx, &pb.RecordRequest{Id: req.GetId()})
}

func (s *memoryDataServiceServer) Inspect(ctx context.Context, req *pb.RecordRequest) (*pb.MemoryDocument, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result, err := s.deps.Lifecycle.Inspect(ctx, req.GetId())
	if err != nil {
		return nil, contractError(err)
	}
	return toProtoMemoryDocument(result), nil
}

func (s *memoryDataServiceServer) Source(ctx context.Context, req *pb.RecordRequest) (*pb.SourceRecord, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result, err := s.deps.Ingestion.Source(ctx, req.GetId())
	if err == nil {
		err = security.RequireCapability(ctx, domain.CapabilitySourcesRead)
	}
	out := &pb.SourceRecord{}
	return out, contractResponse(result, out, err)
}

func (s *memoryDataServiceServer) Propose(ctx context.Context, req *pb.CandidateInput) (*pb.MemoryCandidate, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var in domain.MemoryCandidate
	if err := decodeContract(req, &in); err != nil {
		return nil, err
	}
	result, err := s.deps.Lifecycle.Propose(ctx, in)
	out := &pb.MemoryCandidate{}
	return out, contractResponse(result, out, err)
}

func (s *memoryDataServiceServer) Candidates(ctx context.Context, req *pb.RecordRequest) (*pb.CandidateList, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result, err := s.deps.Lifecycle.Candidates(ctx, req.GetId(), req.GetAfter())
	out := &pb.CandidateList{}
	return out, contractResponse(map[string]any{"items": result}, out, err)
}

func (s *memoryDataServiceServer) Retract(ctx context.Context, req *pb.ForgetRequest) (*pb.MemoryOperation, error) {
	return s.forget(ctx, req, true)
}

func (s *memoryDataServiceServer) Forget(ctx context.Context, req *pb.ForgetRequest) (*pb.MemoryOperation, error) {
	return s.forget(ctx, req, false)
}

func (s *memoryDataServiceServer) forget(ctx context.Context, req *pb.ForgetRequest, retract bool) (*pb.MemoryOperation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var in domain.ForgetSelector
	if err := decodeContract(req.GetSelector(), &in); err != nil {
		return nil, err
	}
	var result domain.MemoryOperation
	var err error
	if retract {
		result, err = s.deps.Lifecycle.Retract(ctx, in, req.GetIdempotencyKey())
	} else {
		result, err = s.deps.Lifecycle.Forget(ctx, in, req.GetIdempotencyKey())
	}
	out := &pb.MemoryOperation{}
	return out, contractResponse(result, out, err)
}

func (s *memoryDataServiceServer) Export(ctx context.Context, req *pb.ExportRequest) (*pb.MemoryOperation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result, err := s.deps.Lifecycle.Export(ctx, req.GetIdempotencyKey())
	out := &pb.MemoryOperation{}
	return out, contractResponse(result, out, err)
}

func (s *memoryDataServiceServer) Operation(ctx context.Context, req *pb.RecordRequest) (*pb.MemoryOperation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result, err := s.deps.Lifecycle.Operation(ctx, req.GetId())
	out := &pb.MemoryOperation{}
	return out, contractResponse(result, out, err)
}

type workspaceAdminServiceServer struct {
	pb.UnimplementedWorkspaceAdminServiceServer
	deps Dependencies
}

func (s *workspaceAdminServiceServer) scope(ctx context.Context, in *pb.AdminWorkspaceScope) (context.Context, error) {
	if s.deps.Identity == nil || s.deps.Lifecycle == nil {
		return ctx, status.Error(codes.Unavailable, "memory_disabled")
	}
	bound, err := s.deps.Identity.AdministrativeDataContext(ctx, in.GetUserId(), in.GetWorkspaceId(), in.GetPartitionId())
	if err != nil {
		return ctx, contractError(err)
	}
	if in.GetPartitionId() != "" && s.deps.Audit != nil {
		s.deps.Audit.Record(bound, domain.AuditEvent{ActorType: "admin", ActorID: "admin", Action: "inspect_private_workspace", TargetType: "workspace", TargetID: in.GetWorkspaceId(), Payload: map[string]any{"partition_id": in.GetPartitionId()}})
	}
	return bound, nil
}

func (s *workspaceAdminServiceServer) ReviewCandidate(ctx context.Context, req *pb.ReviewCandidateRequest) (*pb.MemoryCandidate, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Lifecycle.Decide(ctx, domain.CandidateDecision{ID: req.GetId(), ExpectedRevision: req.GetExpectedRevision(), Decision: req.GetDecision(), ConfirmedKind: req.GetConfirmedKind()})
	out := &pb.MemoryCandidate{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) Publish(ctx context.Context, req *pb.PublishRequest) (*pb.MemoryPublication, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	var in domain.PublicationInput
	if err = decodeContract(req.GetPublication(), &in); err != nil {
		return nil, err
	}
	result, err := s.deps.Lifecycle.Publish(ctx, in)
	out := &pb.MemoryPublication{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) Publications(ctx context.Context, req *pb.AdminWorkspaceScope) (*pb.PublicationList, error) {
	ctx, err := s.scope(ctx, req)
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Lifecycle.Publications(ctx, req.GetAfter())
	out := &pb.PublicationList{}
	return out, contractResponse(map[string]any{"items": result}, out, err)
}

func (s *workspaceAdminServiceServer) RetractPublication(ctx context.Context, req *pb.RetractPublicationRequest) (*pb.MemoryPublication, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Lifecycle.RetractPublication(ctx, req.GetId(), req.GetExpectedRevision())
	out := &pb.MemoryPublication{}
	return out, contractResponse(result, out, err)
}

func (s *memoryDataServiceServer) Usage(ctx context.Context, req *pb.AIUsageRequest) (*pb.AIUsageResponse, error) {
	if s.deps.AIUsage == nil {
		return nil, status.Error(codes.Unavailable, "usage unavailable")
	}
	items, err := s.deps.AIUsage.List(ctx, req.GetAfter(), int(req.GetLimit()))
	out := &pb.AIUsageResponse{}
	return out, contractResponse(map[string]any{"items": items, "usage_authority": "broker", "additional_charge": false}, out, err)
}

func (s *memoryDataServiceServer) ReconcileUsage(ctx context.Context, req *pb.RecordRequest) (*pb.AICall, error) {
	if s.deps.AIUsage == nil {
		return nil, status.Error(codes.Unavailable, "usage unavailable")
	}
	result, err := s.deps.AIUsage.Reconcile(ctx, req.GetId())
	out := &pb.AICall{}
	return out, contractResponse(result, out, err)
}

func (s *workspaceAdminServiceServer) ReconcileUsage(ctx context.Context, req *pb.AdminRecordRequest) (*pb.AICall, error) {
	ctx, err := s.scope(ctx, req.GetScope())
	if err != nil {
		return nil, err
	}
	return (&memoryDataServiceServer{deps: s.deps}).ReconcileUsage(ctx, &pb.RecordRequest{Id: req.GetId()})
}
