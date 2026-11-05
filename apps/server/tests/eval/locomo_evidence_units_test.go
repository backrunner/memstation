package eval

// Read-only architectural prototype. This file is not a product strategy.
// Source construction has no question, label, gold answer or grade input.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	memoryservice "memstation/apps/server/internal/service/memory"
	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"
)

type sourceUnitIndex struct {
	*memorystore.InMemoryMemoryRepository
	lookup                      ports.MemoryRepository
	User, Workspace, Generation string
	Forward                     map[string]string                `json:"key_to_unit"`
	Units                       map[string]domain.MemoryDocument `json:"units"`
	Rejected                    map[string]int                   `json:"rejected"`
	KeyCount                    int                              `json:"key_count"`
	GraphEdges                  int                              `json:"graph_edges"`
}

func unitInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == float64(int(n))
	}
	return 0, false
}

func unitAnchorInput(d domain.MemoryDocument) (domain.SourceWindowInput, error) {
	in := domain.SourceWindowInput{UserID: d.UserID, WorkspaceID: d.WorkspaceID, ConversationID: d.ConversationID, Refs: d.SourceRefs, Radius: 1, Limit: 32, MaxBytes: 65536}
	rev, _ := unitInt(d.Metadata["source_conversation_revision"])
	in.ConversationRevision = int64(rev)
	if len(in.Refs) > 0 {
		if len(in.Refs) > 32 {
			return in, fmt.Errorf("too_many_refs")
		}
		return in, nil
	}
	if d.Type == domain.MemoryTypeSegment || d.Metadata["source_turn"] == true {
		start, ok := unitInt(d.Metadata["start_sequence"])
		end, hasEnd := unitInt(d.Metadata["end_sequence"])
		if ok && start >= 0 {
			if !hasEnd {
				end = start
			}
			if end < start || end-start > 31 {
				return in, fmt.Errorf("too_many_anchors")
			}
			for n := start; n <= end; n++ {
				in.Sequences = append(in.Sequences, n)
			}
		}
	}
	in.Sequences = append(in.Sequences, domain.SourceQuoteMessageSequences(d.Metadata)...)
	sort.Ints(in.Sequences)
	seqs := []int{}
	for _, n := range in.Sequences {
		if len(seqs) == 0 || seqs[len(seqs)-1] != n {
			seqs = append(seqs, n)
		}
	}
	in.Sequences = seqs
	if len(in.Sequences) == 0 || len(in.Sequences) > 32 || in.ConversationRevision <= 0 {
		return in, fmt.Errorf("no_exact_source_anchor")
	}
	return in, nil
}

// A unit is a complete bounded source window, not a claimed semantic event.
// Its identity contains origin and revision; equal text cannot merge origins.
func sourceUnit(d domain.MemoryDocument, in domain.SourceWindowInput, turns []domain.SourceTurn) (domain.MemoryDocument, error) {
	if len(turns) == 0 || len(turns) > 12 {
		return domain.MemoryDocument{}, fmt.Errorf("unit_turn_budget")
	}
	turns = append([]domain.SourceTurn(nil), turns...)
	sort.Slice(turns, func(i, j int) bool { return turns[i].Sequence < turns[j].Sequence })
	anchors := map[int]bool{}
	refs := []domain.SourceRef{}
	var text strings.Builder
	coordinates := []any{}
	quotes := []any{}
	for _, turn := range turns {
		if turn.UserID != d.UserID || turn.WorkspaceID != d.WorkspaceID || turn.ConversationID != d.ConversationID {
			return domain.MemoryDocument{}, domain.ErrAccessDenied
		}
		if turn.Anchor {
			anchors[turn.Sequence] = true
		}
		refs = append(refs, turn.Refs...)
		// External dataset/source identifiers never enter reranker or reader text.
		meta := map[string]any{"speaker": turn.Speaker, "role": turn.Role}
		if turn.SourceDate != "" {
			meta["source_time"] = turn.SourceDate
		}
		if !turn.OccurredAt.IsZero() {
			meta["observed_at"] = turn.OccurredAt.UTC().Format(time.RFC3339)
		}
		raw, _ := json.Marshal(meta)
		text.Write(raw)
		text.WriteByte('\n')
		text.WriteString(turn.Text)
		text.WriteString("\n\n")
		coordinates = append(coordinates, []any{turn.Sequence, turn.Refs, turn.Text, turn.Speaker, turn.SourceDate, turn.OccurredAt})
		quotes = append(quotes, map[string]any{"sequence_num": turn.Sequence, "sequence_base": 0})
	}
	for _, seq := range in.Sequences {
		if !anchors[seq] {
			return domain.MemoryDocument{}, fmt.Errorf("missing_source_anchor")
		}
	}
	for _, ref := range in.Refs {
		found := false
		for _, actual := range refs {
			if actual.WorkspaceID == ref.WorkspaceID && actual.MessageID == ref.MessageID && actual.Revision == ref.Revision && actual.PartIndex == ref.PartIndex {
				found = true
				break
			}
		}
		if !found {
			return domain.MemoryDocument{}, fmt.Errorf("missing_source_revision")
		}
	}
	content := strings.TrimSpace(text.String())
	if len(content) > 8192 {
		return domain.MemoryDocument{}, fmt.Errorf("unit_byte_budget")
	}
	raw, _ := json.Marshal([]any{d.UserID, d.WorkspaceID, d.PartitionID, d.GenerationID, d.ConversationID, in.ConversationRevision, coordinates})
	sum := sha256.Sum256(raw)
	id := "unit_" + hex.EncodeToString(sum[:])
	return domain.MemoryDocument{ID: id, NodeID: id, UserID: d.UserID, WorkspaceID: d.WorkspaceID, PartitionID: d.PartitionID, GenerationID: d.GenerationID, ConversationID: d.ConversationID, Revision: 1, Type: domain.MemoryTypeSegment, Content: content, Importance: 1, DecayFactor: 1, ActivityAt: d.ActivityAt, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, SourceRefs: refs, Metadata: map[string]any{"source_unit": true, "source_conversation_revision": in.ConversationRevision, "source_quotes": quotes}}, nil
}

func buildSourceUnitIndex(ctx context.Context, user, workspace, generation string, keys []domain.MemoryDocument, reader ports.SourceContextReader, lookup ports.MemoryRepository) (*sourceUnitIndex, error) {
	w, err := security.DataScope(ctx, user, workspace)
	if err != nil {
		return nil, err
	}
	if _, authenticated := security.AccessFromContext(ctx); authenticated {
		if err := security.RequireCapability(ctx, domain.CapabilitySourcesRead); err != nil {
			return nil, err
		}
	}
	idx := &sourceUnitIndex{InMemoryMemoryRepository: memorystore.NewInMemoryMemoryRepository(), lookup: lookup, User: user, Workspace: w, Generation: generation, Forward: map[string]string{}, Units: map[string]domain.MemoryDocument{}, Rejected: map[string]int{}, KeyCount: len(keys)}
	cache := map[string][]domain.SourceTurn{}
	sort.Slice(keys, func(i, j int) bool { return keys[i].NodeID < keys[j].NodeID })
	for _, d := range keys {
		if d.UserID != user || d.WorkspaceID != w || d.GenerationID != generation || !security.CanReadPartition(ctx, d.PartitionID) {
			return nil, domain.ErrAccessDenied
		}
		if d.Metadata["is_superseded"] == true {
			idx.Rejected["superseded"]++
			continue
		}
		in, err := unitAnchorInput(d)
		if err != nil {
			idx.Rejected[err.Error()]++
			continue
		}
		raw, _ := json.Marshal(in)
		turns, ok := cache[string(raw)]
		if !ok {
			turns, err = reader.ReadSourceWindow(ctx, in)
			if err != nil {
				return nil, err
			}
			cache[string(raw)] = turns
		}
		unit, err := sourceUnit(d, in, turns)
		if err != nil {
			if errors.Is(err, domain.ErrAccessDenied) {
				return nil, err
			}
			idx.Rejected[err.Error()]++
			continue
		}
		idx.Forward[d.NodeID] = unit.NodeID
		if old, ok := idx.Units[unit.NodeID]; ok {
			if old.ActivityAt.After(unit.ActivityAt) {
				unit.ActivityAt = old.ActivityAt
			}
			if old.CreatedAt.Before(unit.CreatedAt) {
				unit.CreatedAt = old.CreatedAt
			}
			if old.UpdatedAt.After(unit.UpdatedAt) {
				unit.UpdatedAt = old.UpdatedAt
			}
		}
		idx.Units[unit.NodeID] = unit
	}
	ids := make([]string, 0, len(idx.Units))
	for id := range idx.Units {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := idx.StoreDocument(ctx, idx.Units[id]); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

func (r *sourceUnitIndex) SearchKeywordMatches(ctx context.Context, u, g, q string, limit int, kinds ...domain.MemoryType) ([]domain.KeywordMatch, error) {
	if u != r.User || g != r.Generation {
		return nil, domain.ErrAccessDenied
	}
	matches, err := r.lookup.SearchKeywordMatches(ctx, u, g, q, limit, kinds...)
	if err != nil {
		return nil, err
	}
	out := []domain.KeywordMatch{}
	for _, m := range matches {
		if id := r.Forward[m.NodeID]; id != "" {
			m.NodeID = id
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *sourceUnitIndex) MatchEntityNames(ctx context.Context, u, g, scope string, names []string) ([]domain.EntityNameMatch, error) {
	if u != r.User || g != r.Generation {
		return nil, domain.ErrAccessDenied
	}
	matches, err := r.lookup.MatchEntityNames(ctx, u, g, scope, names)
	if err != nil {
		return nil, err
	}
	out := []domain.EntityNameMatch{}
	for _, m := range matches {
		if id := r.Forward[m.NodeID]; id != "" {
			m.NodeID = id
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *sourceUnitIndex) ListEntityAliases(ctx context.Context, u, g string) ([]domain.EntityAlias, error) {
	if u != r.User || g != r.Generation {
		return nil, domain.ErrAccessDenied
	}
	aliases, err := r.lookup.ListEntityAliases(ctx, u, g)
	if err != nil {
		return nil, err
	}
	out := []domain.EntityAlias{}
	for _, a := range aliases {
		a.SourceNodeID = r.Forward[a.SourceNodeID]
		a.TargetNodeID = r.Forward[a.TargetNodeID]
		if a.TargetNodeID != "" {
			out = append(out, a)
		}
	}
	return out, nil
}

// Project existing relations only, removing self loops. No entity identity or
// event relation is inferred by sharing a window. Native store bounds traversal.
func (r *sourceUnitIndex) projectRelations(ctx context.Context) error {
	ids := make([]string, 0, len(r.Forward))
	for id := range r.Forward {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	relations, err := r.lookup.ListRelations(ctx, r.User, r.Generation, ids, 1)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, edge := range relations {
		edge.SrcNodeID = r.Forward[edge.SrcNodeID]
		edge.DstNodeID = r.Forward[edge.DstNodeID]
		if edge.SrcNodeID == "" || edge.DstNodeID == "" || edge.SrcNodeID == edge.DstNodeID {
			continue
		}
		key := edge.SrcNodeID + "|" + edge.DstNodeID + "|" + edge.RelationType
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := r.LinkDocuments(ctx, edge); err != nil {
			return err
		}
		r.GraphEdges++
	}
	return nil
}

type unitVectors struct {
	ports.VectorStore
	Snapshot frozenRecallSnapshot
	Index    *sourceUnitIndex
}

func (v unitVectors) Search(ctx context.Context, in domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	original, err := (frozenRecallVectors{snapshot: v.Snapshot}).Search(ctx, in)
	if err != nil {
		return nil, err
	}
	if in.GenerationID != v.Index.Generation || in.WorkspaceID != v.Index.Workspace {
		return nil, domain.ErrAccessDenied
	}
	out := []domain.VectorSearchResult{}
	for _, m := range original {
		if id := v.Index.Forward[m.NodeID]; id != "" {
			m.NodeID = id
			m.NodeType = string(domain.MemoryTypeSegment)
			out = append(out, m)
		}
	}
	return out, nil
}

// Enumerates the complete authorized corpus before loading question snapshots.
// Both arms use the existing MemoryEngine. No parallel ranking implementation.
func TestPrepareLOCOMOSourceUnits(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_CASES")
	if path == "" {
		t.Skip("manual source-only prototype")
	}
	var rows []packetReplayCase
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		t.Fatal("invalid cases")
	}
	out := os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_OUT")
	if out == "" || os.Mkdir(out, 0o700) != nil {
		t.Fatal("fresh output required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	db, err := OpenEvaluationDatabase(ctx, os.Getenv("MEMSTATION_EVAL_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var readonly string
	if err := db.QueryRowContext(ctx, `SHOW default_transaction_read_only`).Scan(&readonly); err != nil || readonly != "on" {
		t.Fatal("read-only database required")
	}
	first := rows[0].Documents[0]
	repo := memorystore.NewPostgresMemoryRepository(db)
	sources := conversationstore.NewPostgresConversationRepository(db)
	keys, err := repo.ListDocumentsInGeneration(ctx, first.UserID, first.GenerationID, nil, 50000)
	if err != nil || len(keys) >= 50000 {
		t.Fatal("source corpus enumeration failed", err)
	}
	index, err := buildSourceUnitIndex(ctx, first.UserID, first.WorkspaceID, first.GenerationID, keys, sources, repo)
	if err != nil {
		t.Fatal("source-only build failed", err)
	}
	if err := index.projectRelations(ctx); err != nil {
		t.Fatal("source relation projection failed", err)
	}
	if err := writePipelineJSON(filepath.Join(out, "source-index.json"), index); err != nil {
		t.Fatal(err)
	}
	t.Logf("source-only index: %d keys, %d mapped, %d units, %d edges, rejected %v", len(keys), len(index.Forward), len(index.Units), index.GraphEdges, index.Rejected)
	for _, row := range rows {
		data, err := os.ReadFile(filepath.Join(os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_SNAPSHOTS"), row.Baseline.QuestionID+".json"))
		var snapshot frozenRecallSnapshot
		if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot.Input.Query != row.Baseline.Query || snapshot.Input.UserID != index.User || (snapshot.Input.GenerationID != "" && snapshot.Input.GenerationID != index.Generation) || snapshot.Input.AsOf != nil || snapshot.Input.KnownAt != nil || len(snapshot.Input.SubjectActorIDs) > 0 {
			t.Fatal("unsupported or mismatched snapshot", err)
		}
		engine := memoryservice.NewEngine(index, unitVectors{Snapshot: snapshot, Index: index})
		engine.SetClock(func() time.Time { return snapshot.CapturedAt })
		engine.SetEntityRecall(frozenRecallEntities{snapshot}, 200*time.Millisecond)
		configurePipelineReranker(engine, PipelineRerankOptions{Model: os.Getenv("RERANK_MODEL"), BaseURL: os.Getenv("RERANK_BASE_URL"), APIKey: os.Getenv("RERANK_API_KEY"), MaxCandidates: 50, Timeout: 2 * time.Minute})
		captureOnly := os.Getenv("MEMSTATION_EVAL_RECALL_REPLAY_CAPTURE_ONLY") == "true"
		capture := &recallSnapshotReranker{}
		if captureOnly {
			engine.SetReranker(capture)
		}
		output := filepath.Join(out, row.Baseline.QuestionID+".json")
		if err := writePipelineJSON(output, map[string]any{"error": "attempt interrupted"}); err != nil {
			t.Fatal(err)
		}
		found, err := engine.SearchWithVariant(ctx, snapshot.Input, memoryservice.SearchVariantFull)
		result := map[string]any{"snapshot_sha256": pipelineHash(string(data)), "retrieval": found, "capture_only": captureOnly}
		failure := ""
		if err != nil {
			failure = "retrieval_failed"
		} else if !pipelineEntityRecallComplete(found.Result.EntityRecall) {
			failure = "entity_recall_incomplete"
		}
		if captureOnly {
			result["admitted_rerank_input"] = capture.Input
		} else if failure == "" {
			if found.Rerank.Status != "applied" {
				failure = "reranker_not_applied"
			} else {
				packet, err := engine.PrepareEvidence(ctx, snapshot.Input, found.Candidates, memoryservice.EvidenceOptions{MaxBytes: 16000})
				if err != nil {
					failure = "evidence_failed"
				} else {
					result["packet"] = packet
				}
			}
		}
		if failure != "" {
			result["error"] = failure
		}
		if err := writePipelineJSON(output, result); err != nil {
			t.Fatal(err)
		}
		if failure != "" {
			t.Errorf("case %s failed: %s", row.Baseline.QuestionID, failure)
		}
		t.Logf("prepared %s, candidates %d, rerank bytes %d", row.Baseline.QuestionID, found.Rerank.Candidates, found.Rerank.InputBytes)
	}
}

// Synthetic mechanism checks do not use any LOCOMO content.
func TestSourceUnitMechanisms(t *testing.T) {
	key := domain.MemoryDocument{ID: "key", NodeID: "key", UserID: "owner", WorkspaceID: "owner", GenerationID: "generation", ConversationID: "conversation", Type: domain.MemoryTypeFact, Metadata: map[string]any{"source_conversation_revision": 1, "source_quotes": []any{map[string]any{"sequence_num": 1, "sequence_base": 0}}}}
	cases := []struct {
		name  string
		texts []string
	}{
		{"invitation_acceptance_purpose", []string{"Mira invited Jo to the studio to plan a mural.", "Yes, I will come.", "Bring your sketches so we can compare layouts."}},
		{"plan_outcome_cancel", []string{"I plan to visit Oslo next month.", "The visit was cancelled.", "I stayed home instead."}},
		{"same_name_distinct_people", []string{"Alex from my lab made the prototype.", "Alex from my club asked about it.", "They have never met."}},
		{"recurring_vs_repeat_report", []string{"We meet every Friday.", "Last Friday's meeting was postponed.", "I told you about that same postponement yesterday."}},
		{"state_update", []string{"The office was in Paris.", "We moved it to Lyon in March.", "The old address is no longer current."}},
		{"cross_batch_pronoun", []string{"Nora lent Ben the bicycle.", "He returned it on Monday.", "She checked the brakes."}},
		{"uncertain_time", []string{"Sometime around late spring we met.", "I cannot remember the exact day.", "It was before the June conference."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			turns := []domain.SourceTurn{}
			for n, text := range tc.texts {
				turns = append(turns, domain.SourceTurn{UserID: "owner", WorkspaceID: "owner", ConversationID: "conversation", Sequence: n, Anchor: n == 1, Speaker: fmt.Sprintf("speaker-%d", n%2), Text: text, SourceDate: "2024-08-08"})
			}
			in, err := unitAnchorInput(key)
			if err != nil {
				t.Fatal(err)
			}
			u, err := sourceUnit(key, in, turns)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range tc.texts {
				if !strings.Contains(u.Content, text) {
					t.Fatal("original source was lost")
				}
			}
			if strings.Contains(u.Content, "2026") || len(u.SubjectActorIDs) > 0 {
				t.Fatal("inferred time or actor identity")
			}
			other := key
			other.ConversationID = "other"
			otherTurns := append([]domain.SourceTurn(nil), turns...)
			for i := range otherTurns {
				otherTurns[i].ConversationID = "other"
			}
			v, err := sourceUnit(other, in, otherTurns)
			if err != nil || v.ID == u.ID {
				t.Fatal("equal text merged distinct origins", err)
			}
			changed := append([]domain.SourceTurn(nil), turns...)
			changed[1].Text += " Correction."
			v, err = sourceUnit(key, in, changed)
			if err != nil || v.ID == u.ID {
				t.Fatal("edit did not invalidate unit identity", err)
			}
			if _, err := sourceUnit(key, in, append(turns[:1:1], turns[2:]...)); err == nil {
				t.Fatal("deleted anchor silently retained")
			}
			changed = append([]domain.SourceTurn(nil), turns...)
			changed[0].UserID = "other"
			if _, err := sourceUnit(key, in, changed); !errors.Is(err, domain.ErrAccessDenied) {
				t.Fatal("cross-owner source accepted")
			}
		})
	}
	t.Run("versioned_refs_no_sequence_fallback", func(t *testing.T) {
		d := key
		d.SourceRefs = []domain.SourceRef{{WorkspaceID: "owner", MessageID: "message", Revision: 2, PartIndex: 0}}
		in, err := unitAnchorInput(d)
		if err != nil || len(in.Sequences) > 0 {
			t.Fatal("versioned source used positions")
		}
		turn := domain.SourceTurn{UserID: "owner", WorkspaceID: "owner", ConversationID: "conversation", Anchor: true, Text: "An exact quote.", Refs: []domain.SourceRef{{WorkspaceID: "owner", MessageID: "message", Revision: 1, PartIndex: 0}}}
		if _, err := sourceUnit(d, in, []domain.SourceTurn{turn}); err == nil {
			t.Fatal("stale revision accepted")
		}
	})
	t.Run("whole_units_not_truncated", func(t *testing.T) {
		in, _ := unitAnchorInput(key)
		turn := domain.SourceTurn{UserID: "owner", WorkspaceID: "owner", ConversationID: "conversation", Sequence: 1, Anchor: true, Text: strings.Repeat("x", 8193)}
		if _, err := sourceUnit(key, in, []domain.SourceTurn{turn}); err == nil {
			t.Fatal("oversized value silently truncated")
		}
	})
	t.Run("scope_checked_before_source_read", func(t *testing.T) {
		d := key
		d.WorkspaceID = "other"
		if _, err := buildSourceUnitIndex(context.Background(), "owner", "owner", "generation", []domain.MemoryDocument{d}, nil, nil); !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatal("wrong workspace reached source reader")
		}
	})
}

type unitFixtureSource struct{ turns []domain.SourceTurn }

func (s unitFixtureSource) ReadSourceWindow(_ context.Context, _ domain.SourceWindowInput) ([]domain.SourceTurn, error) {
	return s.turns, nil
}

func TestSourceUnitKeyDedupAndCapability(t *testing.T) {
	workspace := security.WorkspaceID(context.Background(), "owner")
	d := domain.MemoryDocument{ID: "key1", NodeID: "key1", UserID: "owner", WorkspaceID: workspace, GenerationID: "generation", ConversationID: "conversation", Type: domain.MemoryTypeFact, Content: "A derived retrieval key.", Metadata: map[string]any{"source_conversation_revision": 1, "source_quotes": []any{map[string]any{"sequence_num": 1, "sequence_base": 0}}}}
	other := d
	other.ID = "key2"
	other.NodeID = "key2"
	other.Content = "An entity mention."
	other.Type = domain.MemoryTypeEntity
	turns := []domain.SourceTurn{{UserID: "owner", WorkspaceID: workspace, ConversationID: "conversation", Sequence: 1, Anchor: true, Text: "I went to the studio to choose materials for our mural."}}
	index, err := buildSourceUnitIndex(context.Background(), "owner", workspace, "generation", []domain.MemoryDocument{d, other}, unitFixtureSource{turns}, nil)
	if err != nil || len(index.Forward) != 2 || len(index.Units) != 1 {
		t.Fatal("keys did not share canonical original evidence", err)
	}
	for _, u := range index.Units {
		if strings.Contains(u.Content, d.Content) || !strings.Contains(u.Content, turns[0].Text) {
			t.Fatal("derived key replaced original evidence")
		}
	}
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: "owner", WorkspaceID: workspace, Capabilities: []string{"memory.read"}})
	if _, err := buildSourceUnitIndex(ctx, "owner", workspace, "generation", []domain.MemoryDocument{d}, nil, nil); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("missing sources.read was not rejected before expansion", err)
	}
}
