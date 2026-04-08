package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

type LongMemEvalGranularity string

const (
	LongMemEvalGranularitySession LongMemEvalGranularity = "session"
	LongMemEvalGranularityTurn    LongMemEvalGranularity = "turn"
)

type LongMemEvalOptions struct {
	Granularity LongMemEvalGranularity `json:"granularity,omitempty"`
}

type longMemEvalSample struct {
	QuestionID         string              `json:"question_id"`
	QuestionType       string              `json:"question_type"`
	Question           string              `json:"question"`
	Answer             any                 `json:"answer"`
	QuestionDate       any                 `json:"question_date"`
	HaystackSessionIDs []any               `json:"haystack_session_ids"`
	HaystackDates      []any               `json:"haystack_dates"`
	HaystackSessions   [][]longMemEvalTurn `json:"haystack_sessions"`
	AnswerSessionIDs   []any               `json:"answer_session_ids"`
}

type longMemEvalTurn struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	HasAnswer bool   `json:"has_answer,omitempty"`
}

func LoadLongMemEvalDataset(path string, options LongMemEvalOptions) (Dataset, error) {
	options, err := normalizeLongMemEvalOptions(options)
	if err != nil {
		return Dataset{}, err
	}
	samples, err := loadLongMemEvalSamples(path)
	if err != nil {
		return Dataset{}, err
	}

	dataset, err := buildLongMemEvalDataset(samples, options)
	if err != nil {
		return Dataset{}, err
	}
	if err := dataset.Validate(); err != nil {
		return Dataset{}, err
	}
	return dataset, nil
}

func loadLongMemEvalSamples(path string) ([]longMemEvalSample, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var samples []longMemEvalSample
	if err := json.Unmarshal(raw, &samples); err != nil {
		return nil, err
	}
	return samples, nil
}

func normalizeLongMemEvalOptions(options LongMemEvalOptions) (LongMemEvalOptions, error) {
	if options.Granularity == "" {
		options.Granularity = LongMemEvalGranularitySession
	}
	switch options.Granularity {
	case LongMemEvalGranularitySession, LongMemEvalGranularityTurn:
		return options, nil
	default:
		return LongMemEvalOptions{}, fmt.Errorf("unsupported LongMemEval granularity %q", options.Granularity)
	}
}

func buildLongMemEvalDataset(samples []longMemEvalSample, options LongMemEvalOptions) (Dataset, error) {
	dataset := Dataset{
		Name: fmt.Sprintf("longmemeval-external-%s", options.Granularity),
		Description: fmt.Sprintf(
			"External LongMemEval retrieval benchmark adapted for MemStation retrieval/ranking evaluation (%s granularity, abstention excluded).",
			options.Granularity,
		),
		Documents: make([]DatasetDocument, 0),
		Queries:   make([]QueryCase, 0),
	}

	for _, sample := range samples {
		if shouldSkipLongMemEvalSample(sample) {
			continue
		}
		userID := longMemEvalUserID(sample.QuestionID)
		documents, positives, err := buildLongMemEvalDocuments(sample, userID, options)
		if err != nil {
			return Dataset{}, fmt.Errorf("build LongMemEval sample %s: %w", sample.QuestionID, err)
		}
		if len(documents) == 0 || len(positives) == 0 {
			continue
		}

		dataset.Documents = append(dataset.Documents, documents...)

		graded := make(map[string]float64, len(positives))
		for _, id := range positives {
			graded[id] = 1
		}

		dataset.Queries = append(dataset.Queries, QueryCase{
			CaseID:          fmt.Sprintf("longmemeval.%s.%s", sample.QuestionID, options.Granularity),
			UserID:          userID,
			Query:           strings.TrimSpace(sample.Question),
			TopN:            longMemEvalDefaultTopN(options.Granularity),
			MemoryTypes:     []domain.MemoryType{domain.MemoryTypeSegment},
			Positives:       positives,
			GradedRelevance: graded,
			Metadata: map[string]any{
				"question_id":   sample.QuestionID,
				"question_type": strings.TrimSpace(sample.QuestionType),
				"question_date": normalizeAnyString(sample.QuestionDate),
				"answer":        normalizeAnyString(sample.Answer),
				"granularity":   string(options.Granularity),
			},
			Notes: fmt.Sprintf("LongMemEval question %s (%s)", sample.QuestionID, sample.QuestionType),
		})
	}

	sort.Slice(dataset.Documents, func(i, j int) bool {
		return dataset.Documents[i].ID < dataset.Documents[j].ID
	})
	sort.Slice(dataset.Queries, func(i, j int) bool {
		return dataset.Queries[i].CaseID < dataset.Queries[j].CaseID
	})
	return dataset, nil
}

func buildLongMemEvalDocuments(sample longMemEvalSample, userID string, options LongMemEvalOptions) ([]DatasetDocument, []string, error) {
	switch options.Granularity {
	case LongMemEvalGranularitySession:
		documents, positives := buildLongMemEvalSessionDocuments(sample, userID)
		return documents, positives, nil
	case LongMemEvalGranularityTurn:
		documents, positives := buildLongMemEvalTurnDocuments(sample, userID)
		return documents, positives, nil
	default:
		return nil, nil, fmt.Errorf("unsupported LongMemEval granularity %q", options.Granularity)
	}
}

func buildLongMemEvalSessionDocuments(sample longMemEvalSample, userID string) ([]DatasetDocument, []string) {
	questionTime := longMemEvalTimeOrDefault(sample.QuestionDate, time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	answerSessions := longMemEvalStringSet(sample.AnswerSessionIDs)
	questionID := sanitizeExternalID(sample.QuestionID)
	documents := make([]DatasetDocument, 0, len(sample.HaystackSessions))
	positives := make([]string, 0, len(answerSessions))

	for idx, session := range sample.HaystackSessions {
		sessionID := longMemEvalSessionID(sample, idx)
		content := renderLongMemEvalSession(session)
		if content == "" {
			continue
		}
		sessionTime := longMemEvalSessionTime(sample, idx, questionTime)
		sanitizedSessionID := sanitizeExternalID(sessionID)
		docID := fmt.Sprintf("longmemeval-doc-%s-session-%s", questionID, sanitizedSessionID)
		documents = append(documents, DatasetDocument{
			ID:          docID,
			NodeID:      fmt.Sprintf("longmemeval-node-%s-session-%s", questionID, sanitizedSessionID),
			UserID:      userID,
			Type:        domain.MemoryTypeSegment,
			Content:     content,
			Importance:  1,
			DecayFactor: 1,
			Metadata: map[string]any{
				"question_id":   sample.QuestionID,
				"question_type": sample.QuestionType,
				"session_id":    sessionID,
				"session_index": idx + 1,
				"granularity":   string(LongMemEvalGranularitySession),
			},
			CreatedAt: sessionTime,
			UpdatedAt: sessionTime,
		})
		if _, ok := answerSessions[sessionID]; ok {
			positives = append(positives, docID)
		}
	}

	return documents, uniqueStrings(positives)
}

func buildLongMemEvalTurnDocuments(sample longMemEvalSample, userID string) ([]DatasetDocument, []string) {
	questionTime := longMemEvalTimeOrDefault(sample.QuestionDate, time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	answerSessions := longMemEvalStringSet(sample.AnswerSessionIDs)
	questionID := sanitizeExternalID(sample.QuestionID)
	documents := make([]DatasetDocument, 0)
	positives := make([]string, 0)
	fallbackPositives := make([]string, 0)

	for sessionIdx, session := range sample.HaystackSessions {
		sessionID := longMemEvalSessionID(sample, sessionIdx)
		sessionTime := longMemEvalSessionTime(sample, sessionIdx, questionTime)
		sanitizedSessionID := sanitizeExternalID(sessionID)
		sessionMarked := false
		for turnIdx, turn := range session {
			content := strings.TrimSpace(turn.Content)
			if content == "" {
				continue
			}
			role := strings.TrimSpace(turn.Role)
			if role == "" {
				role = "unknown"
			}
			docID := fmt.Sprintf("longmemeval-doc-%s-turn-%s-%03d", questionID, sanitizedSessionID, turnIdx+1)
			documents = append(documents, DatasetDocument{
				ID:          docID,
				NodeID:      fmt.Sprintf("longmemeval-node-%s-turn-%s-%03d", questionID, sanitizedSessionID, turnIdx+1),
				UserID:      userID,
				Type:        domain.MemoryTypeSegment,
				Content:     fmt.Sprintf("%s: %s", role, content),
				Importance:  1,
				DecayFactor: 1,
				Metadata: map[string]any{
					"question_id":   sample.QuestionID,
					"question_type": sample.QuestionType,
					"session_id":    sessionID,
					"session_index": sessionIdx + 1,
					"turn_index":    turnIdx + 1,
					"has_answer":    turn.HasAnswer,
					"granularity":   string(LongMemEvalGranularityTurn),
				},
				CreatedAt: sessionTime,
				UpdatedAt: sessionTime,
			})
			if turn.HasAnswer {
				positives = append(positives, docID)
				sessionMarked = true
			}
			if _, ok := answerSessions[sessionID]; ok {
				fallbackPositives = append(fallbackPositives, docID)
			}
		}
		if sessionMarked {
			continue
		}
	}

	if len(positives) == 0 {
		positives = fallbackPositives
	}
	return documents, uniqueStrings(positives)
}

func shouldSkipLongMemEvalSample(sample longMemEvalSample) bool {
	questionID := strings.TrimSpace(sample.QuestionID)
	if questionID == "" {
		return true
	}
	if strings.HasSuffix(strings.ToLower(questionID), "_abs") {
		return true
	}
	return strings.TrimSpace(sample.Question) == ""
}

func longMemEvalDefaultTopN(granularity LongMemEvalGranularity) int {
	if granularity == LongMemEvalGranularityTurn {
		return 20
	}
	return 10
}

func longMemEvalUserID(questionID string) string {
	return "longmemeval-" + sanitizeExternalID(questionID)
}

func longMemEvalSessionID(sample longMemEvalSample, idx int) string {
	if idx >= 0 && idx < len(sample.HaystackSessionIDs) {
		if value := normalizeAnyString(sample.HaystackSessionIDs[idx]); value != "" {
			return value
		}
	}
	return fmt.Sprintf("session-%03d", idx+1)
}

func longMemEvalSessionTime(sample longMemEvalSample, idx int, fallback time.Time) time.Time {
	if idx >= 0 && idx < len(sample.HaystackDates) {
		return longMemEvalTimeOrDefault(sample.HaystackDates[idx], fallback)
	}
	return fallback
}

func renderLongMemEvalSession(session []longMemEvalTurn) string {
	lines := make([]string, 0, len(session))
	for _, turn := range session {
		content := strings.TrimSpace(turn.Content)
		if content == "" {
			continue
		}
		role := strings.TrimSpace(turn.Role)
		if role == "" {
			role = "unknown"
		}
		lines = append(lines, fmt.Sprintf("%s: %s", role, content))
	}
	return strings.Join(lines, "\n")
}

func longMemEvalStringSet(values []any) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := normalizeAnyString(value)
		if normalized == "" {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

func normalizeAnyString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strings.TrimSpace(strconv.FormatFloat(typed, 'f', -1, 64))
	case int:
		return strconv.Itoa(typed)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func longMemEvalTimeOrDefault(value any, fallback time.Time) time.Time {
	raw := normalizeAnyString(value)
	if raw == "" {
		return fallback
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.DateOnly,
		time.DateTime,
		"2006-01-02 15:04",
		"2006/01/02",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"Jan 2 2006",
		"Jan 2, 2006",
		"January 2 2006",
		"January 2, 2006",
	} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed.UTC()
		}
	}
	return fallback
}

func sanitizeExternalID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(" ", "-", "/", "-", ":", "-", ".", "-", "_", "-", "\n", "-", "\t", "-")
	value = replacer.Replace(value)
	value = strings.Trim(value, "-")
	if value == "" {
		return "unknown"
	}
	return value
}
