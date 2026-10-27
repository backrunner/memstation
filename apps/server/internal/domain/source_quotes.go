package domain

import "encoding/json"

// SourceQuoteMessageSequences returns zero-based message positions. Explicit
// provenance includes an explicit base; missing provenance is never inferred.
func SourceQuoteMessageSequences(metadata map[string]any) []int {
	raw, _ := json.Marshal(metadata["source_quotes"])
	var quotes []struct {
		Sequence *int `json:"sequence_num"`
		Base     *int `json:"sequence_base"`
	}
	var out []int
	if json.Unmarshal(raw, &quotes) != nil {
		return out
	}
	for _, q := range quotes {
		if q.Sequence == nil || q.Base == nil {
			continue
		}
		base := *q.Base
		if (base == 0 || base == 1) && *q.Sequence >= base {
			out = append(out, *q.Sequence-base)
		}
	}
	return out
}
