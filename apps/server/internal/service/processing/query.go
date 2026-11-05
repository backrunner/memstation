package processing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"memstation/apps/server/internal/aicall"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

type queryCacheEntry struct {
	names   []string
	expires time.Time
}

// Only query observations are cached. Authorized memory matches are always read
// again, so source edits, erasure, publication and audience changes stay live.
func (s *Service) ExtractQueryEntities(ctx context.Context, user, query string) (domain.QueryEntities, error) {
	if s == nil {
		return domain.QueryEntities{}, errors.New("entity extractor is not configured")
	}
	if err := ctx.Err(); err != nil {
		return domain.QueryEntities{}, err
	}
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil {
		return domain.QueryEntities{}, err
	}
	query = strings.TrimSpace(query)
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > 512 {
		return domain.QueryEntities{}, domain.ErrEntityQueryBudget
	}
	if query == "" {
		return domain.QueryEntities{Names: []string{}}, nil
	}
	parts := slices.Clone(security.Partitions(ctx))
	slices.Sort(parts)
	raw, _ := json.Marshal([]any{"query-entities-v1", s.config.Identity(), user, workspace, parts, query})
	sum := sha256.Sum256(raw)
	key := hex.EncodeToString(sum[:])
	now := time.Now()
	s.queryMu.Lock()
	hit, ok := s.queryCache[key]
	s.queryMu.Unlock()
	if ok && now.Before(hit.expires) {
		return domain.QueryEntities{Names: slices.Clone(hit.names), CacheHit: true}, nil
	}
	ctx = aicall.WithMetadata(ctx, "entity.query.extract", s.config.Strategy)
	observations, err := s.Extract(ctx, user, []string{query})
	if err != nil {
		return domain.QueryEntities{}, err
	}
	names := []string{}
	seen := map[string]bool{}
	for _, observation := range observations {
		for _, span := range observation.Spans {
			switch span.Label {
			case "person", "organization", "location", "project", "product", "technical_identifier":
			default:
				continue
			}
			name := domain.EntityNameKey(span.Text)
			if name == "" || len(name) > 256 || span.Confidence < .5 || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
			if len(names) == 8 {
				break
			}
		}
	}
	// Keep the process cache bounded even when every question is different.
	s.queryMu.Lock()
	if s.queryCache == nil {
		s.queryCache = map[string]queryCacheEntry{}
	}
	for k, v := range s.queryCache {
		if !now.Before(v.expires) {
			delete(s.queryCache, k)
		}
	}
	if len(s.queryCache) >= 256 {
		oldest := ""
		expiry := time.Time{}
		for k, v := range s.queryCache {
			if oldest == "" || v.expires.Before(expiry) {
				oldest = k
				expiry = v.expires
			}
		}
		delete(s.queryCache, oldest)
	}
	s.queryCache[key] = queryCacheEntry{names: slices.Clone(names), expires: now.Add(2 * time.Minute)}
	s.queryMu.Unlock()
	return domain.QueryEntities{Names: names}, nil
}
