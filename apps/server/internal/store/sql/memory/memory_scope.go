package memory

import (
	"context"

	"memstation/apps/server/internal/domain"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"
)

func validateEvidenceIdentity(user, nodeID string, evidences []domain.MemoryEvidence) error {
	for _, e := range evidences {
		if e.UserID != "" && e.UserID != user || e.NodeID != "" && e.NodeID != nodeID {
			return domain.ErrAccessDenied
		}
	}
	return nil
}

// Foreign endpoints are rejected for evidence and graph operations.
func (r *InMemoryMemoryRepository) nodeOutsidePersonalLocked(ctx context.Context, user, nodeID string) bool {
	for _, doc := range r.documents {
		if doc.NodeID == nodeID && (doc.UserID != user || !scopestore.DocumentVisible(ctx, doc)) {
			return true
		}
	}
	return false
}
