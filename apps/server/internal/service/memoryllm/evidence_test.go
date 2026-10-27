package memoryllm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type evidenceGeneratorFunc func(context.Context, Request) (json.RawMessage, error)

func (f evidenceGeneratorFunc) Generate(ctx context.Context, r Request) (json.RawMessage, error) {
	return f(ctx, r)
}

func TestEvidenceReadingChecksVerbatimSupportAndKeepsInference(t *testing.T) {
	gen := evidenceGeneratorFunc(func(_ context.Context, req Request) (json.RawMessage, error) {
		input, ok := req.Input.(map[string]any)
		if !ok || len(input) != 2 || input["question"] != "What is likely?" || input["memories"] != "A: I finished the interviews. B: I am planning to move." || req.Role != "analysis_model" || req.Task != "evidence.read" || req.MaxOutputTokens != 4096 {
			t.Fatal("unexpected reader boundary", req)
		}
		return json.RawMessage(`{"coverage":[{"aspect":"likelihood","items":[{"statement":"A likely completed interviews","quotes":["A: I finished the interviews."],"inferred":true}],"gap":"No stated future plan for A."}],"answerable":true,"answer":"A likely finished the interviews, but future plans are unknown."}`), nil
	})
	got, err := ReadEvidence(context.Background(), gen, "owner", "What is likely?", "A: I finished the interviews. B: I am planning to move.", 4096)
	if err != nil || !got.Coverage[0].Items[0].Inferred || !*got.Answerable {
		t.Fatal(got, err)
	}
}

func TestEvidenceReadingRejectsInventedQuotesAndMalformedLedger(t *testing.T) {
	valid := `{"coverage":[{"aspect":"event","items":[{"statement":"planned","quotes":["I plan to hike."],"inferred":false}],"gap":""}],"answerable":true,"answer":"A hike is planned."}`
	for _, raw := range []string{
		strings.ReplaceAll(valid, "I plan to hike.", "I completed the hike."),
		strings.Replace(valid, `"quotes":["I plan to hike."]`, `"quotes":[]`, 1),
		strings.Replace(valid, `"answerable":true,`, "", 1),
		`{"coverage":[{"aspect":"event","items":[],"gap":""}],"answerable":false,"answer":"Unknown."}`,
		`{"coverage":[{"aspect":"event","items":[],"gap":"Missing"}],"answerable":true,"answer":"Hike."}`,
	} {
		gen := evidenceGeneratorFunc(func(context.Context, Request) (json.RawMessage, error) { return json.RawMessage(raw), nil })
		if _, err := ReadEvidence(context.Background(), gen, "owner", "event?", "I plan to hike.", 4096); !errors.Is(err, ErrInvalidEvidenceReading) {
			t.Fatal("accepted unsupported or malformed reading", raw, err)
		}
	}
}

func TestEvidenceReadingAllowsHonestEmptyEvidenceAndNoImplicitRetry(t *testing.T) {
	calls := 0
	gen := evidenceGeneratorFunc(func(context.Context, Request) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"coverage":[{"aspect":"event","items":[],"gap":"No supplied evidence"}],"answerable":false,"answer":"I do not know."}`), nil
	})
	if _, err := ReadEvidence(context.Background(), gen, "owner", "event?", "", 4096); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadEvidence(ctx, gen, "owner", "event?", "", 4096); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancelled reader called model", err, calls)
	}
	if _, err := ReadEvidence(context.Background(), gen, "owner", "event?", strings.Repeat("x", 256001), 4096); err == nil || calls != 1 {
		t.Fatal("oversized evidence called model", err, calls)
	}
}
