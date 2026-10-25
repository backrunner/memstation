package memory

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

var ErrInvalidBrowse = errors.New("invalid memory filters or cursor")

type memoryCursor struct {
	Version    int                   `json:"v"`
	Scope      string                `json:"scope"`
	Generation string                `json:"generation"`
	Position   domain.MemoryPosition `json:"position"`
}

func (s *Service) BrowseDocuments(ctx context.Context, input domain.MemoryBrowseQuery) (domain.MemoryDocumentPage, error) {
	if _, err := security.DataScope(ctx, input.UserID, ""); err != nil {
		return domain.MemoryDocumentPage{}, err
	}
	input.Query = strings.TrimSpace(input.Query)
	if len(input.Query) > 1024 || input.Limit < 1 || input.Limit > 100 || len(input.After) > 4096 {
		return domain.MemoryDocumentPage{}, ErrInvalidBrowse
	}
	if len(input.Kinds) == 0 {
		input.Kinds = domain.DefaultSearchMemoryTypes(true)
	}
	input.Kinds = slices.Clone(input.Kinds)
	slices.Sort(input.Kinds)
	input.Kinds = slices.Compact(input.Kinds)
	for _, kind := range input.Kinds {
		if !domain.IsSupportedMemoryType(kind) {
			return domain.MemoryDocumentPage{}, ErrInvalidBrowse
		}
	}
	var cursor memoryCursor
	var position *domain.MemoryPosition
	if input.After != "" {
		data, err := base64.RawURLEncoding.DecodeString(input.After)
		if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.Generation == "" || cursor.Position.UpdatedAt.IsZero() || cursor.Position.ID == "" || !slices.Contains(input.Kinds, cursor.Position.Kind) {
			return domain.MemoryDocumentPage{}, ErrInvalidBrowse
		}
		if input.GenerationID != "" && input.GenerationID != cursor.Generation {
			return domain.MemoryDocumentPage{}, ErrInvalidBrowse
		}
		input.GenerationID = cursor.Generation
		position = &cursor.Position
	}
	var err error
	input.GenerationID, err = s.resolveGenerationID(ctx, input.UserID, input.GenerationID)
	if err != nil {
		return domain.MemoryDocumentPage{}, err
	}
	// Bind cursor to owner, workspace, generation, normalized filter and kinds.
	// It is a position hint, never authorization; every store query still scopes rows.
	encoded, _ := json.Marshal([]any{input.UserID, security.WorkspaceID(ctx, input.UserID), input.GenerationID, input.Query, input.Kinds, security.Partitions(ctx)})
	hash := sha256.Sum256(encoded)
	scope := hex.EncodeToString(hash[:])
	if position != nil && cursor.Scope != scope {
		return domain.MemoryDocumentPage{}, ErrInvalidBrowse
	}
	repo, ok := s.repo.(ports.MemoryBrowseRepository)
	if !ok {
		return domain.MemoryDocumentPage{}, errors.New("memory browsing unavailable")
	}
	limit := input.Limit
	input.Limit++ // one extra row proves the existence of a following page
	items, err := repo.BrowseDocuments(ctx, input, position)
	if err != nil {
		return domain.MemoryDocumentPage{}, err
	}
	page := domain.MemoryDocumentPage{Items: items, GenerationID: input.GenerationID}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		data, _ := json.Marshal(memoryCursor{Version: 1, Scope: scope, Generation: input.GenerationID, Position: domain.MemoryPosition{UpdatedAt: last.UpdatedAt, Kind: last.Type, ID: last.ID}})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	if page.Items == nil {
		page.Items = []domain.MemoryDocument{}
	}
	page.Items = security.MemoryContents(ctx, page.Items)
	return page, nil
}
