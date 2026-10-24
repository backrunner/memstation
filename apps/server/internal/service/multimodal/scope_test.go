package multimodal

import (
	"context"
	"errors"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func TestScopeRejectedBeforeStorageOrExtraction(t *testing.T) {
	// No dependencies: denial must precede reading a body, storage or extraction.
	s := &Service{}
	for _, access := range []domain.AccessContext{{UserID: "other", WorkspaceID: domain.PersonalWorkspaceID("other")}, {UserID: "owner", WorkspaceID: "group"}} {
		ctx := security.WithAccess(context.Background(), access)
		conversation := domain.ConversationRecord{UserID: "owner", ConversationID: "conv"}
		calls := map[string]func() error{
			"upload":      func() error { _, err := s.SaveUpload(ctx, "owner", "file", "text/plain", nil, nil); return err },
			"get":         func() error { _, err := s.GetBlob(ctx, "owner", "blob"); return err },
			"open":        func() error { _, _, err := s.OpenBlob(ctx, "owner", "blob"); return err },
			"delete":      func() error { return s.DeleteBlob(ctx, "owner", "blob") },
			"cleanup":     func() error { return s.CleanupConversationBlobs(ctx, "owner", "conv") },
			"materialize": func() error { _, err := s.MaterializeConversation(ctx, conversation); return err },
			"extract":     func() error { _, err := s.ExtractConversation(ctx, conversation, nil); return err },
			"parts":       func() error { _, err := s.ListMessageParts(ctx, "owner", "conv"); return err },
			"artifacts":   func() error { _, err := s.ListPartArtifacts(ctx, "owner", "conv"); return err },
		}
		for name, call := range calls {
			if err := call(); !errors.Is(err, domain.ErrAccessDenied) {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	if _, err := s.ExtractConversation(context.Background(), domain.ConversationRecord{UserID: "owner", ConversationID: "conv"}, []domain.MessagePart{{UserID: "other"}}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("foreign nested part accepted", err)
	}
}
