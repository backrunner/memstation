package memorygeneration

import "memstation/apps/server/internal/domain"

func snapshotMessageMetadata(message domain.UnifiedMessage) map[string]any {
	out := map[string]any{}
	for k, v := range message.Metadata {
		out[k] = v
	}
	delete(out, "trusted_source")
	if message.Source != nil {
		out["trusted_source"] = message.Source
	}
	return out
}
