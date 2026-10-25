package aibroker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"memstation/apps/server/internal/domain"
)

// Reconciliation reads the broker's ledger by original call ID. It never invokes
// a model and cannot infer an upstream outcome from a transport timeout.
func (c *Client) reconcile(ctx context.Context, call domain.AICall) (domain.AICall, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.config.BaseURL, "/")+"/memory/calls/"+url.PathEscape(call.ID), nil)
	if err != nil {
		return call, err
	}
	req.Header.Set("Authorization", "Bearer "+c.config.Credential)
	req.Header.Set("X-MemStation-Broker-Version", "1")
	req.Header.Set("X-MemStation-Workspace", call.WorkspaceID)
	req.Header.Set("X-MemStation-Role", call.Role)
	req.Header.Set("X-MemStation-Internal-Call", "memory-broker-v1")
	response, err := c.http.Do(req)
	if err != nil {
		return call, errors.New("broker reconciliation unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusAccepted {
		return call, nil
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("X-MemStation-Broker-Version") != "1" {
		return call, errors.New("broker reconciliation contract mismatch")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return call, errors.New("broker reconciliation response too large")
	}
	var result struct {
		CallID       string `json:"call_id"`
		WorkspaceID  string `json:"workspace_id"`
		Role         string `json:"role"`
		Status       string `json:"status"`
		UsageID      string `json:"usage_id"`
		RequestID    string `json:"request_id"`
		ModelVersion string `json:"model_version"`
		Input        int    `json:"input_tokens"`
		Output       int    `json:"output_tokens"`
		Cost         string `json:"reported_cost"`
		Currency     string `json:"currency"`
		FailureCode  string `json:"failure_code"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || result.CallID != call.ID || result.WorkspaceID != call.WorkspaceID || result.Role != call.Role {
		return call, errors.New("broker reconciliation identity mismatch")
	}
	if result.Status == "unknown" || result.Status == "processing" {
		return call, nil
	}
	if result.Status != "complete" && result.Status != "failed" || result.Input < 0 || result.Output < 0 || result.Status == "complete" && (result.UsageID == "" || result.RequestID == "") {
		return call, errors.New("invalid broker reconciliation result")
	}
	if result.Cost != "" {
		cost, e := strconv.ParseFloat(result.Cost, 64)
		if e != nil || math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 || result.Currency == "" {
			return call, errors.New("invalid broker reconciliation usage")
		}
	}
	call.Status = result.Status
	call.BrokerUsageID = result.UsageID
	call.ProviderRequestID = result.RequestID
	call.ModelVersion = result.ModelVersion
	call.InputTokens = result.Input
	call.OutputTokens = result.Output
	call.ReportedCost = result.Cost
	call.Currency = result.Currency
	call.FailureCode = result.FailureCode
	if err = c.ledger.FinishAICall(ctx, call); err != nil {
		return call, err
	}
	return call, nil
}
