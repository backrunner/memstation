package domain

import (
	"encoding/json"
	"strings"
	"time"
)

// Project existing business records into the independent service contract.
// This is a structural mapping; it performs no semantic extraction or inference.
func ProjectMemoryDocument(document MemoryDocument) MemoryDocument {
	if kind, ok := document.Metadata["kind"].(string); ok && kind != "" {
		document.Kind = kind
	}
	if document.Kind == "" {
		document.Kind = string(document.Type)
	}
	document.Visibility = "workspace"
	if document.PartitionID != "" {
		document.Visibility = "private"
	}
	readTime := func(value any) *time.Time {
		switch v := value.(type) {
		case time.Time:
			return &v
		case *time.Time:
			return v
		case string:
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return &t
			}
		}
		return nil
	}
	if document.ValidFrom == nil {
		document.ValidFrom = readTime(document.Metadata["valid_from"])
	}
	if document.ValidTo == nil {
		document.ValidTo = readTime(document.Metadata["valid_to"])
	}
	return document
}

// Processing checkpoints are storage internals, not recalled memory metadata.
func (document MemoryDocument) MarshalJSON() ([]byte, error) {
	type publicDocument MemoryDocument
	value := publicDocument(ProjectMemoryDocument(document))
	value.Metadata = PublicMemoryMetadata(document.Metadata)
	return json.Marshal(value)
}

func PublicMemoryMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	public := make(map[string]any, len(metadata))
	for key, item := range metadata {
		if key == "_access" || key == "trusted_source" || strings.HasPrefix(key, "_llm_") {
			continue
		}
		public[key] = item
	}
	return public
}
