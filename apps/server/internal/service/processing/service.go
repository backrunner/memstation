// Package processing implements explicit lightweight and generative routing.
// Encoder labels are observations; they never confer actor identity or fact truth.
package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"memstation/apps/server/internal/aicall"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

type Config struct {
	EntityRecallTimeoutMS int     `json:"entity_recall_timeout_ms"`
	Strategy              string  `json:"strategy"`
	Endpoint              string  `json:"-"`
	Token                 string  `json:"-"`
	Model                 string  `json:"model"`
	Revision              string  `json:"revision"`
	Splitter              string  `json:"splitter"`
	Threshold             float64 `json:"threshold"`
	AuditRate             float64 `json:"audit_rate"`
	MaxSemanticBytes      int     `json:"max_semantic_bytes"`
}

func (c Config) Validate() error {
	if c.Strategy != "generative" && c.Strategy != "lightweight" && c.Strategy != "hybrid" {
		return errors.New("invalid memory processing strategy")
	}
	if math.IsNaN(c.Threshold) || math.IsInf(c.Threshold, 0) || math.IsNaN(c.AuditRate) || math.IsInf(c.AuditRate, 0) {
		return errors.New("non-finite processing configuration")
	}
	if c.EntityRecallTimeoutMS < 1 || c.EntityRecallTimeoutMS > 5000 {
		return errors.New("invalid entity recall timeout")
	}
	if c.Endpoint == "" || c.Token == "" || c.Model == "" || c.Revision == "" {
		return errors.New("enabled memory extraction requires endpoint, token, model and pinned revision; configure MEMSTATION_MEMORY_PROCESSING_ENDPOINT, TOKEN, MODEL and REVISION")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || c.Token == "" || strings.ContainsAny(c.Token, "\r\n") || c.Model == "" || c.Revision == "" || (c.Splitter != "char" && c.Splitter != "whitespace") || math.IsNaN(c.Threshold) || math.IsInf(c.Threshold, 0) || c.Threshold <= 0 || c.Threshold > 1 || math.IsNaN(c.AuditRate) || math.IsInf(c.AuditRate, 0) || c.AuditRate < 0 || c.AuditRate > 1 || c.MaxSemanticBytes < 1 {
		return errors.New("invalid pinned lightweight processing configuration")
	}
	return nil
}

func (c Config) Identity() string {
	// Read-side latency tuning must not invalidate queued source-processing jobs.
	c.EntityRecallTimeoutMS = 0
	b, _ := json.Marshal(c)
	d := sha256.Sum256(b)
	return "processing-v3:" + hex.EncodeToString(d[:])
}

type Span struct {
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Text       string  `json:"text"`
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
}
type Observation struct {
	Spans         []Span  `json:"spans"`
	NeedsSemantic float64 `json:"needs_semantic"`
	Supported     bool    `json:"supported"`
}
type Response struct {
	Version      int           `json:"version"`
	Model        string        `json:"model"`
	Revision     string        `json:"revision"`
	Splitter     string        `json:"splitter"`
	Observations []Observation `json:"observations"`
}
type Service struct {
	queryMu    sync.Mutex
	queryCache map[string]queryCacheEntry
	config     Config
	client     *http.Client
	ledger     ports.AICallRepository
}

func New(config Config, ledger ports.AICallRepository) (*Service, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Service{config: config, ledger: ledger, client: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *Service) Config() Config {
	if s == nil {
		return Config{Strategy: "generative"}
	}
	return s.config
}

// extractBatchWindows bounds how long one request holds the extractor's single
// inference slot, so queries wait for a short batch rather than a whole conversation.
const extractBatchWindows = 16

// Extract sends source windows in bounded batches and returns one observation per
// window in input order. A failed batch fails the call; retries resume from the start.
func (s *Service) Extract(ctx context.Context, user string, texts []string) ([]Observation, error) {
	if s == nil {
		return nil, errors.New("lightweight extraction not configured")
	}
	out := make([]Observation, 0, len(texts))
	for start := 0; start < len(texts); start += extractBatchWindows {
		batch, err := s.extractBatch(ctx, user, texts[start:min(start+extractBatchWindows, len(texts))])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func (s *Service) extractBatch(ctx context.Context, user string, texts []string) (out []Observation, resultErr error) {
	for _, t := range texts {
		if !utf8.ValidString(t) || len([]rune(t)) > 512 {
			return nil, errors.New("invalid lightweight source window")
		}
	}
	stage := "lightweight.extract"
	if aicall.MetadataFromContext(ctx).Stage == "entity.query.extract" {
		stage = "entity.query.extract"
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(s.config.EntityRecallTimeoutMS)*time.Millisecond)
		defer cancel()
	}
	if aicall.MetadataFromContext(ctx).Stage == "preview" {
		stage = "preview." + stage
	}
	ctx = aicall.WithMetadata(ctx, stage, s.config.Strategy)
	rec, err := aicall.Start(ctx, s.ledger, user, "extraction", s.config.Model, "local")
	if err != nil {
		return nil, err
	}
	defer func() {
		rec.Call.ModelVersion = s.config.Revision
		resultErr = errors.Join(resultErr, rec.Finish(ctx, resultErr))
	}()
	raw, err := json.Marshal(map[string]any{"version": 1, "model": s.config.Model, "revision": s.config.Revision, "splitter": s.config.Splitter, "texts": texts})
	if err != nil {
		return nil, err
	}
	path := "/extract"
	if stage == "entity.query.extract" {
		path = "/entities"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.Endpoint, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.config.Token)
	req.Header.Set("Content-Type", "application/json")
	if path == "/entities" {
		deadline, _ := ctx.Deadline()
		remaining := time.Until(deadline).Milliseconds()
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		req.Header.Set("X-Memstation-Timeout-Ms", strconv.FormatInt(remaining, 10))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, errors.New("lightweight extractor unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &ExtractorHTTPError{StatusCode: resp.StatusCode}
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("lightweight response exceeds budget")
	}
	var result Response
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil {
		return nil, errors.New("invalid lightweight response")
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing lightweight response")
	}
	if result.Version != 1 || result.Model != s.config.Model || result.Revision != s.config.Revision || result.Splitter != s.config.Splitter || len(result.Observations) != len(texts) {
		return nil, errors.New("lightweight identity mismatch")
	}
	for i, o := range result.Observations {
		if !finite(o.NeedsSemantic) || len(o.Spans) > 64 {
			return nil, errors.New("invalid lightweight observation")
		}
		runes := []rune(texts[i])
		for _, p := range o.Spans {
			if p.Start < 0 || p.End <= p.Start || p.End > len(runes) || p.Text != string(runes[p.Start:p.End]) || !finite(p.Confidence) || p.Label == "" || len(p.Label) > 80 {
				return nil, errors.New("ungrounded lightweight span")
			}
		}
	}
	return result.Observations, nil
}

// ExtractorHTTPError preserves an operational status without exposing the
// upstream response, source text, URL or credentials in logs and task errors.
type ExtractorHTTPError struct {
	StatusCode int
}

func (e *ExtractorHTTPError) Error() string {
	return fmt.Sprintf("lightweight extractor rejected request (HTTP %d)", e.StatusCode)
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) && x >= 0 && x <= 1 }

// Promotion uses a configurable model signal plus a reproducible scope-bound audit.
// An empty entity list is never taken as proof that no useful memory exists.
func (s *Service) Promote(scope string, observations []Observation) (bool, string) {
	if s == nil || s.config.Strategy == "generative" {
		return true, "generative"
	}
	if s.config.Strategy == "lightweight" {
		return false, "not_requested"
	}
	for _, o := range observations {
		if !o.Supported || o.NeedsSemantic >= s.config.Threshold {
			return true, "model_signal"
		}
	}
	digest := sha256.Sum256([]byte(s.config.Identity() + ":" + scope))
	sample := (float64(uint32(digest[0])<<24|uint32(digest[1])<<16|uint32(digest[2])<<8|uint32(digest[3])) + .5) / 4294967296
	if sample < s.config.AuditRate {
		return true, "audit_sample"
	}
	return false, "deferred"
}

func (c Config) Public() domain.ProcessingPolicy {
	return domain.ProcessingPolicy{EntityRecallTimeoutMS: c.EntityRecallTimeoutMS, Strategy: c.Strategy, Model: c.Model, Revision: c.Revision, Splitter: c.Splitter, Threshold: c.Threshold, AuditRate: c.AuditRate, MaxSemanticBytes: c.MaxSemanticBytes, Identity: c.Identity()}
}
