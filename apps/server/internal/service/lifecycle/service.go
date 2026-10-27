package lifecycle

import (
	"context"
	"errors"
	"slices"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

var ErrInvalidInput = errors.New("invalid memory lifecycle input")

type Service struct {
	ready   func(context.Context) error
	runtime domain.MemoryRuntime
	repo    ports.MemoryLifecycleRepository
}

func NewService(repo ports.MemoryLifecycleRepository) *Service {
	return &Service{repo: repo, runtime: domain.MemoryRuntime{AIBackend: "direct", RoleAliases: map[string]string{}, UsageAuthority: "memstation"}}
}
func (s *Service) SetRuntime(info domain.MemoryRuntime)       { s.runtime = info }
func (s *Service) SetReady(check func(context.Context) error) { s.ready = check }

func (s *Service) Inspect(ctx context.Context, id string) (domain.MemoryDocument, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityMemoryRead); err != nil {
		return domain.MemoryDocument{}, err
	}
	doc, err := s.repo.Inspect(ctx, id)
	if err != nil {
		return doc, err
	}
	if err = security.RequireCapability(ctx, domain.CapabilityMemoryRead); err != nil {
		return domain.MemoryDocument{}, err
	}
	if visible, ok := security.MemoryContent(ctx, doc); ok {
		return visible, nil
	}
	return domain.MemoryDocument{}, domain.ErrAccessNotFound
}

func (s *Service) Propose(ctx context.Context, c domain.MemoryCandidate) (domain.MemoryCandidate, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityCandidatesWrite); err != nil {
		return c, err
	}
	if strings.TrimSpace(c.Content) == "" || len(c.Content) > 65536 || len(c.SubjectActorIDs) > 64 || len(c.SourceRefs) > 100 || !slices.Contains([]string{"fact", "preference", "event", "relationship", "group_habit", "summary", "inference"}, c.Kind) {
		return c, ErrInvalidInput
	}
	if (c.TargetNodeID == "") != (c.TargetRevision == 0) || c.TargetRevision < 0 {
		return c, ErrInvalidInput
	}
	c.Origin = "tool"
	if security.AdminFromContext(ctx) {
		c.Origin = "human"
	}
	return s.repo.Propose(ctx, c)
}

func (s *Service) Candidates(ctx context.Context, id string, after ...string) ([]domain.MemoryCandidate, error) {
	if len(after) > 1 || len(after) == 1 && len(after[0]) > 256 {
		return nil, ErrInvalidInput
	}
	if err := security.RequireCapability(ctx, domain.CapabilityCandidatesWrite); err != nil {
		return nil, err
	}
	out, err := s.repo.Candidates(ctx, id, after...)
	if err != nil {
		return nil, err
	}
	if err = security.RequireCapability(ctx, domain.CapabilityCandidatesWrite); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) Decide(ctx context.Context, d domain.CandidateDecision) (domain.MemoryCandidate, error) {
	if !security.AdminFromContext(ctx) {
		return domain.MemoryCandidate{}, domain.ErrAccessDenied
	}
	if d.ID == "" || d.ExpectedRevision < 1 || !slices.Contains([]string{"approve", "reject"}, d.Decision) {
		return domain.MemoryCandidate{}, ErrInvalidInput
	}
	return s.repo.Decide(ctx, d)
}

func validKey(key string) bool {
	return strings.TrimSpace(key) != "" && len(key) <= 256 && !strings.ContainsRune(key, 0)
}

func (s *Service) Forget(ctx context.Context, q domain.ForgetSelector, key string) (domain.MemoryOperation, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityMemoryForget); err != nil {
		return domain.MemoryOperation{}, err
	}
	return s.forget(ctx, q, key)
}

func (s *Service) Retract(ctx context.Context, q domain.ForgetSelector, key string) (domain.MemoryOperation, error) {
	if err := security.RequireCapability(ctx, domain.CapabilitySourcesRetract); err != nil {
		return domain.MemoryOperation{}, err
	}
	if len(q.SourceRefs) == 0 || len(q.NodeIDs) > 0 || q.ActorID != "" || q.Workspace {
		return domain.MemoryOperation{}, ErrInvalidInput
	}
	return s.forget(ctx, q, key)
}

func (s *Service) forget(ctx context.Context, q domain.ForgetSelector, key string) (domain.MemoryOperation, error) {
	if !validKey(key) || len(q.SourceRefs) > 1000 || len(q.NodeIDs) > 1000 || len(q.SourceRefs)+len(q.NodeIDs) == 0 && q.ActorID == "" && !q.Workspace {
		return domain.MemoryOperation{}, ErrInvalidInput
	}
	return s.repo.Forget(ctx, q, key)
}

func (s *Service) Operation(ctx context.Context, id string) (domain.MemoryOperation, error) {
	out, err := s.repo.Operation(ctx, id)
	if err != nil {
		return out, err
	}
	capability := domain.CapabilityMemoryExport
	if out.Kind == "forget" {
		capability = domain.CapabilityMemoryForget
		if security.RequireCapability(ctx, capability) != nil {
			capability = domain.CapabilitySourcesRetract
		}
	}
	if err = security.RequireCapability(ctx, capability); err != nil {
		return domain.MemoryOperation{}, err
	}
	return out, nil
}

func (s *Service) Export(ctx context.Context, key string) (domain.MemoryOperation, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityMemoryExport); err != nil {
		return domain.MemoryOperation{}, err
	}
	if !validKey(key) {
		return domain.MemoryOperation{}, ErrInvalidInput
	}
	return s.repo.Export(ctx, key)
}

func (s *Service) Publish(ctx context.Context, in domain.PublicationInput) (domain.MemoryPublication, error) {
	if !security.AdminFromContext(ctx) {
		return domain.MemoryPublication{}, domain.ErrAccessDenied
	}
	if (in.ReplacesPublicationID == "") != (in.ExpectedPublicationRevision == 0) || in.ExpectedPublicationRevision < 0 {
		return domain.MemoryPublication{}, ErrInvalidInput
	}
	if in.SourceNodeID == "" || in.SourceRevision < 1 || in.TargetWorkspaceID == "" {
		return domain.MemoryPublication{}, ErrInvalidInput
	}
	return s.repo.Publish(ctx, in)
}

func (s *Service) Publications(ctx context.Context, after ...string) ([]domain.MemoryPublication, error) {
	if len(after) > 1 || len(after) == 1 && len(after[0]) > 256 {
		return nil, ErrInvalidInput
	}
	if !security.AdminFromContext(ctx) {
		return nil, domain.ErrAccessDenied
	}
	return s.repo.Publications(ctx, after...)
}

func (s *Service) RetractPublication(ctx context.Context, id string, revision int64) (domain.MemoryPublication, error) {
	if !security.AdminFromContext(ctx) {
		return domain.MemoryPublication{}, domain.ErrAccessDenied
	}
	if revision < 1 {
		return domain.MemoryPublication{}, ErrInvalidInput
	}
	return s.repo.RetractPublication(ctx, id, revision)
}

// Probe reports only the current bound scope and effective authorization. Data
// availability is checked through the same recovery barrier as reads and workers.
func (s *Service) Probe(ctx context.Context, version string) domain.MemoryCapabilities {
	a, _ := security.AccessFromContext(ctx)
	allowed := func(cap string) bool { return security.RequireCapability(ctx, cap) == nil }
	result := domain.MemoryCapabilities{
		Runtime: s.runtime, ContractVersion: "1.0", ServerVersion: version, Status: "ready", Access: a,
		Capabilities: map[string]bool{
			"server_isolation": true, "multi_workspace": true, "private_audience": true, "publishing": security.AdminFromContext(ctx),
			"independent_ingestion": allowed(domain.CapabilityEventsWrite), "actor_attribution": true, "external_actor_mentions": true, "batch_idempotency": true,
			"source_retraction": allowed(domain.CapabilitySourcesRetract), "forget": allowed(domain.CapabilityMemoryForget),
			"source_read": allowed(domain.CapabilitySourcesRead), "processing_watermarks": allowed(domain.CapabilityEventsWrite),
			"candidates": allowed(domain.CapabilityCandidatesWrite), "memory_inspect": allowed(domain.CapabilityMemoryRead),
			"candidate_review": security.AdminFromContext(ctx), "usage": allowed(domain.CapabilityUsageRead) && s.runtime.AIBackend == "broker", "broker": s.runtime.AIBackend == "broker", "recall_explanation": true, "export": allowed(domain.CapabilityMemoryExport),
		}, Limitations: []string{"Mimic adapter and broker require external integration acceptance", "backup expiration is managed by the deployment retention policy; restore requires the latest independent deletion journal"},
	}
	if s.ready == nil || s.ready(ctx) != nil {
		result.Status = "memory-disabled"
		for key := range result.Capabilities {
			result.Capabilities[key] = false
		}
		result.Limitations = append(result.Limitations, "durable memory or deletion recovery barrier unavailable")
	}
	return result
}
