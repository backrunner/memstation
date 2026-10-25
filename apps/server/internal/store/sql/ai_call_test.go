package sql

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
)

func TestAICallLedgerPreservesPrivateAudienceAndBillingReferences(t *testing.T) {
	db, _, _, ctx, b := lifecycleFixture(t)
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
	db, _, _, ctx, b := lifecycleFixture(t)
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
