package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

// EvidenceOptions budgets the complete rendered context, including provenance.
// Token counts are conservative estimates, not a provider-specific tokenizer.
type EvidenceOptions struct {
	RequiredIDs        []string `json:"required_ids,omitempty"`
	MaxBytes           int      `json:"max_bytes"`
	MaxEstimatedTokens int      `json:"max_estimated_tokens"`
	NeighborTurns      int      `json:"neighbor_turns"`
	MaxAnchors         int      `json:"max_anchors"`
}

// EvidenceConfig is opt-in until controlled QA demonstrates a quality benefit.
// Production and evaluation share PrepareEvidence without changing defaults.
type EvidenceConfig struct {
	Enabled       bool
	NeighborTurns int
}

func (e *Engine) SetEvidenceConfig(config EvidenceConfig) error {
	if config.NeighborTurns < 0 || config.NeighborTurns > 2 {
		return fmt.Errorf("source neighbor radius must be between zero and two")
	}
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.evidence = config
	return nil
}

func (e *Engine) evidenceConfig() EvidenceConfig {
	e.configMu.RLock()
	defer e.configMu.RUnlock()
	return e.evidence
}

type EvidenceDiagnostics struct {
	Core              *EvidenceCoverage `json:"core,omitempty"`
	Anchors           int               `json:"anchors"`
	SourceTurns       int               `json:"source_turns"`
	PackedSourceTurns int               `json:"packed_source_turns"`
	Unavailable       int               `json:"unavailable"`
	SourcesDisabled   bool              `json:"sources_disabled"`
}

type EvidencePacket struct {
	Text        string                  `json:"text"`
	Memories    []domain.MemoryDocument `json:"memories"`
	SourceIDs   map[string]string       `json:"source_ids"`
	Diagnostics EvidenceDiagnostics     `json:"diagnostics"`
}

func (e *Engine) SetSourceContextReader(reader ports.SourceContextReader) { e.sources = reader }

// FormatEvidenceDocument emits only whitelisted provenance. Stored timestamps
// are labelled as recorded_at; they are never substituted for event/source time.
func FormatEvidenceDocument(doc domain.MemoryDocument) string {
	meta := map[string]any{"id": doc.ID, "type": doc.Type}
	if doc.ConversationID != "" {
		meta["conversation_id"] = doc.ConversationID
	}
	if !doc.CreatedAt.IsZero() {
		meta["recorded_at"] = doc.CreatedAt.UTC().Format(time.RFC3339)
	}
	if len(doc.SubjectActorIDs) > 0 {
		meta["subject_actor_ids"] = doc.SubjectActorIDs
	}
	if len(doc.SourceRefs) > 0 {
		meta["source_refs"] = doc.SourceRefs
	}
	for _, k := range []string{"participants", "time_anchor", "source_times", "semantic_unit_version", "speaker", "role", "source_time", "session_date", "source_session_time", "valid_from", "valid_to", "event_time_precision", "status", "is_superseded", "support_kind", "subject_scope", "source_context", "source_parts", "start_sequence", "end_sequence", "ref"} {
		if v, ok := doc.Metadata[k]; ok && v != nil {
			meta[k] = v
		}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		raw = []byte(`{"provenance":"unavailable"}`)
	}
	return string(raw) + "\n" + strings.TrimSpace(doc.Content)
}

// PrepareEvidence validates current anchor nodes before bounded source reads.
// Historical snapshots never hydrate today's mutable sources. Missing sources
// degrade to the retained memory; caller cancellation and access errors fail.
func (e *Engine) PrepareEvidence(ctx context.Context, input domain.SearchInput, docs []domain.MemoryDocument, o EvidenceOptions) (EvidencePacket, error) {
	var packet EvidencePacket
	workspace, err := security.DataScope(ctx, input.UserID, input.WorkspaceID)
	if err != nil {
		return packet, err
	}
	bound, err := security.WithDataScope(ctx, input.UserID, workspace, security.Partitions(ctx)...)
	if err != nil {
		return packet, err
	}
	ctx = bound
	if o.MaxBytes <= 0 {
		o.MaxBytes = 16000
	}
	o.MaxBytes = min(o.MaxBytes, 256000)
	if o.MaxAnchors <= 0 {
		o.MaxAnchors = 20
	}
	o.MaxAnchors = min(o.MaxAnchors, 32)
	if o.NeighborTurns < 0 || o.NeighborTurns > 2 {
		return packet, fmt.Errorf("source neighbor radius must be between zero and two")
	}
	w := security.WorkspaceID(ctx, input.UserID)
	for _, doc := range docs {
		if doc.UserID != input.UserID || (doc.WorkspaceID != "" && doc.WorkspaceID != w) || !security.CanReadPartition(ctx, doc.PartitionID) {
			return packet, domain.ErrAccessDenied
		}
	}
	var sourceDocs []domain.MemoryDocument
	packet.Diagnostics.SourcesDisabled = e.sources == nil || o.NeighborTurns == 0 || input.AsOf != nil || input.KnownAt != nil
	if _, authenticated := security.AccessFromContext(ctx); authenticated {
		if err := security.RequireCapability(ctx, domain.CapabilitySourcesRead); err != nil {
			packet.Diagnostics.SourcesDisabled = true
		}
	}
	if !packet.Diagnostics.SourcesDisabled {
		// Re-read each bounded generation group so cached or deleted anchor
		// metadata cannot authorize reading a different conversation or revision.
		groups := map[string][]string{}
		for _, doc := range docs {
			if doc.NodeID != "" {
				groups[doc.GenerationID] = append(groups[doc.GenerationID], doc.NodeID)
			}
		}
		canonical := map[string]domain.MemoryDocument{}
		for generation, ids := range groups {
			if input.GenerationID != "" && generation != input.GenerationID {
				return packet, domain.ErrAccessDenied
			}
			current, err := e.repo.ListDocumentsByNodeIDsInGeneration(ctx, input.UserID, generation, ids)
			if err != nil {
				return packet, err
			}
			for _, doc := range current {
				canonical[doc.NodeID] = doc
			}
		}
		validDocs := []domain.MemoryDocument{}
		for _, doc := range docs {
			current, ok := canonical[doc.NodeID]
			if ok && current.ID == doc.ID && current.GenerationID == doc.GenerationID && current.Revision == doc.Revision {
				validDocs = append(validDocs, doc)
			} else {
				packet.Diagnostics.Unavailable++
			}
		}
		docs = validDocs
		seen := map[string]bool{}
		for _, original := range docs[:min(len(docs), o.MaxAnchors)] {
			doc, ok := canonical[original.NodeID]
			if !ok || doc.ConversationID == "" || doc.GenerationID != original.GenerationID || doc.Revision != original.Revision {
				packet.Diagnostics.Unavailable++
				continue
			}
			seqs := sourceAnchorSequences(doc)
			if len(seqs) == 0 && len(doc.SourceRefs) == 0 {
				continue
			}
			packet.Diagnostics.Anchors++
			conversationRevision, _ := evidenceInt(doc.Metadata["source_conversation_revision"])
			turns, err := e.sources.ReadSourceWindow(ctx, domain.SourceWindowInput{UserID: input.UserID, WorkspaceID: w, ConversationID: doc.ConversationID, ConversationRevision: int64(conversationRevision), Sequences: seqs, Refs: doc.SourceRefs[:min(len(doc.SourceRefs), 32)], Radius: o.NeighborTurns, Limit: 8, MaxBytes: min(o.MaxBytes/3, 8192)})
			if err != nil {
				if ctx.Err() != nil {
					return packet, ctx.Err()
				}
				if errors.Is(err, domain.ErrAccessDenied) {
					return packet, err
				}
				packet.Diagnostics.Unavailable++
				continue
			}
			if len(turns) == 0 {
				packet.Diagnostics.Unavailable++
			}
			for _, turn := range turns {
				if turn.UserID != input.UserID || turn.WorkspaceID != w || turn.ConversationID != doc.ConversationID {
					return packet, domain.ErrAccessDenied
				}
				id := fmt.Sprintf("source:%s:%d", turn.ConversationID, turn.Sequence)
				if seen[id] {
					continue
				}
				seen[id] = true
				meta := map[string]any{"source_context": true, "start_sequence": turn.Sequence, "end_sequence": turn.Sequence, "speaker": turn.Speaker, "role": turn.Role}
				if len(turn.Parts) > 0 {
					meta["source_parts"] = turn.Parts
				}
				if turn.ExternalRef != "" {
					meta["ref"] = turn.ExternalRef
				}
				if turn.SourceDate != "" {
					meta["session_date"] = turn.SourceDate
				}
				if !turn.OccurredAt.IsZero() {
					meta["source_time"] = turn.OccurredAt.UTC().Format(time.RFC3339)
				}
				sourceDocs = append(sourceDocs, domain.MemoryDocument{ID: id, UserID: input.UserID, WorkspaceID: w, PartitionID: doc.PartitionID, ConversationID: turn.ConversationID, Type: domain.MemoryTypeSegment, Content: turn.Text, SourceRefs: turn.Refs, Metadata: meta})
			}
		}
	}
	packet.Diagnostics.SourceTurns = len(sourceDocs)
	packet.Memories = packEvidence(docs, sourceDocs, o)
	if len(o.RequiredIDs) > 0 {
		coverage := MeasureEvidenceCoverage(o.RequiredIDs, packet.Memories)
		packet.Diagnostics.Core = &coverage
	}
	for _, doc := range packet.Memories {
		if v, _ := doc.Metadata["source_context"].(bool); v {
			packet.Diagnostics.PackedSourceTurns++
		}
	}
	if len(packet.Memories) > 0 {
		packet.Text = buildEvidenceSystemPrompt(packet.Memories)
	}
	packet.SourceIDs = make(map[string]string, len(packet.Memories))
	for i, doc := range packet.Memories {
		packet.SourceIDs[fmt.Sprintf("memory-%d", i+1)] = doc.ID
	}
	return packet, ctx.Err()
}

func sourceAnchorSequences(doc domain.MemoryDocument) []int {
	if len(doc.SourceRefs) > 0 {
		return nil
	}
	var out []int
	if doc.Type == domain.MemoryTypeSegment || doc.Metadata["source_turn"] == true {
		if start, ok := evidenceInt(doc.Metadata["start_sequence"]); ok && start >= 0 {
			out = append(out, start)
			if end, ok := evidenceInt(doc.Metadata["end_sequence"]); ok && end > start {
				out = append(out, end)
			}
		}
	}
	out = append(out, domain.SourceQuoteMessageSequences(doc.Metadata)...)
	return out[:min(len(out), 32)]
}

func evidenceInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), int64(int(x)) == x
	case float64:
		return int(x), x == float64(int(x))
	case json.Number:
		i, e := x.Int64()
		return int(i), e == nil
	}
	return 0, false
}

func packEvidence(core, sources []domain.MemoryDocument, o EvidenceOptions) []domain.MemoryDocument {
	var selected []domain.MemoryDocument
	seen := map[string]bool{}
	seenTurns := map[string]bool{}
	add := func(d domain.MemoryDocument, capBytes, capTokens int) {
		if strings.TrimSpace(d.Content) == "" {
			return
		}
		// Provenance is part of identity. Equal text from different people or
		// dates is not interchangeable evidence. Complementary facts survive.
		identity := FormatEvidenceDocument(d)
		turnIdentity := evidenceTurnIdentity(d)
		if seen[identity] || (turnIdentity != "" && seenTurns[turnIdentity]) {
			return
		}
		next := append(append([]domain.MemoryDocument(nil), selected...), d)
		text := buildEvidenceSystemPrompt(next)
		if len(text) > capBytes || (capTokens > 0 && estimateEvidenceTokens(text) > capTokens) {
			return
		}
		selected = next
		seen[identity] = true
		if turnIdentity != "" {
			seenTurns[turnIdentity] = true
		}
	}
	coreCap := o.MaxBytes
	required := map[string]bool{}
	for _, id := range o.RequiredIDs {
		required[id] = true
	}
	// Source expansion and the normal 2/3 reserve cannot evict protected core.
	// Oversized whole records remain omitted and are explicitly diagnosed.
	for _, d := range core {
		if required[d.ID] {
			add(d, o.MaxBytes, o.MaxEstimatedTokens)
		}
	}
	coreTokens := o.MaxEstimatedTokens
	if len(sources) > 0 {
		coreCap = o.MaxBytes * 2 / 3
		if coreTokens > 0 {
			coreTokens = max(1, coreTokens*2/3)
		}
	}
	for _, d := range core {
		add(d, coreCap, coreTokens)
	}
	for _, d := range sources {
		add(d, o.MaxBytes, o.MaxEstimatedTokens)
	}
	for _, d := range core {
		add(d, o.MaxBytes, o.MaxEstimatedTokens)
	}
	return selected
}

// Only complete original turns share an identity with hydrated turns. Derived
// facts and excerpts may be complementary even when their source is identical.
func evidenceTurnIdentity(d domain.MemoryDocument) string {
	if d.ConversationID == "" || (d.Metadata["source_turn"] != true && d.Metadata["source_context"] != true) {
		return ""
	}
	start, ok := evidenceInt(d.Metadata["start_sequence"])
	end, endOK := evidenceInt(d.Metadata["end_sequence"])
	if !ok || !endOK || start < 0 || start != end {
		return ""
	}
	var refs []byte
	if len(d.SourceRefs) > 0 {
		refs, _ = json.Marshal(d.SourceRefs)
	}
	return fmt.Sprintf("%s:%d:%s", d.ConversationID, start, refs)
}

func estimateEvidenceTokens(text string) int {
	return max(len(strings.Fields(text))*2, (len(text)+3)/4)
}
