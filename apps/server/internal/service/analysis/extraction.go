package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

const extractionCorrectionHint = "Validate every fact, impression and item, not only episodes. Use only exact source IDs and verbatim spans from this batch's user or tool messages; assistant and system messages cannot support facts. Supply all required scores in their stated ranges. Optional fact validity dates must be RFC3339 timestamps with time zones, or omitted when not supported; valid_to must be after valid_from. Supersede only exact matching existing fact IDs. " + semanticEpisodeCorrectionHint

type extractionState struct {
	result                                    Result
	seen                                      map[string]bool
	relationCount, episodeCount, genericCount int
}

// A rejected attempt must not consume keys, limits or partially append memories.
func (s extractionState) clone() extractionState {
	s.seen = maps.Clone(s.seen)
	s.result.Facts = slices.Clone(s.result.Facts)
	s.result.GenericItems = slices.Clone(s.result.GenericItems)
	s.result.Relations = slices.Clone(s.result.Relations)
	return s
}

func applyExtractionBatch(ctx context.Context, input Input, user, conversation string, batch []Source, providedFacts []domain.MemoryDocument, policy MemoryWritePolicy, extracted Extraction, state *extractionState) error {
	result := &state.result
	seen := state.seen
	nodeByKey := map[string]string{}
	omittedKeys := map[string]bool{}
	state.relationCount += len(extracted.Relations)
	if state.relationCount > 64 {
		return errors.New("too many LLM relations")
	}
	if extracted.Facts == nil || extracted.Items == nil || len(extracted.Facts) > 16 || len(extracted.Items) > 12 {
		return errors.New("invalid LLM analysis collections")
	}
	for _, fact := range extracted.Facts {
		if !validKey(fact.Key) || !validText(fact.Category, 80) {
			return errors.New("invalid LLM fact identity")
		}
		doc, err := documentFromItem(fact.Item, domain.MemoryTypeFact, user, conversation, batch)
		if err != nil {
			return err
		}
		doc.Metadata["category"] = fact.Category
		doc.Metadata["fact_key"] = fact.Key
		// An empty model decision is explicit: storage must not infer a
		// replacement merely because another fact shares the semantic key.
		doc.Metadata["supersedes_fact_ids"] = append([]string{}, fact.Supersedes...)
		for _, date := range []string{fact.ValidFrom, fact.ValidTo} {
			if date != "" {
				if _, err := time.Parse(time.RFC3339, date); err != nil {
					return errors.New("invalid LLM fact validity timestamp")
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
				return errors.New("invalid LLM fact validity interval")
			}
		}
		doc = applyFactLineageMetadata(doc, user, conversation)
		doc.ID = FactRevisionDocumentID(conversation, fact.Key, metadataString(doc.Metadata, "lineage_revision_key"))
		doc.NodeID = FactRevisionNodeID(conversation, fact.Key, metadataString(doc.Metadata, "lineage_revision_key"))
		if err := validateSupersedes(fact, providedFacts); err != nil {
			return err
		}
		if len(fact.Supersedes) > 0 {
			doc.Metadata["supersedes_fact_ids"] = fact.Supersedes
		}
		if previous := nodeByKey[fact.Key]; previous != "" && previous != doc.NodeID {
			return errors.New("duplicate LLM fact key")
		}
		nodeByKey[fact.Key] = doc.NodeID
		if !seen[doc.ID] {
			result.Facts = append(result.Facts, doc)
			seen[doc.ID] = true
		}
	}
	if extracted.Impression != nil {
		doc, err := documentFromItem(*extracted.Impression, domain.MemoryTypeImpression, user, conversation, batch)
		if err != nil {
			return err
		}
		if result.Impression.ID == "" {
			doc.ID = ImpressionDocumentID(conversation)
			doc.NodeID = ImpressionNodeID(conversation)
			result.Impression = doc
			nodeByKey["impression"] = doc.NodeID
		} else {
			omittedKeys["impression"] = true
		}
	}
	for _, item := range extracted.Items {
		if !domain.IsGenericMemoryType(item.Type) || item.Type == domain.MemoryTypeEpisode || !validKey(item.Key) || !validText(item.Title, 240) || !validText(item.Status, 40) {
			return errors.New("invalid LLM generic memory fields")
		}
		doc, err := documentFromItem(item.Item, item.Type, user, conversation, batch)
		if err != nil {
			return err
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
				return errors.New("invalid LLM entity identity")
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
			for _, source := range batch {
				if source.ID == item.ArtifactID && source.Kind == "artifact" {
					valid = true
				}
			}
			if !valid {
				return errors.New("LLM artifact references unknown source")
			}
			doc.Metadata["artifact_id"] = item.ArtifactID
		}
		if !policy.EnableGenericItems || state.genericCount >= policy.MaxGenericItems {
			omittedKeys[item.Key] = true
			continue
		}
		if previous := nodeByKey[item.Key]; previous != "" && previous != doc.NodeID {
			return errors.New("duplicate LLM document key")
		}
		nodeByKey[item.Key] = doc.NodeID
		if !seen[doc.ID] {
			result.GenericItems = append(result.GenericItems, doc)
			state.genericCount++
			seen[doc.ID] = true
		}
	}
	if (!policy.EnableSemanticEpisodes && len(extracted.Episodes) > 0) || (policy.EnableSemanticEpisodes && extracted.Episodes == nil) || len(extracted.Episodes) > 4 {
		return errors.New("invalid semantic episode collection")
	}
	for _, episode := range extracted.Episodes {
		doc, err := episodeDocument(episode, user, conversation, batch)
		if err != nil {
			return err
		}
		if previous := nodeByKey[episode.Key]; previous != "" && previous != doc.NodeID {
			return errors.New("duplicate LLM episode key")
		}
		nodeByKey[episode.Key] = doc.NodeID
		if !seen[doc.ID] {
			state.episodeCount++
			if state.episodeCount > 24 {
				return errors.New("semantic episode processing budget exceeded")
			}
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
			return errors.New("invalid LLM relation endpoints")
		}
		switch relation.Type {
		case domain.MemoryRelationUsedTool, domain.MemoryRelationProducedArtifact, domain.MemoryRelationMentionsEntity, domain.MemoryRelationDependsOn, domain.MemoryRelationResultedIn, domain.MemoryRelationSupports:
		default:
			return errors.New("invalid LLM relation type")
		}
		result.Relations = append(result.Relations, domain.MemoryRelation{UserID: user, SrcNodeID: src, DstNodeID: dst, RelationType: relation.Type, Weight: relation.Weight, Source: "llm", Metadata: map[string]any{"conversation_id": conversation}})
	}
	return nil
}
