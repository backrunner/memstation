package browse

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

var ErrInvalid = errors.New("invalid record filters or cursor")

type cursor struct {
	Version  int                   `json:"v"`
	Scope    string                `json:"scope"`
	Position domain.RecordPosition `json:"position"`
}

// Prepare binds a position to its owner, workspace, collection and filter.
// Cursors are not credentials: stores independently enforce scope on every query.
func Prepare(ctx context.Context, collection string, input *domain.RecordBrowseQuery) (*domain.RecordPosition, string, error) {
	workspace, err := security.DataScope(ctx, input.UserID, "")
	if err != nil {
		return nil, "", err
	}
	input.Query = strings.TrimSpace(input.Query)
	if len(input.Query) > 1024 || input.Limit < 1 || input.Limit > 100 || len(input.After) > 4096 {
		return nil, "", ErrInvalid
	}
	encoded, _ := json.Marshal([]string{input.UserID, workspace, collection, input.Query})
	hash := sha256.Sum256(encoded)
	scope := hex.EncodeToString(hash[:])
	if input.After == "" {
		return nil, scope, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(input.After)
	var item cursor
	if err != nil || json.Unmarshal(data, &item) != nil || item.Version != 1 || item.Scope != scope || item.Position.ID == "" || item.Position.UpdatedAt.IsZero() {
		return nil, "", ErrInvalid
	}
	return &item.Position, scope, nil
}

func Page[T any](items []T, limit int, scope string, position func(T) domain.RecordPosition) domain.RecordPage[T] {
	page := domain.RecordPage[T]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		encoded, _ := json.Marshal(cursor{Version: 1, Scope: scope, Position: position(items[limit-1])})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	if page.Items == nil {
		page.Items = []T{}
	}
	return page
}
