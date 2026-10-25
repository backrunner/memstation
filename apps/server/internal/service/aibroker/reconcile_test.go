package aibroker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func TestReconcileUnknownUsesLedgerWithoutInvokingModel(t *testing.T) {
	call := domain.AICall{ID: "original-call", UserID: "owner", WorkspaceID: "group", Role: "analysis", Status: "unknown", UsageAuthority: "broker"}
	ledger := &callLedger{calls: map[string]domain.AICall{call.ID: call}}
	requests := 0
	status, workspace := "processing", "group"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/v1/memory/calls/original-call" || r.Header.Get("Authorization") != "Bearer restricted-service-test" {
			t.Error("reconciliation invoked model or lost identity")
		}
		w.Header().Set("X-MemStation-Broker-Version", "1")
		_ = json.NewEncoder(w).Encode(map[string]any{"call_id": call.ID, "workspace_id": workspace, "role": call.Role, "status": status, "usage_id": "usage-original", "request_id": "provider-original", "reported_cost": "0.02", "currency": "USD", "input_tokens": 10, "output_tokens": 4})
	}))
	defer server.Close()
	broker, err := New(brokerConfig(server.URL), ledger)
	if err != nil {
		t.Fatal(err)
	}
	service := NewUsageService(ledger, broker)
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: "owner", WorkspaceID: "group", ScopeVersion: 2, PartitionIDs: []string{""}, Capabilities: []string{domain.CapabilityUsageRead}})
	out, err := service.Reconcile(ctx, call.ID)
	if err != nil || out.Status != "unknown" {
		t.Fatalf("premature terminal status %+v %v", out, err)
	}
	workspace = "another-group"
	status = "complete"
	if _, err = service.Reconcile(ctx, call.ID); err == nil {
		t.Fatal("foreign ledger accepted")
	}
	workspace = "group"
	out, err = service.Reconcile(ctx, call.ID)
	if err != nil || out.Status != "complete" || out.BrokerUsageID != "usage-original" || out.InputTokens != 10 {
		t.Fatalf("reconciliation lost %+v %v", out, err)
	}
	before := requests
	if _, err = service.Reconcile(ctx, call.ID); err != nil || requests != before {
		t.Fatal("completed call reissued")
	}
	if _, err = service.Reconcile(security.WithAccess(context.Background(), domain.AccessContext{UserID: "owner", WorkspaceID: "group", ScopeVersion: 2}), call.ID); err == nil {
		t.Fatal("usage read without capability")
	}
	calls, _ := ledger.AICalls(ctx, "", 100)
	if len(calls) != 1 {
		t.Fatal("reconciliation charged another call")
	}
}
