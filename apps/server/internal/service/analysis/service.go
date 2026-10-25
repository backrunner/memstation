package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/memoryllm"
	"memstation/apps/server/internal/textutil"
)

type Service struct{ generator memoryllm.Generator }

func NewService(generators ...memoryllm.Generator) *Service {
	s := &Service{}
	if len(generators) > 0 {
		s.generator = generators[0]
	}
	return s
}

type Input struct {
	UserID         string
	ConversationID string
	Conversation   domain.ConversationRecord
	Summary        domain.MemoryDocument
	ExistingFacts  []domain.MemoryDocument
	WritePolicy    MemoryWritePolicy
}
type MemoryWritePolicy struct {
	EnableGenericItems bool
	MaxGenericItems    int
}
type Result struct {
	Fact         domain.MemoryDocument
	Facts        []domain.MemoryDocument
	Impression   domain.MemoryDocument
	GenericItems []domain.MemoryDocument
	Relations    []domain.MemoryRelation
}
type Quote struct {
	SourceID string `json:"source_id"`
	Text     string `json:"text"`
}
type Item struct {
	SubjectActorIDs []string `json:"subject_actor_ids,omitempty"`
	SubjectScope    string   `json:"subject_scope,omitempty"`
	Content         string   `json:"content"`
	Importance      float64  `json:"importance"`
	DecayFactor     float64  `json:"decay_factor"`
	Confidence      float64  `json:"confidence"`
	Subject         string   `json:"subject"`
	Quotes          []Quote  `json:"quotes"`
}
type Fact struct {
	Item
	Key        string   `json:"key"`
	Category   string   `json:"category"`
	Supersedes []string `json:"supersedes"`
	ValidFrom  string   `json:"valid_from,omitempty"`
	ValidTo    string   `json:"valid_to,omitempty"`
}
type GenericItem struct {
	Item
	Key           string            `json:"key"`
	Type          domain.MemoryType `json:"type"`
	Title         string            `json:"title"`
	Status        string            `json:"status"`
	EntityKind    string            `json:"entity_kind,omitempty"`
	CanonicalName string            `json:"canonical_name,omitempty"`
	ArtifactID    string            `json:"artifact_id,omitempty"`
}
type Relation struct {
	SourceKey string  `json:"source_key"`
	TargetKey string  `json:"target_key"`
	Type      string  `json:"type"`
	Weight    float64 `json:"weight"`
}

type Extraction struct {
	Relations  []Relation    `json:"relations,omitempty"`
	Facts      []Fact        `json:"facts"`
	Impression *Item         `json:"impression"`
	Items      []GenericItem `json:"items"`
}
type Summary struct {
	Summary     string   `json:"summary"`
	TopicLabel  string   `json:"topic_label"`
	Keywords    []string `json:"keywords"`
	Importance  float64  `json:"importance"`
	DecayFactor float64  `json:"decay_factor"`
	ChunkCount  int      `json:"-"`
}
type Source struct {
	Ref             *domain.SourceRef  `json:"source_ref,omitempty"`
	SpeakerActorID  string             `json:"speaker_actor_id,omitempty"`
	AuthorActorID   string             `json:"author_actor_id,omitempty"`
	MentionActorIDs []string           `json:"mention_actor_ids,omitempty"`
	Kind            string             `json:"kind,omitempty"`
	ID              string             `json:"id"`
	Role            domain.MessageRole `json:"role"`
	Text            string             `json:"text"`
	SequenceNum     int                `json:"sequence_num"`
	PartID          string             `json:"part_id,omitempty"`
}

// Sources serializes original material. Splitting is transport budgeting, not memory generation.
func Sources(conversation domain.ConversationRecord) []Source {
	out := []Source{}
	if conversation.ContextMode == "events" {
		for seq, message := range conversation.Messages {
			if message.Source == nil {
				continue
			}
			source := message.Source
			for i, part := range source.Parts {
				if strings.TrimSpace(part.Text) == "" {
					continue
				}
				ref := domain.SourceRef{WorkspaceID: source.WorkspaceID, MessageID: source.MessageID, Revision: source.Revision, PartIndex: i}
				author := source.Speaker.ActorID
				if part.Type == "quote" {
					author = ""
					if part.Author != nil {
						author = part.Author.ActorID
					}
				}
				mentions := []string{}
				for _, p := range source.Parts {
					if p.Type == "mention" && p.ActorID != "" {
						mentions = append(mentions, p.ActorID)
					}
				}
				out = append(out, Source{Kind: part.Type, ID: fmt.Sprintf("%s:%d:%d", source.MessageID, source.Revision, i), Role: message.Role, Text: part.Text, SequenceNum: seq + 1, Ref: &ref, SpeakerActorID: source.Speaker.ActorID, AuthorActorID: author, MentionActorIDs: mentions})
			}
		}
		return out
	}
	if len(conversation.Parts) > 0 {
		for _, part := range conversation.Parts {
			text := part.TextContent
			if text == "" {
				text = part.RawPayload
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			out = append(out, Source{Kind: string(part.PartType), ID: firstNonEmpty(part.PartID, fmt.Sprintf("message-%d", part.SequenceNum)), Role: part.Role, Text: text, SequenceNum: part.SequenceNum, PartID: part.PartID})
		}
	} else {
		for index, message := range conversation.Messages {
			raw, _ := json.Marshal(struct {
				Text    string                `json:"text,omitempty"`
				Blocks  []domain.ContentBlock `json:"blocks,omitempty"`
				Calls   []domain.ToolCall     `json:"tool_calls,omitempty"`
				Results []domain.ToolResult   `json:"tool_results,omitempty"`
			}{message.Text, message.Blocks, message.ToolCalls, message.ToolResults})
			text := provider.MessageText(message)
			if len(message.ToolCalls) > 0 || len(message.ToolResults) > 0 || len(message.Blocks) > 0 {
				text = string(raw)
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			out = append(out, Source{ID: fmt.Sprintf("message-%d", index+1), Role: message.Role, Text: text, SequenceNum: index + 1})
		}
	}
	for _, artifact := range conversation.Artifacts {
		if artifact.Status != "completed" || strings.TrimSpace(artifact.TextContent) == "" {
			continue
		}
		out = append(out, Source{Kind: "artifact", ID: artifact.ArtifactID, Role: domain.RoleTool, Text: artifact.TextContent, PartID: artifact.PartID})
	}
	return out
}

func sourceBatches(sources []Source) ([][]Source, error) {
	batches := [][]Source{}
	batch := []Source{}
	size := 0
	for _, source := range sources {
		remaining := source.Text
		offset := 0
		if source.Ref != nil && source.Ref.Span != nil {
			offset = source.Ref.Span.Start
		}
		for remaining != "" {
			piece := source
			piece.Text = textutil.PrefixBytes(remaining, 12000)
			if source.Ref != nil {
				ref := *source.Ref
				ref.Span = &domain.TextSpan{Start: offset, End: offset + utf8.RuneCountInString(piece.Text)}
				piece.Ref = &ref
			}
			offset += utf8.RuneCountInString(piece.Text)
			remaining = remaining[len(piece.Text):]
			if size+len(piece.Text) > 16000 && len(batch) > 0 {
				batches = append(batches, batch)
				batch = []Source{}
				size = 0
			}
			batch = append(batch, piece)
			size += len(piece.Text)
		}
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	if len(batches) > 32 {
		return nil, errors.New("memory source exceeds 32-chunk processing budget")
	}
	if len(batches) == 0 {
		return nil, errors.New("conversation has no processable source content")
	}
	return batches, nil
}

func (s *Service) generate(ctx context.Context, input Input, task, role, instruction string, payload any, target any) error {
	if s == nil || s.generator == nil {
		return errors.New("memory LLM is not configured")
	}
	budget := 4096
	if task == "analysis.extract" {
		budget = 8192
	}
	return memoryllm.GenerateDecoded(ctx, s.generator, memoryllm.Request{UserID: firstNonEmpty(input.UserID, input.Conversation.UserID), Role: role, Task: task, Instruction: instruction, Input: payload, MaxOutputTokens: budget}, target)
}

const summaryInstruction = `Summarize the supplied conversation sources faithfully, in their language. Preserve negation, attribution, uncertainty, decisions, changes and unresolved questions. Assistant suggestions are not user facts; plans are not completed events. Return {"summary":string,"topic_label":string,"keywords":[string],"importance":number,"decay_factor":number}. Importance must be 0.5..2 and decay_factor 0.5..3. No generic placeholder text.`

func (s *Service) SummarizeConversation(ctx context.Context, input Input) (Summary, error) {
	if err := validateInput(input); err != nil {
		return Summary{}, err
	}
	batches, err := sourceBatches(Sources(input.Conversation))
	if err != nil {
		return Summary{}, err
	}
	summaries := []Summary{}
	for _, batch := range batches {
		var result Summary
		if err := s.generate(ctx, input, "summary.generate", "summary_model", summaryInstruction, map[string]any{"sources": batch}, &result); err != nil {
			return Summary{}, err
		}
		if err := validateSummary(result); err != nil {
			return Summary{}, err
		}
		summaries = append(summaries, result)
	}
	// Bound each merge as well as each source chunk; 32 maximal summaries do
	// not fit the client input budget in one request.
	for len(summaries) > 1 {
		merged := []Summary{}
		for offset := 0; offset < len(summaries); offset += 4 {
			group := summaries[offset:min(offset+4, len(summaries))]
			result := group[0]
			if len(group) > 1 {
				if err := s.generate(ctx, input, "summary.merge", "summary_model", summaryInstruction+" Merge all chunk summaries chronologically, removing overlap without dropping material facts.", map[string]any{"chunk_summaries": group}, &result); err != nil {
					return Summary{}, err
				}
				if err := validateSummary(result); err != nil {
					return Summary{}, err
				}
			}
			merged = append(merged, result)
		}
		summaries = merged
	}
	result := summaries[0]
	result.ChunkCount = len(batches)
	return result, nil
}

func validateSummary(s Summary) error {
	if !validText(s.Summary, 24000) || !validText(s.TopicLabel, 240) || s.Keywords == nil || len(s.Keywords) > 20 || !validScore(s.Importance, s.DecayFactor) {
		return errors.New("invalid LLM summary fields")
	}
	for _, word := range s.Keywords {
		if !validText(word, 160) {
			return errors.New("invalid LLM summary keyword")
		}
	}
	return nil
}

const extractionInstruction = `Extract only durable, supported memories from the original sources. The summary and existing facts are context, never new evidence. Interpret negation, corrections, hypothetical examples, speaker attribution and temporal changes. Do not treat assistant suggestions as user commitments. Return {"facts":[],"impression":null,"items":[]}. Empty arrays and null impression are correct when there is nothing to remember.
Each fact is {"key":string,"category":string,"subject":string,"content":string,"confidence":number,"importance":number,"decay_factor":number,"quotes":[{"source_id":string,"text":string}],"supersedes":[existing_fact_id],"valid_from":optional_RFC3339,"valid_to":optional_RFC3339}. Use stable semantic snake_case keys specific to subject and project; reuse an existing key only for the same attribute. Supersede only facts explicitly corrected or replaced, never merely similar facts. Every supersedes entry must be an exact id from existing_facts, and that existing fact's key and subject must exactly equal the emitted fact's key and subject. For a correction of the same attribute, preserve the existing key and subject verbatim; never generate a new key or subject while referencing its old id. If there is no supported matching replacement, use an empty supersedes array. Quotes must be exact spans from user or tool sources.
Optional impression has content, subject, confidence, importance, decay_factor, quotes and describes only supported observations, not fixed personality labels.
Each item is {"key":string,"type":string,"title":string,"status":string,"subject":string,"content":string,"confidence":number,"importance":number,"decay_factor":number,"quotes":[{"source_id":string,"text":string}]}. All these item fields are required, including subject and scores for completed or cancelled tasks. Subject identifies the person or project the assertion concerns; do not return an empty subject or content. If the subject cannot be supported, omit that item. Type is task,event,procedure,entity,agent_action,artifact. A future plan is a task, never a completed event. Entities additionally have entity_kind and canonical_name. Do not invent tool calls or artifact IDs. An artifact may include artifact_id only when supplied in an artifact source. Optionally return relations:[{source_key,target_key,type,weight}] between your emitted fact/item keys; allowed types are used_tool,produced_artifact,mentions_entity,depends_on,resulted_in,supports. Weight 0..1. Never copy credentials, tokens, passwords or raw debug payloads into memories or quotes. Confidence 0..1, importance 0.5..2, decay_factor 0.5..3; these ranges apply to every fact, impression and item, with no exceptions for task status. At most 16 facts and 12 items per response.
When sources contain source_ref and actor identifiers, every item/fact/impression must include subject_scope (actor or workspace) and subject_actor_ids. Use exact supplied author_actor_id or mention_actor_ids for actor subjects. A quoted statement belongs to its author, not the quoting speaker. Unknown authors stay unknown: omit unsupported actor claims. Workspace habits use subject_scope=workspace and an empty subject_actor_ids array. Never use the owner as an implicit subject or infer an actor identity from a display name.`

func (s *Service) AnalyzeConversation(ctx context.Context, input Input) (Result, error) {
	user := firstNonEmpty(input.UserID, input.Conversation.UserID)
	conversation := firstNonEmpty(input.ConversationID, input.Conversation.ConversationID)
	if user == "" || conversation == "" {
		return Result{}, errors.New("analysis requires owner and conversation")
	}
	if err := validateInput(input); err != nil {
		return Result{}, err
	}
	sources := Sources(input.Conversation)
	batches, err := sourceBatches(sources)
	if err != nil {
		return Result{}, err
	}
	existing := []map[string]any{}
	providedFacts := []domain.MemoryDocument{}
	existingBytes := 2
	existingTruncated := false
	for _, fact := range input.ExistingFacts {
		if fact.UserID != "" && fact.UserID != user {
			return Result{}, errors.New("foreign existing fact in analysis input")
		}
		if fact.Metadata["is_superseded"] == true || metadataString(fact.Metadata, "superseded_by_id") != "" {
			continue
		}
		candidate := map[string]any{"id": fact.ID, "content": fact.Content, "key": factKeyFor(fact), "subject": metadataString(fact.Metadata, "subject"), "category": metadataString(fact.Metadata, "category"), "valid_from": fact.Metadata["valid_from"], "valid_to": fact.Metadata["valid_to"]}
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return Result{}, err
		}
		if existingBytes+len(encoded)+1 > 32*1024 {
			existingTruncated = true
			continue
		}
		existing = append(existing, candidate)
		providedFacts = append(providedFacts, fact)
		existingBytes += len(encoded) + 1
	}
	relationCount := 0
	result := Result{Facts: []domain.MemoryDocument{}, GenericItems: []domain.MemoryDocument{}}
	seen := map[string]bool{}
	policy := normalizeMemoryWritePolicy(input.WritePolicy)
	for _, batch := range batches {
		nodeByKey := map[string]string{}
		omittedKeys := map[string]bool{}
		var extracted Extraction
		payload := map[string]any{"sources": batch, "summary": input.Summary.Content, "existing_facts": existing, "existing_facts_truncated": existingTruncated}
		if err := s.generate(ctx, input, "analysis.extract", "analysis_model", extractionInstruction, payload, &extracted); err != nil {
			return Result{}, err
		}
		relationCount += len(extracted.Relations)
		if relationCount > 64 {
			return Result{}, errors.New("too many LLM relations")
		}
		if extracted.Facts == nil || extracted.Items == nil || len(extracted.Facts) > 16 || len(extracted.Items) > 12 {
			return Result{}, errors.New("invalid LLM analysis collections")
		}
		for _, fact := range extracted.Facts {
			if !validKey(fact.Key) || !validText(fact.Category, 80) {
				return Result{}, errors.New("invalid LLM fact identity")
			}
			doc, err := documentFromItem(fact.Item, domain.MemoryTypeFact, user, conversation, sources)
			if err != nil {
				return Result{}, err
			}
			doc.Metadata["category"] = fact.Category
			doc.Metadata["fact_key"] = fact.Key
			// An empty model decision is explicit: storage must not infer a
			// replacement merely because another fact shares the semantic key.
			doc.Metadata["supersedes_fact_ids"] = append([]string{}, fact.Supersedes...)
			for _, date := range []string{fact.ValidFrom, fact.ValidTo} {
				if date != "" {
					if _, err := time.Parse(time.RFC3339, date); err != nil {
						return Result{}, errors.New("invalid LLM fact validity timestamp")
					}
				}
			}
			if fact.ValidFrom != "" {
				doc.Metadata["valid_from"] = fact.ValidFrom
			}
			if fact.ValidTo != "" {
				doc.Metadata["valid_to"] = fact.ValidTo
			}
			if fact.ValidFrom != "" && fact.ValidTo != "" {
				from, _ := time.Parse(time.RFC3339, fact.ValidFrom)
				to, _ := time.Parse(time.RFC3339, fact.ValidTo)
				if !to.After(from) {
					return Result{}, errors.New("invalid LLM fact validity interval")
				}
			}
			doc = applyFactLineageMetadata(doc, user, conversation)
			doc.ID = FactRevisionDocumentID(conversation, fact.Key, metadataString(doc.Metadata, "lineage_revision_key"))
			doc.NodeID = FactRevisionNodeID(conversation, fact.Key, metadataString(doc.Metadata, "lineage_revision_key"))
			if err := validateSupersedes(fact, providedFacts); err != nil {
				return Result{}, err
			}
			if len(fact.Supersedes) > 0 {
				doc.Metadata["supersedes_fact_ids"] = fact.Supersedes
			}
			if previous := nodeByKey[fact.Key]; previous != "" && previous != doc.NodeID {
				return Result{}, errors.New("duplicate LLM fact key")
			}
			nodeByKey[fact.Key] = doc.NodeID
			if !seen[doc.ID] {
				result.Facts = append(result.Facts, doc)
				seen[doc.ID] = true
			}
		}
		if extracted.Impression != nil && result.Impression.ID == "" {
			doc, err := documentFromItem(*extracted.Impression, domain.MemoryTypeImpression, user, conversation, sources)
			if err != nil {
				return Result{}, err
			}
			doc.ID = ImpressionDocumentID(conversation)
			doc.NodeID = ImpressionNodeID(conversation)
			result.Impression = doc
			nodeByKey["impression"] = doc.NodeID
		}
		for _, item := range extracted.Items {
			if !domain.IsGenericMemoryType(item.Type) || !validKey(item.Key) || !validText(item.Title, 240) || !validText(item.Status, 40) {
				return Result{}, errors.New("invalid LLM generic memory fields")
			}
			if !policy.EnableGenericItems || len(result.GenericItems) >= policy.MaxGenericItems {
				omittedKeys[item.Key] = true
				continue
			}
			doc, err := documentFromItem(item.Item, item.Type, user, conversation, sources)
			if err != nil {
				return Result{}, err
			}
			doc.Metadata["title"] = item.Title
			doc.Metadata["status"] = item.Status
			doc.Metadata["item_type"] = string(item.Type)
			doc.Metadata["review_status"] = "auto"
			seed := genericItemSeed(item.Type, conversation, item.Content, doc.Metadata)
			doc.ID = GenericMemoryDocumentID(user, item.Type, conversation, seed)
			doc.NodeID = GenericMemoryNodeID(user, item.Type, conversation, seed)
			if item.Type == domain.MemoryTypeEntity {
				if !validText(item.CanonicalName, 240) || !validText(item.EntityKind, 80) {
					return Result{}, errors.New("invalid LLM entity identity")
				}
				doc.Metadata["title"] = item.CanonicalName
				doc.Metadata["entity_kind"] = item.EntityKind
				doc.Metadata["entity_scope"] = metadataString(input.Conversation.Metadata, "entity_scope")
				if len(doc.SourceRefs) > 0 || security.WritePartition(ctx) != "" {
					actors := append([]string{}, doc.SubjectActorIDs...)
					slices.Sort(actors)
					scopeData, _ := json.Marshal([]any{security.WorkspaceID(ctx, user), security.WritePartition(ctx), actors})
					doc.Metadata["entity_scope"] = domain.AccessID("subject-scope", string(scopeData))
				}

				key := canonicalEntityKey(doc.Content, doc.Metadata)
				doc.Metadata["canonical_key"] = key
				doc.Metadata["aliases"] = []string{item.Title}
				doc.Metadata["mention_conversation_ids"] = []string{conversation}
				doc.ID = CanonicalEntityDocumentID(user, key)
				doc.NodeID = CanonicalEntityNodeID(user, key)
			}
			if item.ArtifactID != "" {
				valid := false
				for _, source := range sources {
					if source.ID == item.ArtifactID && source.Kind == "artifact" {
						valid = true
					}
				}
				if !valid {
					return Result{}, errors.New("LLM artifact references unknown source")
				}
				doc.Metadata["artifact_id"] = item.ArtifactID
			}
			if previous := nodeByKey[item.Key]; previous != "" && previous != doc.NodeID {
				return Result{}, errors.New("duplicate LLM document key")
			}
			nodeByKey[item.Key] = doc.NodeID
			if !seen[doc.ID] {
				result.GenericItems = append(result.GenericItems, doc)
				seen[doc.ID] = true
			}
		}
		for _, relation := range extracted.Relations {
			if omittedKeys[relation.SourceKey] || omittedKeys[relation.TargetKey] {
				continue
			}
			src, dst := nodeByKey[relation.SourceKey], nodeByKey[relation.TargetKey]
			if src == "" || dst == "" || src == dst || !finiteRange(relation.Weight, 0, 1) {
				return Result{}, errors.New("invalid LLM relation endpoints")
			}
			switch relation.Type {
			case domain.MemoryRelationUsedTool, domain.MemoryRelationProducedArtifact, domain.MemoryRelationMentionsEntity, domain.MemoryRelationDependsOn, domain.MemoryRelationResultedIn, domain.MemoryRelationSupports:
			default:
				return Result{}, errors.New("invalid LLM relation type")
			}
			result.Relations = append(result.Relations, domain.MemoryRelation{UserID: user, SrcNodeID: src, DstNodeID: dst, RelationType: relation.Type, Weight: relation.Weight, Source: "llm", Metadata: map[string]any{"conversation_id": conversation}})
		}
	}

	if len(result.Facts) > 0 {
		result.Fact = result.Facts[0]
	}
	all := append(append([]domain.MemoryDocument{}, result.Facts...), result.GenericItems...)
	if result.Impression.ID != "" {
		all = append(all, result.Impression)
	}
	for _, doc := range all {
		doc.Metadata["source_conversation_revision"] = input.Conversation.Revision
	}
	if input.Summary.NodeID != "" {
		for _, doc := range all {
			result.Relations = append(result.Relations, domain.MemoryRelation{UserID: user, SrcNodeID: input.Summary.NodeID, DstNodeID: doc.NodeID, RelationType: domain.MemoryRelationDerivedFrom, Weight: 1, Source: "llm", Metadata: map[string]any{"conversation_id": conversation}})
		}
	}

	return result, nil
}

func documentFromItem(item Item, kind domain.MemoryType, user, conversation string, sources []Source) (domain.MemoryDocument, error) {
	if !validText(item.Content, 8000) {
		return domain.MemoryDocument{}, errors.New("invalid LLM memory content: requires 1..8000 UTF-8 bytes")
	}
	if !validText(item.Subject, 240) {
		return domain.MemoryDocument{}, errors.New("invalid LLM memory subject: requires 1..240 UTF-8 bytes")
	}
	if !finiteRange(item.Importance, .5, 2) {
		return domain.MemoryDocument{}, errors.New("invalid LLM memory importance: requires 0.5..2")
	}
	if !finiteRange(item.DecayFactor, .5, 3) {
		return domain.MemoryDocument{}, errors.New("invalid LLM memory decay_factor: requires 0.5..3")
	}
	if !finiteRange(item.Confidence, 0, 1) {
		return domain.MemoryDocument{}, errors.New("invalid LLM memory confidence: requires 0..1")
	}
	if len(item.Quotes) > 16 {
		return domain.MemoryDocument{}, errors.New("invalid LLM memory quotes: at most 16")
	}
	doc := domain.MemoryDocument{SubjectActorIDs: append([]string{}, item.SubjectActorIDs...), UserID: user, ConversationID: conversation, Type: kind, Content: strings.TrimSpace(item.Content), Importance: item.Importance, DecayFactor: item.DecayFactor, Metadata: map[string]any{"subject": item.Subject, "confidence": item.Confidence, "extraction_method": "llm", "score_reason": "llm_extracted", "support_kind": "model_inference", "source": "llm"}}
	quotes := []any{}
	parts := []string{}
	eventSources := false
	supportedActors := map[string]bool{}
	for _, quote := range item.Quotes {
		found := false
		for _, source := range sources {
			if source.ID != quote.SourceID {
				continue
			}
			if source.Role != domain.RoleUser && source.Role != domain.RoleTool {
				return domain.MemoryDocument{}, errors.New("LLM quote cannot use assistant or system assertion as evidence")
			}
			start := strings.Index(source.Text, quote.Text)
			if !validText(quote.Text, 8000) || start < 0 {
				return domain.MemoryDocument{}, errors.New("LLM quote is not an exact source span")
			}
			if source.Ref != nil {
				eventSources = true
				ref := *source.Ref
				offset := 0
				if ref.Span != nil {
					offset = ref.Span.Start
				}
				ref.Span = &domain.TextSpan{Start: offset + utf8.RuneCountInString(source.Text[:start]), End: offset + utf8.RuneCountInString(source.Text[:start+len(quote.Text)])}
				doc.SourceRefs = append(doc.SourceRefs, ref)
				doc.WorkspaceID = ref.WorkspaceID
				if source.AuthorActorID != "" {
					supportedActors[source.AuthorActorID] = true
				}
				for _, actor := range source.MentionActorIDs {
					supportedActors[actor] = true
				}
			}
			raw := fmt.Sprintf("%s|%s|%s|%d|%s|%d|%d", user, conversation, source.ID, source.SequenceNum, source.PartID, start, start+len(quote.Text))
			sum := sha256.Sum256([]byte(raw))
			support := "model_inference"
			if doc.Content == quote.Text {
				support = "user_statement"
				if source.Role == domain.RoleTool {
					support = "tool_observation"
				}
				doc.Metadata["support_kind"] = support
			}
			artifactID := ""
			if source.Kind == "artifact" {
				artifactID = source.ID
			}
			quotes = append(quotes, map[string]any{"part_id": source.PartID, "artifact_id": artifactID, "sequence_num": source.SequenceNum, "quote": quote.Text, "start_byte": start, "end_byte": start + len(quote.Text), "support_kind": support, "source_id": hex.EncodeToString(sum[:])})
			if source.PartID != "" {
				parts = append(parts, source.PartID)
			}
			found = true
			break
		}
		if !found {
			return domain.MemoryDocument{}, errors.New("LLM quote references unknown source")
		}
	}
	if len(quotes) == 0 {
		return domain.MemoryDocument{}, errors.New("LLM memory requires original source evidence")
	}
	if eventSources {
		if item.SubjectScope == "workspace" {
			if len(item.SubjectActorIDs) != 0 {
				return domain.MemoryDocument{}, errors.New("workspace subject cannot include actor IDs")
			}
		} else if item.SubjectScope == "actor" && len(item.SubjectActorIDs) > 0 {
			seenActors := []string{}
			for _, actor := range item.SubjectActorIDs {
				if !supportedActors[actor] || slices.Contains(seenActors, actor) {
					return domain.MemoryDocument{}, errors.New("unsupported LLM subject actor")
				}
				seenActors = append(seenActors, actor)
			}
		} else {
			return domain.MemoryDocument{}, errors.New("event memory requires explicit supported subject scope")
		}
		doc.Metadata["subject_scope"] = item.SubjectScope
	}
	doc.Metadata["source_quotes"] = quotes
	doc.Metadata["source_part_ids"] = parts
	return doc, nil
}

func validateSupersedes(fact Fact, existing []domain.MemoryDocument) error {
	for _, id := range fact.Supersedes {
		found := false
		for _, old := range existing {
			if old.ID == id && old.Type == domain.MemoryTypeFact && factKeyFor(old) == fact.Key && metadataString(old.Metadata, "subject") == fact.Subject && metadataString(old.Metadata, "superseded_by_id") == "" {
				found = true
				break
			}
		}
		if !found {
			return errors.New("LLM supersedes references an unrelated or missing fact")
		}
	}
	return nil
}

func validScore(importance, decay float64) bool {
	return finiteRange(importance, .5, 2) && finiteRange(decay, .5, 3)
}

func finiteRange(v, min, max float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= min && v <= max
}

func validText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && utf8.ValidString(s)
}

func validKey(s string) bool {
	if len(s) == 0 || len(s) > 120 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

func validateInput(input Input) error {
	user := firstNonEmpty(input.UserID, input.Conversation.UserID)
	conversation := firstNonEmpty(input.ConversationID, input.Conversation.ConversationID)
	if user == "" || conversation == "" || (input.Conversation.UserID != "" && input.Conversation.UserID != user) || (input.Conversation.ConversationID != "" && input.Conversation.ConversationID != conversation) {
		return errors.New("memory source scope mismatch")
	}
	for _, part := range input.Conversation.Parts {
		if (part.UserID != "" && part.UserID != user) || (part.ConversationID != "" && part.ConversationID != conversation) {
			return errors.New("memory source part scope mismatch")
		}
	}
	for _, artifact := range input.Conversation.Artifacts {
		if (artifact.UserID != "" && artifact.UserID != user) || (artifact.ConversationID != "" && artifact.ConversationID != conversation) {
			return errors.New("memory artifact scope mismatch")
		}
	}
	return nil
}
