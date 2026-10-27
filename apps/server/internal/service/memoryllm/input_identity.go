package memoryllm

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Source coordinates remain private. Only request-local labels cross the model
// boundary; restoration happens before schema and source-grounding validation.
type inputIdentity struct {
	labels    map[string]string
	originals map[string]string
	counts    map[string]int
}

func (p *inputIdentity) label(kind, original string) string {
	if original == "" {
		return ""
	}
	key := kind + ":" + original
	if label, ok := p.labels[key]; ok {
		return label
	}
	p.counts[kind]++
	label := fmt.Sprintf("%s-%d", kind, p.counts[kind])
	p.labels[key], p.originals[label] = label, original
	return label
}

func projectInputIdentity(raw []byte, media []SourceMedia) ([]byte, []SourceMedia, *inputIdentity, error) {
	p := &inputIdentity{labels: map[string]string{}, originals: map[string]string{}, counts: map[string]int{}}
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, nil, nil, err
	}
	if object, ok := payload.(map[string]any); ok {
		sources, _ := object["sources"].([]any)
		for _, item := range sources {
			source, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := source["id"].(string); ok {
				source["id"] = p.label("source", id)
			}
			if id, ok := source["part_id"].(string); ok {
				source["part_id"] = p.label("part", id)
			}
			if ref, ok := source["source_ref"].(map[string]any); ok {
				delete(ref, "workspace_id")
				delete(ref, "message_id")
			}
			delete(source, "media_sha256")
		}
		if ids, ok := object["evidence_source_ids"].([]any); ok {
			for i, value := range ids {
				if id, ok := value.(string); ok {
					ids[i] = p.label("source", id)
				}
			}
		}
		for _, field := range []string{"existing_facts", "facts"} {
			if facts, ok := object[field].([]any); ok {
				for _, item := range facts {
					if fact, ok := item.(map[string]any); ok {
						if id, ok := fact["id"].(string); ok {
							fact["id"] = p.label("fact", id)
						}
					}
				}
			}
		}
	}
	prepared := append([]SourceMedia(nil), media...)
	for i := range prepared {
		prepared[i].SourceID = p.label("source", prepared[i].SourceID)
	}
	encoded, err := json.Marshal(payload)
	return encoded, prepared, p, err
}

func (p *inputIdentity) restore(raw []byte) ([]byte, error) {
	if len(p.originals) == 0 {
		return raw, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var visit func(any)
	visit = func(value any) {
		switch object := value.(type) {
		case map[string]any:
			for key, item := range object {
				switch key {
				case "source_id", "artifact_id":
					if id, ok := item.(string); ok {
						if original, found := p.originals[id]; found {
							object[key] = original
						}
					}
				case "supersedes", "source_ids", "source_document_ids":
					if ids, ok := item.([]any); ok {
						for i, value := range ids {
							if id, ok := value.(string); ok {
								if original, found := p.originals[id]; found {
									ids[i] = original
								}
							}
						}
					}
				default:
					visit(item)
				}
			}
		case []any:
			for _, item := range object {
				visit(item)
			}
		}
	}
	visit(value)
	return json.Marshal(value)
}
