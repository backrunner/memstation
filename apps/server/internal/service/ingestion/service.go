package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

var (
	ErrInvalidEvent        = errors.New("invalid source event")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
)

type Service struct {
	repo         ports.IngestionRepository
	media        ports.MediaRepository
	windowTokens int
}

func (s *Service) SetMediaRepository(media ports.MediaRepository) { s.media = media }

func NewService(repo ports.IngestionRepository, windowTokens int) *Service {
	if windowTokens <= 0 {
		windowTokens = 8192
	}
	return &Service{repo: repo, windowTokens: windowTokens}
}

func (s *Service) Observe(ctx context.Context, batch domain.ObserveBatch, key string) (domain.IngestionReceipt, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityEventsWrite); err != nil {
		return domain.IngestionReceipt{}, err
	}
	access, ok := security.AccessFromContext(ctx)
	if !ok || access.AgentID == "" || batch.WorkspaceID != access.WorkspaceID {
		return domain.IngestionReceipt{}, domain.ErrAccessDenied
	}
	if err := ValidateBatch(batch, key); err != nil {
		return domain.IngestionReceipt{}, err
	}
	if !security.CanReadPartition(ctx, batch.PartitionID) {
		return domain.IngestionReceipt{}, domain.ErrAccessDenied
	}
	bound, err := security.WithDataScope(ctx, access.UserID, access.WorkspaceID, batch.PartitionID)
	if err != nil {
		return domain.IngestionReceipt{}, err
	}
	for _, event := range batch.Events {
		for _, part := range event.Parts {
			if part.Media == nil || part.Media.BlobID == "" {
				continue
			}
			if err := security.RequireCapability(bound, domain.CapabilityFilesRead); err != nil {
				return domain.IngestionReceipt{}, err
			}
			if s.media == nil {
				return domain.IngestionReceipt{}, domain.ErrAccessDenied
			}
			blob, err := s.media.GetBlob(bound, access.UserID, part.Media.BlobID)
			if err != nil || blob.DeletedAt != nil {
				return domain.IngestionReceipt{}, domain.ErrAccessDenied
			}
		}
	}
	return s.repo.Observe(bound, batch, key, s.windowTokens)
}

func (s *Service) Progress(ctx context.Context, id string) (domain.IngestionReceipt, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityEventsWrite); err != nil {
		return domain.IngestionReceipt{}, err
	}
	return s.repo.Progress(ctx, id)
}

func (s *Service) Source(ctx context.Context, id string) (domain.SourceRecord, error) {
	if err := security.RequireCapability(ctx, domain.CapabilitySourcesRead); err != nil {
		return domain.SourceRecord{}, err
	}
	return s.repo.Source(ctx, id)
}

func (s *Service) Finalize(ctx context.Context, id string, revision int64) error {
	if err := security.RequireCapability(ctx, domain.CapabilityEventsWrite); err != nil {
		return err
	}
	if revision < 0 {
		return ErrInvalidEvent
	}
	return s.repo.FinalizeWindow(ctx, id, revision)
}

func ValidateBatch(batch domain.ObserveBatch, key string) error {
	if len(batch.Events) == 0 || len(batch.Events) > 100 || !validID(key) || !validID(batch.WorkspaceID) || !validID(batch.IntegrationID) || !validID(batch.ExternalSpaceID) || len(batch.ThreadID) > 256 {
		return ErrInvalidEvent
	}
	total := 0
	for _, event := range batch.Events {
		if !validID(event.EventID) || !validID(event.SourceMessageID) || event.SourceRevision < 1 || event.OccurredAt.IsZero() {
			return ErrInvalidEvent
		}
		if event.Type != "message.created" && event.Type != "message.edited" && event.Type != "message.deleted" {
			return ErrInvalidEvent
		}
		if event.Type == "message.deleted" {
			if len(event.Parts) != 0 {
				return ErrInvalidEvent
			}
			continue
		}
		if len(event.Parts) == 0 || len(event.Parts) > 64 {
			return ErrInvalidEvent
		}
		switch event.Speaker.Kind {
		case "human":
			if !validID(event.Speaker.ExternalID) {
				return ErrInvalidEvent
			}
		case "bot":
			if !validID(event.Speaker.ExternalID) || event.DeliveryStatus != "confirmed" || !validID(event.DeliveryReceipt) {
				return ErrInvalidEvent
			}
		case "unknown":
			if event.Speaker.ExternalID != "" || event.Speaker.ActorID != "" {
				return ErrInvalidEvent
			}
		default:
			return ErrInvalidEvent
		}
		for _, part := range event.Parts {
			if part.Type != "media" && part.Media != nil {
				return ErrInvalidEvent
			}
			if !utf8.ValidString(part.Text) || len(part.Text) > 65536 {
				return ErrInvalidEvent
			}
			total += len(part.Text)
			switch part.Type {
			case "media":
				if part.Media == nil {
					return ErrInvalidEvent
				}
				switch part.Media.Type {
				case domain.ContentBlockImage, domain.ContentBlockAudio, domain.ContentBlockVideo, domain.ContentBlockFile:
				default:
					return ErrInvalidEvent
				}
				if part.Media.BlobID == "" && part.Media.SourceURL == "" && part.Media.ProviderFileID == "" {
					return ErrInvalidEvent
				}
				encoded, err := json.Marshal(part.Media)
				if err != nil {
					return ErrInvalidEvent
				}
				total += len(encoded)
			case "text", "quote":
			case "mention":
				external := part.Author != nil && validID(part.Author.ExternalID) && (part.Author.Kind == "human" || part.Author.Kind == "bot")
				if !validID(part.ActorID) && !external {
					return ErrInvalidEvent
				}
			default:
				return ErrInvalidEvent
			}
			if part.Source != nil {
				if err := validateRef(batch.WorkspaceID, *part.Source); err != nil {
					return err
				}
			}
		}
		if event.ReplyTo != nil {
			if err := validateRef(batch.WorkspaceID, *event.ReplyTo); err != nil {
				return err
			}
		}
	}
	if total > 1<<20 {
		return ErrInvalidEvent
	}
	return nil
}

func validateRef(workspace string, ref domain.SourceRef) error {
	if ref.WorkspaceID != workspace || !validID(ref.MessageID) || ref.Revision < 1 || ref.PartIndex < 0 {
		return ErrInvalidEvent
	}
	if ref.Span != nil && (ref.Span.Start < 0 || ref.Span.End <= ref.Span.Start) {
		return ErrInvalidEvent
	}
	return nil
}

func validID(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 256 && !strings.ContainsRune(value, '\x00')
}

func (s *Service) OpenWindow(ctx context.Context, in domain.ConversationWindowInput, key string) (domain.ConversationWindow, error) {
	var out domain.ConversationWindow
	if err := security.RequireCapability(ctx, domain.CapabilityEventsWrite); err != nil {
		return out, err
	}
	a, ok := security.AccessFromContext(ctx)
	if !ok || a.WorkspaceID != in.WorkspaceID || !security.CanReadPartition(ctx, in.PartitionID) {
		return out, domain.ErrAccessDenied
	}
	if !validID(in.WorkspaceID) || !validID(in.IntegrationID) || !validID(in.ExternalSpaceID) || !validID(key) || len(in.ThreadID) > 256 {
		return out, ErrInvalidEvent
	}
	bound, err := security.WithDataScope(ctx, a.UserID, a.WorkspaceID, in.PartitionID)
	if err != nil {
		return out, err
	}
	return s.repo.OpenWindow(bound, in, key)
}

func (s *Service) Window(ctx context.Context, id string) (domain.ConversationWindow, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityEventsWrite); err != nil {
		return domain.ConversationWindow{}, err
	}
	return s.repo.Window(ctx, id)
}
