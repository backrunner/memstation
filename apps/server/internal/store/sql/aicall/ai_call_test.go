package aicall

import (
	"context"
	"fmt"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	ingestfixturestore "memstation/apps/server/internal/store/sql/internal/ingestfixture"
)

func TestUsageReportSeparatesCurrenciesUnknownUsageAndPrivateAudience(t *testing.T) {
	db, _, ctx, b := ingestfixturestore.IngestionFixture(t)
	repo := NewAICallRepository(runtimeconfig.NewStaticDBHandle(db))
	user := security.UserIDFromContext(ctx)
	for i, item := range []struct {
		price, currency string
		known           bool
	}{{"0.1", "USD", true}, {"0.2", "USD", true}, {"0.05", "EUR", true}, {"", "", false}} {
		call := domain.AICall{ID: fmt.Sprintf("usage-%d", i), UserID: user, WorkspaceID: b.WorkspaceID, PartitionIDs: []string{""}, Role: "analysis", Stage: "analysis.extract", Strategy: "hybrid", ModelAlias: "pinned", UsageAuthority: "memstation"}
		if err := repo.StartAICall(ctx, call); err != nil {
			t.Fatal(err)
		}
		call.Status = "complete"
		call.ReportedCost = item.price
		call.Currency = item.currency
		call.InputTokens = 100
		call.UsageKnown = item.known
		if err := repo.FinishAICall(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	access, _ := security.AccessFromContext(ctx)
	access.PartitionIDs = []string{"", "private"}
	privateCtx := security.WithAccess(context.Background(), access)
	private := domain.AICall{ID: "usage-private", UserID: user, WorkspaceID: b.WorkspaceID, PartitionIDs: access.PartitionIDs, Role: "analysis", Stage: "analysis.extract", Strategy: "hybrid", ModelAlias: "pinned", UsageAuthority: "broker"}
	if err := repo.StartAICall(privateCtx, private); err != nil {
		t.Fatal(err)
	}
	q := domain.AIUsageQuery{Since: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour), Strategy: "hybrid"}
	report, err := repo.AIUsageReport(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Groups) != 1 || report.Groups[0].Calls != 4 || report.Groups[0].KnownUsageCalls != 3 || report.UnpricedCalls != 1 {
		t.Fatalf("private leakage or missing counters: %+v", report)
	}
	if len(report.Costs) != 2 || report.Costs[0].Currency != "EUR" || report.Costs[1].Currency != "USD" || report.Costs[1].Amount != "0.3" {
		t.Fatalf("currencies merged or imprecise decimal sum: %+v", report.Costs)
	}
	q.Stage = "summary.generate"
	report, err = repo.AIUsageReport(ctx, q)
	if err != nil || len(report.Groups) != 0 {
		t.Fatalf("stage filter ignored: %+v %v", report, err)
	}
	q.Stage = "analysis.extract"
	items, err := repo.FilteredAICalls(ctx, q, "", 100)
	if err != nil || len(items) != 4 {
		t.Fatalf("filtered calls mismatch: %+v %v", items, err)
	}
}

func TestAICallLedgerPreservesPrivateAudienceAndBillingReferences(t *testing.T) {
	db, _, ctx, b := ingestfixturestore.IngestionFixture(t)
	repo := NewAICallRepository(runtimeconfig.NewStaticDBHandle(db))
	user := security.UserIDFromContext(ctx)
	public := domain.AICall{ID: "public-call", UserID: user, WorkspaceID: b.WorkspaceID, Role: "summary", ModelAlias: "memory.summary", PartitionIDs: []string{""}}
	if err := repo.StartAICall(ctx, public); err != nil {
		t.Fatal(err)
	}
	public.Status = "complete"
	public.BrokerUsageID = "public-usage"
	public.ReportedCost = "0.012"
	public.Currency = "USD"
	public.InputTokens = 123
	if err := repo.FinishAICall(ctx, public); err != nil {
		t.Fatal(err)
	}
	access, _ := security.AccessFromContext(ctx)
	access.PartitionIDs = []string{"", "private-audience"}
	privateCtx := security.WithAccess(context.Background(), access)
	private := domain.AICall{ID: "private-call", UserID: user, WorkspaceID: b.WorkspaceID, Role: "rerank", ModelAlias: "memory.rerank", PartitionIDs: access.PartitionIDs}
	if err := repo.StartAICall(privateCtx, private); err != nil {
		t.Fatal(err)
	}
	private.Status = "unknown"
	private.FailureCode = "network_result_unknown"
	if err := repo.FinishAICall(privateCtx, private); err != nil {
		t.Fatal(err)
	}
	visible, err := repo.AICalls(ctx, "", 100)
	if err != nil || len(visible) != 1 || visible[0].ID != public.ID {
		t.Fatalf("private usage leaked %+v %v", visible, err)
	}
	all, err := repo.AICalls(privateCtx, "", 100)
	if err != nil || len(all) != 2 {
		t.Fatalf("authorized usage %+v %v", all, err)
	}
	var billed int
	if err = db.QueryRow(`SELECT count(*) FROM token_usages WHERE user_id=$1`, user).Scan(&billed); err != nil {
		t.Fatal(err)
	}
	if billed != 0 {
		t.Fatal("broker usage duplicated into MemStation billing")
	}
}

func TestEmbeddingProfilesRequireNewGenerationForUnpinnedIndex(t *testing.T) {
	db, _, ctx, b := ingestfixturestore.IngestionFixture(t)
	repo := NewAICallRepository(runtimeconfig.NewStaticDBHandle(db))
	user := security.UserIDFromContext(ctx)
	first := domain.EmbeddingIdentity{ModelVersion: "revision-a", Dimensions: 3, Fingerprint: "fingerprint-a"}
	pinned, err := repo.EmbeddingIdentity(ctx, user, "", 1, first)
	if err != nil || pinned != first {
		t.Fatalf("bind %+v %v", pinned, err)
	}
	changed := domain.EmbeddingIdentity{ModelVersion: "revision-b", Dimensions: 3, Fingerprint: "fingerprint-b"}
	pinned, err = repo.EmbeddingIdentity(ctx, user, "", 1, changed)
	if err != nil || pinned != first {
		t.Fatalf("alias change rewrote existing generation %+v %v", pinned, err)
	}
	fresh, err := repo.EmbeddingIdentity(ctx, user, "", 2, changed)
	if err != nil || fresh != changed {
		t.Fatalf("new version %+v %v", fresh, err)
	}
	foreign := security.WithAccess(context.Background(), domain.AccessContext{UserID: user, WorkspaceID: b.WorkspaceID + "-foreign", ScopeVersion: 2, PartitionIDs: []string{""}})
	if _, err = repo.EmbeddingIdentity(foreign, user, "does-not-belong-here", 1, first); err == nil {
		t.Fatal("foreign generation accepted")
	}
}
