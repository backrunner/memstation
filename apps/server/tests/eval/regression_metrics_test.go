package eval

import (
	"fmt"
	"slices"
	"strings"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

// Post-retrieval diagnostics only. Annotation IDs never enter ingestion, search,
// evidence selection or reader prompts. Provenance overlap does not prove that
// the retrieved paraphrase retains every answer-bearing detail.
type regressionSourceRecall struct {
	Unit            string   `json:"unit"`
	Expected        []string `json:"expected"`
	Retrieved       []string `json:"retrieved"`
	Admitted        []string `json:"admitted"`
	RetrievedRecall float64  `json:"retrieved_recall"`
	AdmittedRecall  float64  `json:"admitted_recall"`
}

func regressionContext(docs []domain.MemoryDocument, budget int) (string, []domain.MemoryDocument, int) {
	var text strings.Builder
	admitted := []domain.MemoryDocument{}
	omitted := 0
	for _, doc := range docs {
		rendered := memoryservice.FormatEvidenceDocument(doc) + "\n\n"
		if text.Len()+len(rendered) > budget {
			omitted++
			continue
		}
		text.WriteString(rendered)
		admitted = append(admitted, doc)
	}
	return text.String(), admitted, omitted
}

func regressionMeasureSourceRecall(g regressionGroup, q regressionQuestion, owner string, retrieved, admitted []domain.MemoryDocument) *regressionSourceRecall {
	result := &regressionSourceRecall{}
	var refs func([]domain.MemoryDocument) []string
	if q.Locomo != nil && g.Locomo != nil {
		result.Unit = "annotated dialogue-turn provenance; not semantic completeness"
		result.Expected = uniqueStrings(q.Locomo.Evidence)
		refs = func(docs []domain.MemoryDocument) []string { return pipelineSourceRefs(docs, owner, *g.Locomo) }
	} else if g.LongMem != nil && !strings.HasSuffix(q.ID, "_abs") {
		result.Unit = "annotated source-session provenance; not semantic completeness"
		gold := longMemEvalStringSet(g.LongMem.AnswerSessionIDs)
		for id := range gold {
			result.Expected = append(result.Expected, id)
		}
		refs = func(docs []domain.MemoryDocument) []string {
			found := []string{}
			for _, doc := range docs {
				for i := range g.LongMem.HaystackSessions {
					if doc.ConversationID == fmt.Sprintf("%s-session-%03d", owner, i+1) {
						found = append(found, longMemEvalSessionID(*g.LongMem, i))
						break
					}
				}
			}
			return uniqueStrings(found)
		}
	} else {
		return nil
	}
	if len(result.Expected) == 0 {
		return nil
	}
	slices.Sort(result.Expected)
	selected, packed := asSet(refs(retrieved)), asSet(refs(admitted))
	for _, id := range result.Expected {
		if _, ok := selected[id]; ok {
			result.Retrieved = append(result.Retrieved, id)
		}
		if _, ok := packed[id]; ok {
			result.Admitted = append(result.Admitted, id)
		}
	}
	result.RetrievedRecall = float64(len(result.Retrieved)) / float64(len(result.Expected))
	result.AdmittedRecall = float64(len(result.Admitted)) / float64(len(result.Expected))
	return result
}
