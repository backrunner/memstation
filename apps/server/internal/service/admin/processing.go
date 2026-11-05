package admin

import (
	"context"
	"strings"
	"time"

	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/analysis"
	"memstation/apps/server/internal/service/processing"
)

func (s *Service) SetProcessingPolicy(policy *processing.Service) { s.processingPolicy = policy }

func (s *Service) previewLightweight(ctx context.Context, input analysis.Input, now time.Time) (MemoryPreview, bool, error) {
	config := s.processingPolicy.Config()
	preview := MemoryPreview{ConversationID: input.ConversationID, SummaryStatus: "skipped", AnalysisStatus: "skipped", Facts: []map[string]any{}, GenericItems: []map[string]any{}, KeyMemories: []map[string]any{}, Metadata: map[string]any{"processing_strategy": config.Strategy, "processing_policy_id": config.Identity(), "semantic_status": "not_requested"}}
	windows, texts, media := analysis.LightweightWindows(input.Conversation)
	observations, err := s.processingPolicy.Extract(ctx, input.UserID, texts)
	if err != nil {
		return preview, false, err
	}
	seen := map[string]bool{}
	for i, observation := range observations {
		for _, span := range observation.Spans {
			span.Start += windows[i].Start
			span.End += windows[i].Start
			doc, err := analysis.LightweightDocument(input.UserID, input.ConversationID, windows[i].Source, span, config.Identity())
			if err != nil {
				return preview, false, err
			}
			if !seen[doc.ID] {
				preview.GenericItems = append(preview.GenericItems, previewGenericMemoryItemMap(doc, now))
				seen[doc.ID] = true
			}
		}
	}
	if media {
		observations = append(observations, processing.Observation{Supported: false, NeedsSemantic: 1})
	}
	// Include source content and user in the deterministic audit selection.
	promote, reason := s.processingPolicy.Promote(input.UserID+":"+security.WorkspaceID(ctx, input.UserID)+":"+input.ConversationID+":"+config.Identity()+":"+strings.Join(texts, "\x00"), observations)
	if promote && config.Strategy == "hybrid" {
		size := 0
		for _, source := range analysis.Sources(input.Conversation) {
			size += len(source.Text)
		}
		if size > config.MaxSemanticBytes {
			promote = false
			reason = "deferred_budget"
		}
	}
	preview.Metadata["semantic_status"] = reason
	preview.Metadata["extraction_method"] = "lightweight"
	return preview, promote, nil
}
