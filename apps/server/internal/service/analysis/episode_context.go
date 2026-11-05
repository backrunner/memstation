package analysis

import (
	"fmt"
	"slices"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
)

const (
	episodeContextBytes   = 1024
	episodeContextSources = 2
)

const episodeContextInstruction = ` Sources marked context_only=true are retained neighboring original text, supplied only to resolve an antecedent or clarify a newly reported event. Every emitted fact, impression, item or episode must cite at least one unmarked source (or an actually attached unmarked media source). Do not re-extract an old event supported only by context. When a new source uses a pronoun or omits an event name, cite both the new statement and the exact contextual antecedent if the linkage is supported; otherwise retain uncertainty. A repeated report is not proof of another event. Plans and subsequent outcomes remain separate observations with their original dates. Context spans can be partial; do not invent the omitted text.`

// Reserve space inside the existing episode budget, never append unbounded
// history. Each source byte is primary exactly once; overlap is support only.
func episodeSourceBatches(sources []Source) ([][]Source, error) {
	primary, err := sourceBatchesWithLimits(sources, episodeBatchBytes-episodeContextBytes, episodeBatchBytes-episodeContextBytes, episodeBatchSources-episodeContextSources)
	if err != nil {
		return nil, err
	}
	out := make([][]Source, 0, len(primary))
	ids := make(map[string]bool, len(sources))
	for _, source := range sources {
		ids[source.ID] = true
	}
	for index, batch := range primary {
		var context []Source
		if index > 0 {
			previous := primary[index-1]
			for i := len(previous) - 1; i >= 0 && len(context) < episodeContextSources; i-- {
				source := previous[i]
				// Never replay attachment payloads or jump across a missing attachment.
				if source.MediaSHA256 != "" || source.NativeMedia {
					break
				}
				source = episodeSourceSuffix(source, episodeContextBytes/episodeContextSources)
				source.ContextOnly = true
				// Distinguish two windows of one long source before opaque projection.
				id := fmt.Sprintf("context-window-%d-%d", index, i)
				for ids[id] {
					id += "-"
				}
				ids[id] = true
				source.ID = id
				context = append(context, source)
			}
			slices.Reverse(context)
		}
		out = append(out, append(context, batch...))
	}
	return out, nil
}

func episodeSourceSuffix(source Source, limit int) Source {
	start := max(0, len(source.Text)-limit)
	for start < len(source.Text) && !utf8.RuneStart(source.Text[start]) {
		start++
	}
	if source.Ref != nil {
		ref := *source.Ref
		offset := 0
		if ref.Span != nil {
			offset = ref.Span.Start
		}
		ref.Span = &domain.TextSpan{Start: offset + utf8.RuneCountInString(source.Text[:start]), End: offset + utf8.RuneCountInString(source.Text)}
		source.Ref = &ref
	}
	source.ByteOffset += start
	source.Text = source.Text[start:]
	return source
}
