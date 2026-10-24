package domain

import "encoding/json"

// Processing checkpoints are storage internals, not recalled memory metadata.
func (document MemoryDocument) MarshalJSON() ([]byte, error) {
	type publicDocument MemoryDocument
	value := publicDocument(document)
	value.Metadata = PublicMemoryMetadata(document.Metadata)
	return json.Marshal(value)
}

func PublicMemoryMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	public := make(map[string]any, len(metadata))
	for key, item := range metadata {
		if key == "_llm_summary_checkpoint" || key == "_llm_analysis_checkpoint" || key == "_llm_keymemory_checkpoint" {
			continue
		}
		public[key] = item
	}
	return public
}
