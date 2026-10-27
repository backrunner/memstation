package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
)

type evidenceReader struct {
	calls   int
	input   domain.SourceWindowInput
	err     error
	foreign bool
}

func TestEvidenceKeepsDeclaredSessionClockSeparateFromProcessingTime(t *testing.T) {
	doc := domain.MemoryDocument{ID: "episode", Type: domain.MemoryTypeEpisode, Content: "An event occurred last week.", CreatedAt: time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), Metadata: map[string]any{"source_session_time": "9:00 am on 23 October, 2026"}}
	text := FormatEvidenceDocument(doc)
	if !strings.Contains(text, `"source_session_time":"9:00 am on 23 October, 2026"`) || !strings.Contains(text, `"recorded_at":"2026-10-26T00:00:00Z"`) || strings.Contains(text, `"valid_from"`) {
		t.Fatal("source clock lost or promoted into inferred event validity", text)
	}
}

func (r *evidenceReader) ReadSourceWindow(_ context.Context, in domain.SourceWindowInput) ([]domain.SourceTurn, error) {
	r.calls++
	r.input = in
	owner := in.UserID
	if r.foreign {
		owner = "other"
	}
	return []domain.SourceTurn{{UserID: owner, WorkspaceID: in.WorkspaceID, ConversationID: in.ConversationID, Sequence: 1, Speaker: "Bob", Text: "I did not attend the event."}}, r.err
}

func TestEvidencePreservesAttributionNegationAndBudget(t *testing.T) {
	docs := []domain.MemoryDocument{
		{ID: "a", Type: domain.MemoryTypeFact, Content: "我没有参加活动。", SubjectActorIDs: []string{"Alice"}, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Metadata: map[string]any{"valid_from": "2025-01-01", "secret_token": "do-not-render"}},
		{ID: "b", Type: domain.MemoryTypeFact, Content: "我没有参加活动。", SubjectActorIDs: []string{"Bob"}},
	}
	got := packEvidence(docs, nil, EvidenceOptions{MaxBytes: 16000})
	text := buildEvidenceSystemPrompt(got)
	if len(got) != 2 || strings.Contains(text, "do-not-render") || !strings.Contains(text, "recorded_at") || !strings.Contains(text, "valid_from") || strings.Count(text, "我没有参加活动。") != 2 {
		t.Fatal(text)
	}
	for _, budget := range []int{20, 200, 400, 800} {
		packed := packEvidence(docs, nil, EvidenceOptions{MaxBytes: budget, MaxEstimatedTokens: 100})
		if len(packed) == 0 {
			continue
		}
		text = buildEvidenceSystemPrompt(packed)
		if len(text) > budget || estimateEvidenceTokens(text) > 100 || !utf8.ValidString(text) {
			t.Fatalf("budget=%d text=%s", budget, text)
		}
		for _, d := range packed {
			if d.Content != "我没有参加活动。" {
				t.Fatal("truncated negation", d)
			}
		}
	}
}

func TestEvidenceHydratesCanonicalAnchorsAndRejectsForeignSources(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	original := domain.MemoryDocument{ID: "a", UserID: "owner", ConversationID: "conversation", Type: domain.MemoryTypeFact, Content: "Alice planned an event.", Metadata: map[string]any{"source_conversation_revision": int64(1), "source_quotes": []map[string]any{{"sequence_num": 2, "sequence_base": 1}}}}
	if err := repo.StoreDocument(ctx, original); err != nil {
		t.Fatal(err)
	}
	doc, err := repo.GetDocument(ctx, "owner", "a", domain.MemoryTypeFact)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(repo, nil)
	reader := &evidenceReader{}
	engine.SetSourceContextReader(reader)
	input := domain.SearchInput{UserID: "owner"}
	options := EvidenceOptions{MaxBytes: 16000, NeighborTurns: 1}
	got, err := engine.PrepareEvidence(ctx, input, []domain.MemoryDocument{doc}, options)
	if err != nil || got.Diagnostics.PackedSourceTurns != 1 || reader.calls != 1 || len(reader.input.Sequences) != 1 || reader.input.Sequences[0] != 1 || reader.input.ConversationRevision != 1 || !strings.Contains(got.Text, "did not attend") {
		t.Fatalf("packet=%+v err=%v reader=%+v", got, err, reader)
	}
	now := time.Now()
	historical := input
	historical.AsOf = &now
	if got, err = engine.PrepareEvidence(ctx, historical, []domain.MemoryDocument{doc}, options); err != nil || !got.Diagnostics.SourcesDisabled || reader.calls != 1 {
		t.Fatal("historical source read", err)
	}
	stale := doc
	stale.Revision++
	if got, err = engine.PrepareEvidence(ctx, input, []domain.MemoryDocument{stale}, options); err != nil || len(got.Memories) != 0 || reader.calls != 1 {
		t.Fatal("stale anchor accepted", err)
	}
	reader.foreign = true
	if _, err = engine.PrepareEvidence(ctx, input, []domain.MemoryDocument{doc}, options); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("foreign source accepted", err)
	}
	reader.foreign = false
	reader.err = errors.New("source offline")
	if got, err = engine.PrepareEvidence(ctx, input, []domain.MemoryDocument{doc}, options); err != nil || len(got.Memories) != 1 || got.Diagnostics.Unavailable != 1 {
		t.Fatal("source degradation", err)
	}
	reader.err = domain.ErrAccessDenied
	if _, err = engine.PrepareEvidence(ctx, input, []domain.MemoryDocument{doc}, options); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("access error hidden", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = engine.PrepareEvidence(canceled, input, []domain.MemoryDocument{doc}, options); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel hidden", err)
	}
}

func TestEvidenceSourceCoordinatesPreferRevisionRefs(t *testing.T) {
	d := domain.MemoryDocument{Type: domain.MemoryTypeSegment, Metadata: map[string]any{"start_sequence": 0, "end_sequence": 3}}
	seqs := sourceAnchorSequences(d)
	if len(seqs) != 2 || seqs[0] != 0 || seqs[1] != 3 {
		t.Fatal(seqs)
	}
	d.SourceRefs = []domain.SourceRef{{MessageID: "source", Revision: 1}}
	if len(sourceAnchorSequences(d)) != 0 {
		t.Fatal("revision refs fell back to sequence")
	}
}

func TestInjectionUsesEvidencePacketAndWholePromptBudget(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: "event", UserID: "owner", ConversationID: "conversation", Type: domain.MemoryTypeFact, Content: "Alice planned an event.", Metadata: map[string]any{"source_conversation_revision": int64(1), "source_quotes": []map[string]any{{"sequence_num": 2, "sequence_base": 1}}}}); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(repo, nil)
	reader := &evidenceReader{}
	engine.SetSourceContextReader(reader)
	if err := engine.SetEvidenceConfig(EvidenceConfig{Enabled: true, NeighborTurns: 1}); err != nil {
		t.Fatal(err)
	}
	req := domain.UnifiedRequest{UserID: "owner", Mode: domain.ModeInject, InjectContext: true, MaxInjectionTokens: 1024, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Alice event"}}, AgentID: "fixture-agent"}
	injected, result, err := engine.Inject(ctx, req)
	if err != nil || len(injected.Messages) != 2 || reader.calls != 1 || len(result.Search.Memories) != 2 {
		t.Fatalf("injection: %+v %+v %v", injected, result, err)
	}
	prompt := injected.Messages[0].Text
	if !strings.Contains(prompt, "did not attend") || !strings.Contains(prompt, "source_context") || estimateEvidenceTokens(prompt) > req.MaxInjectionTokens {
		t.Fatal(prompt)
	}
	req.MaxInjectionTokens = 8
	injected, _, err = engine.Inject(ctx, req)
	if err != nil || len(injected.Messages) != 1 {
		t.Fatal("tiny budget injected partial evidence", err)
	}
}

func TestEvidenceDeduplicatesOnlyCompleteSourceTurns(t *testing.T) {
	raw := domain.MemoryDocument{SourceRefs: []domain.SourceRef{}, ID: "raw", ConversationID: "session", Type: domain.MemoryTypeFact, Content: "Alice: I did not attend.", Metadata: map[string]any{"source_turn": true, "start_sequence": 1, "end_sequence": 1}}
	source := domain.MemoryDocument{ID: "source", ConversationID: "session", Type: domain.MemoryTypeSegment, Content: "[Speaker: Alice] I did not attend.", Metadata: map[string]any{"source_context": true, "start_sequence": 1, "end_sequence": 1}}
	derived := domain.MemoryDocument{ID: "derived", ConversationID: "session", Type: domain.MemoryTypeFact, Content: "Alice did not attend.", Metadata: map[string]any{"start_sequence": 1, "end_sequence": 1}}
	got := packEvidence([]domain.MemoryDocument{raw, derived}, []domain.MemoryDocument{source}, EvidenceOptions{MaxBytes: 16000})
	if len(got) != 2 || got[0].ID != "raw" || got[1].ID != "derived" {
		t.Fatalf("dedup erased complementary fact or kept duplicate turn: %+v", got)
	}
	// An oversized original does not mark a turn as represented before admission.
	raw.Content = strings.Repeat("long ", 5000)
	got = packEvidence([]domain.MemoryDocument{raw}, []domain.MemoryDocument{source}, EvidenceOptions{MaxBytes: 1000})
	if len(got) != 1 || got[0].ID != "source" {
		t.Fatalf("unpacked original suppressed source: %+v", got)
	}
}

func TestEvidenceExperimentDoesNotChangeDefaultInjection(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: "fact", UserID: "owner", ConversationID: "conversation", Type: domain.MemoryTypeFact, Content: "Alice planned an event.", Metadata: map[string]any{"source_quotes": []map[string]any{{"sequence_num": 2, "sequence_base": 1}}}}); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(repo, nil)
	reader := &evidenceReader{}
	engine.SetSourceContextReader(reader)
	req := domain.UnifiedRequest{UserID: "owner", Mode: domain.ModeInject, InjectContext: true, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Alice event"}}, AgentID: "fixture-agent"}
	got, _, err := engine.Inject(ctx, req)
	if err != nil || len(got.Messages) != 2 || reader.calls != 0 || strings.Contains(got.Messages[0].Text, "recorded_at") || !strings.Contains(got.Messages[0].Text, "Alice planned an event.") {
		t.Fatalf("default injection changed: %+v %v", got, err)
	}
	if err = engine.SetEvidenceConfig(EvidenceConfig{Enabled: true, NeighborTurns: 3}); err == nil || engine.evidenceConfig().Enabled {
		t.Fatal("invalid experiment config accepted")
	}
}

func TestEvidencePromptExcludesStorageCoordinatesAndRawAttachments(t *testing.T) {
	doc := domain.MemoryDocument{ID: "PRIVATE-DOCUMENT", ConversationID: "PRIVATE-CONVERSATION", Type: domain.MemoryTypeFact, Content: "Alice declined.", SourceRefs: []domain.SourceRef{{WorkspaceID: "PRIVATE-WORKSPACE", MessageID: "PRIVATE-MESSAGE", Revision: 1}}, Metadata: map[string]any{"ref": "PRIVATE-REFERENCE", "source_parts": []domain.SourcePart{{Type: "media", Media: &domain.ContentBlock{Type: domain.ContentBlockImage, SourceURL: "https://example.invalid/PRIVATE-URL"}}}, "speaker": "Alice"}}
	text := buildEvidenceSystemPrompt([]domain.MemoryDocument{doc})
	if strings.Contains(text, "PRIVATE-") || !strings.Contains(text, "Alice declined.") || !strings.Contains(text, "memory-1") {
		t.Fatal(text)
	}
	if doc.Metadata["ref"] != "PRIVATE-REFERENCE" || len(doc.SourceRefs) != 1 {
		t.Fatal("prompt projection mutated stored provenance")
	}
}
