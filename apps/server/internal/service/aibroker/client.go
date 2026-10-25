// Package aibroker calls an external AI runtime directly. It has no dependency on
// the gateway, retrieval engine or conversation pipeline.
package aibroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"memstation/apps/server/internal/aicall"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	BaseURL    string
	Credential string
	Roles      map[string]string
	Embedding  domain.EmbeddingIdentity
	Timeout    time.Duration
}
type Client struct {
	config   Config
	http     *http.Client
	ledger   ports.AICallRepository
	duration metric.Float64Histogram
}

func New(config Config, ledger ports.AICallRepository) (*Client, error) {
	u, err := url.Parse(config.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" || strings.ContainsAny(config.Credential, "\r\n") || config.Credential == "" || ledger == nil {
		return nil, errors.New("invalid AI broker configuration")
	}
	if config.Embedding.Fingerprint == "" || config.Embedding.Dimensions < 1 || config.Embedding.ModelVersion == "" {
		return nil, errors.New("pinned broker embedding identity required")
	}
	aliases := map[string]bool{}
	for _, role := range []string{"summary", "analysis", "embedding", "rerank"} {
		alias := strings.TrimSpace(config.Roles[role])
		if alias == "" || len(alias) > 256 || aliases[alias] {
			return nil, errors.New("distinct configured broker role aliases required")
		}
		aliases[alias] = true
	}
	if config.Timeout <= 0 {
		config.Timeout = 90 * time.Second
	}
	duration, err := otel.Meter("memstation.aibroker").Float64Histogram("memstation.memory.broker.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	return &Client{config: config, ledger: ledger, duration: duration, http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Adapter(role string) (ports.ProviderAdapter, string, error) {
	role = strings.TrimSuffix(role, "_model")
	if role != "summary" && role != "analysis" {
		return nil, "", domain.ErrAccessDenied
	}
	return &textAdapter{client: c, role: role}, c.config.Roles[role], nil
}

type textAdapter struct {
	client *Client
	role   string
}

func (*textAdapter) Name() domain.Provider { return domain.ProviderOpenAICompatible }
func (*textAdapter) Stream(context.Context, domain.UnifiedRequest, func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	return nil, domain.ErrAccessDenied
}

func (a *textAdapter) Complete(ctx context.Context, in domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	if in.Model != a.client.config.Roles[a.role] || in.Stream || len(in.Tools) > 0 {
		return nil, domain.ErrAccessDenied
	}
	messages := []map[string]string{}
	for _, m := range in.Messages {
		if m.Role != domain.RoleSystem && m.Role != domain.RoleUser && m.Role != domain.RoleAssistant {
			return nil, domain.ErrAccessDenied
		}
		messages = append(messages, map[string]string{"role": string(m.Role), "content": m.Text})
	}
	body := map[string]any{"model": in.Model, "messages": messages, "stream": false, "max_tokens": in.MaxOutputTokens, "response_format": map[string]string{"type": "json_object"}}
	raw, _, err := a.client.post(ctx, in.UserID, a.role, "/chat/completions", body, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []any  `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
			Total      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || len(result.Choices) != 1 || len(result.Choices[0].Message.ToolCalls) > 0 {
		return nil, errors.New("invalid broker text response")
	}
	return &domain.UnifiedResponse{ResponseID: result.ID, Model: result.Model, Provider: a.Name(), Protocol: domain.ProtocolOpenAIChat, FinishReason: result.Choices[0].FinishReason, Message: domain.UnifiedMessage{Role: domain.RoleAssistant, Text: result.Choices[0].Message.Content}, Usage: domain.UsageRecord{InputTokens: result.Usage.Prompt, OutputTokens: result.Usage.Completion, TotalTokens: result.Usage.Total}}, nil
}

// Each network attempt has a distinct call ID. A timeout remains unknown; the
// caller may retry the job, but no upstream exactly-once claim is made.
func (c *Client) post(ctx context.Context, user, role, path string, payload any, extra map[string]string) (raw []byte, headers http.Header, resultErr error) {
	alias, ok := c.config.Roles[role]
	if !ok || alias == "" {
		return nil, nil, domain.ErrAccessDenied
	}
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	ctx, operation := otel.Tracer("memstation.aibroker").Start(ctx, "memory.broker.call", trace.WithAttributes(attribute.String("memory.role", role), attribute.String("memory.workspace_id", workspace)))
	started := time.Now()
	defer func() {
		status := "complete"
		if resultErr != nil {
			status = "failed"
			operation.SetStatus(codes.Error, "broker call failed")
		}
		c.duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(attribute.String("role", role), attribute.String("status", status)))
		operation.End()
	}()
	job := aicall.JobFromContext(ctx)
	span := trace.SpanContextFromContext(ctx)
	call := domain.AICall{ID: uuid.NewString(), PartitionIDs: security.Partitions(ctx), UserID: user, WorkspaceID: workspace, PartitionID: security.WritePartition(ctx), Role: role, ModelAlias: alias, JobID: job.ID, Attempt: job.Attempt, TraceID: span.TraceID().String(), UsageAuthority: "broker", Status: "unknown"}
	if err = c.ledger.StartAICall(ctx, call); err != nil {
		return nil, nil, err
	}
	defer func() {
		defer func() { aicall.Record(ctx, call) }()
		recordCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		resultErr = errors.Join(resultErr, c.ledger.FinishAICall(recordCtx, call))
	}()
	body, err := json.Marshal(payload)
	if err != nil {
		call.Status = "failed"
		call.FailureCode = "invalid_payload"
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		call.Status = "failed"
		call.FailureCode = "invalid_request"
		return nil, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.config.Credential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-MemStation-Internal-Call", "memory-broker-v1")
	request.Header.Set("X-MemStation-Broker-Version", "1")
	request.Header.Set("X-MemStation-Role", role)
	request.Header.Set("X-MemStation-Workspace", workspace)
	request.Header.Set("X-MemStation-Call-Id", call.ID)
	request.Header.Set("X-MemStation-Job", job.ID)
	request.Header.Set("X-MemStation-Attempt", strconv.Itoa(job.Attempt))
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(request.Header))
	for key, value := range extra {
		request.Header.Set(key, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		call.FailureCode = "network_result_unknown"
		return nil, nil, errors.New("broker network result unknown")
	}
	defer response.Body.Close()
	headers = response.Header
	raw, err = io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		call.FailureCode = "response_result_unknown"
		return nil, headers, errors.New("broker response unavailable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			call.Status = "failed"
		}
		call.FailureCode = fmt.Sprintf("broker_http_%d", response.StatusCode)
		return nil, headers, errors.New("AI broker rejected request")
	}
	if headers.Get("X-MemStation-Broker-Version") != "1" || headers.Get("X-MemStation-Call-Id") != call.ID || headers.Get("X-MemStation-Usage-Id") == "" || headers.Get("X-MemStation-Request-Id") == "" {
		call.FailureCode = "broker_contract_mismatch"
		return nil, headers, errors.New("AI broker contract mismatch")
	}
	call.ProviderRequestID = headers.Get("X-MemStation-Request-Id")
	call.BrokerUsageID = headers.Get("X-MemStation-Usage-Id")
	call.ModelVersion = headers.Get("X-MemStation-Model-Version")
	call.ReportedCost = headers.Get("X-MemStation-Reported-Cost")
	call.Currency = headers.Get("X-MemStation-Currency")
	if call.ReportedCost != "" {
		cost, e := strconv.ParseFloat(call.ReportedCost, 64)
		if e != nil || math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 || call.Currency == "" {
			call.FailureCode = "invalid_usage"
			return nil, headers, errors.New("invalid broker usage")
		}
	}
	var usage struct {
		Usage struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err = json.Unmarshal(raw, &usage); err != nil || usage.Usage.Input < 0 || usage.Usage.Output < 0 {
		call.FailureCode = "invalid_response"
		return nil, headers, errors.New("invalid broker response")
	}
	call.InputTokens = usage.Usage.Input
	call.OutputTokens = usage.Usage.Output
	call.Status = "complete"
	return raw, headers, nil
}
