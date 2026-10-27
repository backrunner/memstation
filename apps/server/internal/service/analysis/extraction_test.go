package analysis

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"memstation/apps/server/internal/service/memoryllm"
)

func TestAnalysisCorrectsRejectedFactsWithoutPartialMemories(t *testing.T) {
	for _, issue := range []string{"quote", "timestamp", "assistant"} {
		t.Run(issue, func(t *testing.T) {
			input := sourceInput()
			good := modelExtraction(t, input)
			var bad Extraction
			if err := json.Unmarshal(good, &bad); err != nil {
				t.Fatal(err)
			}
			// Make validation fail after an earlier valid fact was already converted.
			another := bad.Facts[0]
			another.Key = "second_fact"
			switch issue {
			case "quote":
				another.Quotes = []Quote{{SourceID: "invented-source", Text: "not present"}}
			case "timestamp":
				another.ValidFrom = "last Tuesday"
			case "assistant":
				another.Quotes = []Quote{{SourceID: "message-2", Text: input.Conversation.Messages[1].Text}}
			}
			bad.Facts = append(bad.Facts, another)
			rejected, _ := json.Marshal(bad)
			calls := 0
			var original any
			s := NewService(generatorFunc(func(_ context.Context, r memoryllm.Request) (json.RawMessage, error) {
				calls++
				if calls == 1 {
					original = r.Input
					return rejected, nil
				}
				if calls != 2 || !reflect.DeepEqual(original, r.Input) || !strings.Contains(r.Instruction, extractionCorrectionHint) {
					t.Fatal("correction changed the source or exceeded its bound")
				}
				return good, nil
			}))
			result, err := s.AnalyzeConversation(context.Background(), input)
			if err != nil || calls != 2 || len(result.Facts) != 1 || result.Facts[0].Metadata["fact_key"] != "test_fact" {
				t.Fatalf("rejected candidate affected result: %+v, %v, calls=%d", result, err, calls)
			}
		})
	}
}

func TestSummaryCorrectsInvalidScoresFromSameSources(t *testing.T) {
	calls := 0
	s := NewService(generatorFunc(func(_ context.Context, r memoryllm.Request) (json.RawMessage, error) {
		calls++
		score := "1.2"
		if calls == 1 {
			score = "99"
		}
		return json.RawMessage(`{"summary":"Supported summary","topic_label":"Project","keywords":[],"importance":` + score + `,"decay_factor":1.5}`), nil
	}))
	result, err := s.SummarizeConversation(context.Background(), sourceInput())
	if err != nil || calls != 2 || result.Importance != 1.2 {
		t.Fatal(result, err, calls)
	}
}
