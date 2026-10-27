package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/service/memoryllm"
)

type LongMemEvalQAJudgeOptions struct {
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Temperature     *float64      `json:"temperature,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	Model           string        `json:"model,omitempty"`
	BaseURL         string        `json:"base_url,omitempty"`
	APIKey          string        `json:"-"`
	Timeout         time.Duration `json:"-"`
}

type LongMemEvalQAJudgeRunInfo struct {
	MaxOutputTokens int      `json:"max_output_tokens"`
	Temperature     *float64 `json:"temperature,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	Provider        string   `json:"provider"`
	Model           string   `json:"model"`
	BaseURL         string   `json:"base_url,omitempty"`
}

type LongMemEvalQAHypothesis struct {
	QuestionID string `json:"question_id"`
	Hypothesis string `json:"hypothesis"`
}

type LongMemEvalQAAutoEvalLabel struct {
	Model       string `json:"model"`
	Label       bool   `json:"label"`
	RawResponse string `json:"raw_response,omitempty"`
}

type LongMemEvalQAResult struct {
	QuestionID    string                     `json:"question_id"`
	QuestionType  string                     `json:"question_type"`
	Question      string                     `json:"question"`
	Answer        string                     `json:"answer"`
	Hypothesis    string                     `json:"hypothesis"`
	IsAbstention  bool                       `json:"is_abstention"`
	AutoEvalLabel LongMemEvalQAAutoEvalLabel `json:"autoeval_label"`
}

type LongMemEvalQAMetric struct {
	Accuracy float64 `json:"accuracy"`
	Count    int     `json:"count"`
}

type LongMemEvalQASummary struct {
	ReferenceCount       int                            `json:"reference_count"`
	Coverage             float64                        `json:"coverage"`
	MissingQuestionIDs   []string                       `json:"missing_question_ids,omitempty"`
	EvaluatedCount       int                            `json:"evaluated_count"`
	SkippedQuestionIDs   []string                       `json:"skipped_question_ids,omitempty"`
	OverallAccuracy      float64                        `json:"overall_accuracy"`
	TaskAveragedAccuracy float64                        `json:"task_averaged_accuracy"`
	AbstentionAccuracy   float64                        `json:"abstention_accuracy"`
	AbstentionCount      int                            `json:"abstention_count"`
	ByType               map[string]LongMemEvalQAMetric `json:"by_type"`
}

type LongMemEvalQAEvaluation struct {
	GeneratedAt time.Time                 `json:"generated_at"`
	Judge       LongMemEvalQAJudgeRunInfo `json:"judge"`
	Summary     LongMemEvalQASummary      `json:"summary"`
	Results     []LongMemEvalQAResult     `json:"results"`
}

type longMemEvalQAJudgeClient struct {
	client  *http.Client
	options LongMemEvalQAJudgeOptions
}

func normalizeLongMemEvalQAJudgeOptions(options LongMemEvalQAJudgeOptions) (LongMemEvalQAJudgeOptions, error) {
	if options.MaxOutputTokens == 0 {
		options.MaxOutputTokens = memoryllm.DefaultMaxOutputTokens
	}
	if options.MaxOutputTokens < 1 || options.MaxOutputTokens > memoryllm.MaxConfiguredOutputTokens {
		return options, fmt.Errorf("invalid output token ceiling")
	}
	options.Model = strings.TrimSpace(options.Model)
	options.BaseURL = strings.TrimSpace(options.BaseURL)
	options.APIKey = strings.TrimSpace(options.APIKey)
	options.ReasoningEffort = strings.TrimSpace(options.ReasoningEffort)
	if options.Temperature != nil && (math.IsNaN(*options.Temperature) || math.IsInf(*options.Temperature, 0) || *options.Temperature < 0 || *options.Temperature > 2) {
		return options, fmt.Errorf("invalid sampling temperature")
	}
	if options.Timeout <= 0 {
		options.Timeout = 60 * time.Second
	}
	switch {
	case options.Model == "":
		return LongMemEvalQAJudgeOptions{}, fmt.Errorf("LongMemEval QA auto-eval requires a judge model")
	case options.BaseURL == "":
		return LongMemEvalQAJudgeOptions{}, fmt.Errorf("LongMemEval QA auto-eval requires a judge base url")
	case options.APIKey == "":
		return LongMemEvalQAJudgeOptions{}, fmt.Errorf("LongMemEval QA auto-eval requires a judge api key")
	default:
		return options, nil
	}
}

func (o LongMemEvalQAJudgeOptions) outputTokenLimit() int {
	if o.MaxOutputTokens == 0 {
		return memoryllm.DefaultMaxOutputTokens
	}
	return o.MaxOutputTokens
}

func (o LongMemEvalQAJudgeOptions) runInfo() LongMemEvalQAJudgeRunInfo {
	baseURL := strings.TrimSpace(o.BaseURL)
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		baseURL = parsed.Scheme + "://" + parsed.Host
	}
	return LongMemEvalQAJudgeRunInfo{
		MaxOutputTokens: o.outputTokenLimit(),
		Temperature:     o.Temperature,
		ReasoningEffort: o.ReasoningEffort,
		Provider:        string(domain.ProviderOpenAICompatible),
		Model:           o.Model,
		BaseURL:         baseURL,
	}
}

func RunLongMemEvalQAAutoEval(ctx context.Context, referencePath, hypothesisPath string, judgeOptions LongMemEvalQAJudgeOptions) (LongMemEvalQAEvaluation, error) {
	normalizedJudge, err := normalizeLongMemEvalQAJudgeOptions(judgeOptions)
	if err != nil {
		return LongMemEvalQAEvaluation{}, err
	}
	references, err := loadLongMemEvalSamples(referencePath)
	if err != nil {
		return LongMemEvalQAEvaluation{}, err
	}
	hypotheses, err := loadLongMemEvalHypotheses(hypothesisPath)
	if err != nil {
		return LongMemEvalQAEvaluation{}, err
	}
	return evaluateLongMemEvalQAWithHypotheses(ctx, references, hypotheses, normalizedJudge)
}

func evaluateLongMemEvalQAWithHypotheses(ctx context.Context, references []longMemEvalSample, hypotheses []LongMemEvalQAHypothesis, judgeOptions LongMemEvalQAJudgeOptions) (LongMemEvalQAEvaluation, error) {
	judge, err := newLongMemEvalQAJudgeClient(judgeOptions)
	if err != nil {
		return LongMemEvalQAEvaluation{}, err
	}
	referenceByID := make(map[string]longMemEvalSample, len(references))
	for _, item := range references {
		id := strings.TrimSpace(item.QuestionID)
		if id == "" {
			return LongMemEvalQAEvaluation{}, fmt.Errorf("LongMemEval reference is missing question_id")
		}
		if _, duplicate := referenceByID[id]; duplicate {
			return LongMemEvalQAEvaluation{}, fmt.Errorf("LongMemEval reference repeats question_id %q", id)
		}
		referenceByID[id] = item
	}

	results := make([]LongMemEvalQAResult, 0, len(hypotheses))
	skipped := make([]string, 0)
	seenHypotheses := make(map[string]struct{}, len(hypotheses))
	for _, hypothesis := range hypotheses {
		questionID := strings.TrimSpace(hypothesis.QuestionID)
		if questionID == "" {
			return LongMemEvalQAEvaluation{}, fmt.Errorf("LongMemEval hypothesis is missing question_id")
		}
		if _, duplicate := seenHypotheses[questionID]; duplicate {
			return LongMemEvalQAEvaluation{}, fmt.Errorf("LongMemEval hypothesis repeats question_id %q", questionID)
		}
		seenHypotheses[questionID] = struct{}{}
	}
	// Validate the entire input before the first paid judge request.
	for _, hypothesis := range hypotheses {
		questionID := strings.TrimSpace(hypothesis.QuestionID)
		reference, ok := referenceByID[questionID]
		if !ok {
			skipped = append(skipped, questionID)
			continue
		}

		prompt, err := buildLongMemEvalQAPrompt(reference, strings.TrimSpace(hypothesis.Hypothesis))
		if err != nil {
			return LongMemEvalQAEvaluation{}, fmt.Errorf("build LongMemEval QA prompt for %s: %w", questionID, err)
		}
		rawResponse, label, err := judge.Evaluate(ctx, prompt)
		if err != nil {
			return LongMemEvalQAEvaluation{}, fmt.Errorf("evaluate LongMemEval QA prompt for %s: %w", questionID, err)
		}

		results = append(results, LongMemEvalQAResult{
			QuestionID:   questionID,
			QuestionType: strings.TrimSpace(reference.QuestionType),
			Question:     strings.TrimSpace(reference.Question),
			Answer:       normalizeAnyString(reference.Answer),
			Hypothesis:   strings.TrimSpace(hypothesis.Hypothesis),
			IsAbstention: strings.HasSuffix(strings.ToLower(questionID), "_abs"),
			AutoEvalLabel: LongMemEvalQAAutoEvalLabel{
				Model:       judgeOptions.Model,
				Label:       label,
				RawResponse: rawResponse,
			},
		})
	}

	summary := summarizeLongMemEvalQAResults(results, skipped)
	summary.ReferenceCount = len(referenceByID)
	if summary.ReferenceCount > 0 {
		summary.Coverage = float64(len(results)) / float64(summary.ReferenceCount)
	}
	for id := range referenceByID {
		if _, supplied := seenHypotheses[id]; !supplied {
			summary.MissingQuestionIDs = append(summary.MissingQuestionIDs, id)
		}
	}
	sort.Strings(summary.MissingQuestionIDs)
	return LongMemEvalQAEvaluation{
		GeneratedAt: time.Now().UTC(),
		Judge:       judgeOptions.runInfo(),
		Summary:     summary,
		Results:     results,
	}, nil
}

func newLongMemEvalQAJudgeClient(options LongMemEvalQAJudgeOptions) (*longMemEvalQAJudgeClient, error) {
	normalized, err := normalizeLongMemEvalQAJudgeOptions(options)
	if err != nil {
		return nil, err
	}
	return &longMemEvalQAJudgeClient{
		client:  &http.Client{Timeout: normalized.Timeout},
		options: normalized,
	}, nil
}

func (c *longMemEvalQAJudgeClient) Evaluate(ctx context.Context, prompt string) (string, bool, error) {
	payload := map[string]any{
		"model":    c.options.Model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"n":        1, "temperature": 0, "max_tokens": 10,
	}
	if c.options.ReasoningEffort != "" {
		payload["reasoning_effort"] = c.options.ReasoningEffort
	}
	if c.options.Temperature != nil {
		payload["temperature"] = *c.options.Temperature
	}
	raw, _, err := provider.DoJSONRequest(
		ctx,
		c.client,
		domain.ProviderOpenAICompatible,
		http.MethodPost,
		strings.TrimRight(c.options.BaseURL, "/")+"/chat/completions",
		map[string]string{
			"Authorization": "Bearer " + c.options.APIKey,
		},
		payload,
	)
	if err != nil {
		return "", false, err
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", false, err
	}
	if len(response.Choices) == 0 {
		return "", false, fmt.Errorf("judge returned no choices")
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	label, err := parseLongMemEvalJudgeLabel(content)
	if err != nil {
		return content, false, err
	}
	return content, label, nil
}

func buildLongMemEvalQAPrompt(reference longMemEvalSample, hypothesis string) (string, error) {
	questionType := strings.TrimSpace(reference.QuestionType)
	question := strings.TrimSpace(reference.Question)
	answer := normalizeAnyString(reference.Answer)
	isAbstention := strings.HasSuffix(strings.ToLower(strings.TrimSpace(reference.QuestionID)), "_abs")
	if isAbstention {
		return fmt.Sprintf(
			"I will give you an unanswerable question, an explanation, and a response from a model. Please answer yes if the model correctly identifies the question as unanswerable. The model could say that the information is incomplete, or some other information is given but the asked information is not.\n\nQuestion: %s\n\nExplanation: %s\n\nModel Response: %s\n\nDoes the model correctly identify the question as unanswerable? Answer yes or no only.",
			question,
			answer,
			hypothesis,
		), nil
	}

	switch questionType {
	case "single-session-user", "single-session-assistant", "multi-session":
		return fmt.Sprintf(
			"I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response is equivalent to the correct answer or contains all the intermediate steps to get the correct answer, you should also answer yes. If the response only contains a subset of the information required by the answer, answer no. \n\nQuestion: %s\n\nCorrect Answer: %s\n\nModel Response: %s\n\nIs the model response correct? Answer yes or no only.",
			question,
			answer,
			hypothesis,
		), nil
	case "temporal-reasoning":
		return fmt.Sprintf(
			"I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response is equivalent to the correct answer or contains all the intermediate steps to get the correct answer, you should also answer yes. If the response only contains a subset of the information required by the answer, answer no. In addition, do not penalize off-by-one errors for the number of days. If the question asks for the number of days/weeks/months, etc., and the model makes off-by-one errors (e.g., predicting 19 days when the answer is 18), the model's response is still correct. \n\nQuestion: %s\n\nCorrect Answer: %s\n\nModel Response: %s\n\nIs the model response correct? Answer yes or no only.",
			question,
			answer,
			hypothesis,
		), nil
	case "knowledge-update":
		return fmt.Sprintf(
			"I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response contains some previous information along with an updated answer, the response should be considered as correct as long as the updated answer is the required answer.\n\nQuestion: %s\n\nCorrect Answer: %s\n\nModel Response: %s\n\nIs the model response correct? Answer yes or no only.",
			question,
			answer,
			hypothesis,
		), nil
	case "single-session-preference":
		return fmt.Sprintf(
			"I will give you a question, a rubric for desired personalized response, and a response from a model. Please answer yes if the response satisfies the desired response. Otherwise, answer no. The model does not need to reflect all the points in the rubric. The response is correct as long as it recalls and utilizes the user's personal information correctly.\n\nQuestion: %s\n\nRubric: %s\n\nModel Response: %s\n\nIs the model response correct? Answer yes or no only.",
			question,
			answer,
			hypothesis,
		), nil
	default:
		return "", fmt.Errorf("unsupported LongMemEval question type %q", questionType)
	}
}

func summarizeLongMemEvalQAResults(results []LongMemEvalQAResult, skipped []string) LongMemEvalQASummary {
	typeAcc := map[string][]float64{
		"single-session-user":       {},
		"single-session-preference": {},
		"single-session-assistant":  {},
		"multi-session":             {},
		"temporal-reasoning":        {},
		"knowledge-update":          {},
	}
	overall := make([]float64, 0, len(results))
	abstention := make([]float64, 0)
	for _, result := range results {
		score := 0.0
		if result.AutoEvalLabel.Label {
			score = 1
		}
		overall = append(overall, score)
		if _, ok := typeAcc[result.QuestionType]; ok {
			typeAcc[result.QuestionType] = append(typeAcc[result.QuestionType], score)
		}
		if result.IsAbstention {
			abstention = append(abstention, score)
		}
	}

	byType := make(map[string]LongMemEvalQAMetric, len(typeAcc))
	taskScores := make([]float64, 0, len(typeAcc))
	for questionType, scores := range typeAcc {
		metric := LongMemEvalQAMetric{
			Accuracy: averageFloat64(scores),
			Count:    len(scores),
		}
		byType[questionType] = metric
		if len(scores) > 0 {
			taskScores = append(taskScores, metric.Accuracy)
		}
	}

	return LongMemEvalQASummary{
		EvaluatedCount:       len(results),
		SkippedQuestionIDs:   skipped,
		OverallAccuracy:      averageFloat64(overall),
		TaskAveragedAccuracy: averageFloat64(taskScores),
		AbstentionAccuracy:   averageFloat64(abstention),
		AbstentionCount:      len(abstention),
		ByType:               byType,
	}
}

func WriteLongMemEvalQAArtifacts(evaluation LongMemEvalQAEvaluation, outDir string) error {
	if strings.TrimSpace(outDir) == "" {
		return fmt.Errorf("output directory is required")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "qa_summary.json"), evaluation); err != nil {
		return err
	}
	if err := writeLongMemEvalQAResultsJSONL(filepath.Join(outDir, "qa_results.jsonl"), evaluation.Results); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "qa_report.md"), []byte(markdownLongMemEvalQAReport(evaluation)), 0o644)
}

func writeLongMemEvalQAResultsJSONL(path string, results []LongMemEvalQAResult) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, item := range results {
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(raw, '\n')); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func markdownLongMemEvalQAReport(evaluation LongMemEvalQAEvaluation) string {
	var builder strings.Builder
	builder.WriteString("# LongMemEval QA Auto-Eval\n\n")
	fmt.Fprintf(&builder, "- Generated at: `%s`\n", evaluation.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&builder, "- Judge provider: `%s`\n", evaluation.Judge.Provider)
	fmt.Fprintf(&builder, "- Judge model: `%s`\n", evaluation.Judge.Model)
	if evaluation.Judge.BaseURL != "" {
		fmt.Fprintf(&builder, "- Judge endpoint: `%s`\n", evaluation.Judge.BaseURL)
	}
	fmt.Fprintf(&builder, "- Evaluated questions: `%d`\n", evaluation.Summary.EvaluatedCount)
	fmt.Fprintf(&builder, "- Reference questions: `%d`; coverage: `%.4f`\n", evaluation.Summary.ReferenceCount, evaluation.Summary.Coverage)
	if len(evaluation.Summary.MissingQuestionIDs) > 0 {
		fmt.Fprintf(&builder, "- Missing hypotheses: `%s`\n", strings.Join(evaluation.Summary.MissingQuestionIDs, ", "))
	}
	if len(evaluation.Summary.SkippedQuestionIDs) > 0 {
		fmt.Fprintf(&builder, "- Skipped hypotheses: `%d`\n", len(evaluation.Summary.SkippedQuestionIDs))
	}
	builder.WriteString("\n## Summary\n\n")
	fmt.Fprintf(&builder, "- Overall accuracy: `%.4f`\n", evaluation.Summary.OverallAccuracy)
	fmt.Fprintf(&builder, "- Task-averaged accuracy: `%.4f`\n", evaluation.Summary.TaskAveragedAccuracy)
	fmt.Fprintf(&builder, "- Abstention accuracy: `%.4f` (`%d`)\n", evaluation.Summary.AbstentionAccuracy, evaluation.Summary.AbstentionCount)
	builder.WriteString("\n## By Question Type\n\n")
	builder.WriteString("| Question Type | Accuracy | Count |\n")
	builder.WriteString("| --- | ---: | ---: |\n")
	for _, questionType := range []string{
		"single-session-user",
		"single-session-preference",
		"single-session-assistant",
		"multi-session",
		"temporal-reasoning",
		"knowledge-update",
	} {
		metric := evaluation.Summary.ByType[questionType]
		fmt.Fprintf(&builder, "| `%s` | %.4f | %d |\n", questionType, metric.Accuracy, metric.Count)
	}
	return builder.String()
}

func loadLongMemEvalHypotheses(path string) ([]LongMemEvalQAHypothesis, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var asArray []LongMemEvalQAHypothesis
	if err := json.Unmarshal(raw, &asArray); err == nil {
		return compactLongMemEvalHypotheses(asArray), nil
	}

	lines := strings.Split(string(raw), "\n")
	out := make([]LongMemEvalQAHypothesis, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item LongMemEvalQAHypothesis
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("decode LongMemEval hypothesis line: %w", err)
		}
		out = append(out, item)
	}
	return compactLongMemEvalHypotheses(out), nil
}

func compactLongMemEvalHypotheses(values []LongMemEvalQAHypothesis) []LongMemEvalQAHypothesis {
	out := make([]LongMemEvalQAHypothesis, 0, len(values))
	for _, item := range values {
		if strings.TrimSpace(item.QuestionID) == "" {
			continue
		}
		out = append(out, LongMemEvalQAHypothesis{
			QuestionID: strings.TrimSpace(item.QuestionID),
			Hypothesis: strings.TrimSpace(item.Hypothesis),
		})
	}
	return out
}

func averageFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func parseLongMemEvalJudgeLabel(raw string) (bool, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return false, fmt.Errorf("judge returned an empty response")
	}
	fields := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '.' || r == ',' || r == ':' || r == ';' || r == '!' || r == '?'
	})
	if len(fields) == 0 {
		return false, fmt.Errorf("judge response %q does not contain a parseable label", raw)
	}
	switch fields[0] {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("judge response %q does not start with yes or no", raw)
	}
}
