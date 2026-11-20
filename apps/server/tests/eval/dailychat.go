package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"time"

	"memstation/apps/server/internal/domain"
)

// Daily-chat datasets are MemStation-native synthetic histories for the target
// scene: ordinary personal or group chat whose memory must answer by time,
// person, event and fact. Unlike LOCOMO they carry when and by whom each question
// is asked, so relative-time questions have a defined reference point.
const (
	PipelineDatasetLOCOMO    = "locomo"
	PipelineDatasetDailyChat = "daily_chat"
	dailyChatSchema          = "memstation-daily-chat-v1"
	// The reader receives the same timestamp rendering for sources and question.
	// Weekday and offset are derived from the instant; they add no new facts.
	dailyChatDateLayout = "Monday, 2 January 2006, 15:04 (UTC-07:00)"
)

// Primary axes map to stable integer categories so the existing per-category
// sampling and reports apply unchanged. Order is part of the contract.
var dailyChatAxes = []string{"time", "person", "event", "fact", "abstention"}

var dailyChatSkills = []string{
	"absolute_date", "relative_time", "ordering", "interval",
	"attribution", "relationship", "alias", "profile",
	"participants", "location", "outcome", "plan_vs_done", "cancellation",
	"update", "negation", "count", "aggregation",
	"assistant_not_evidence", "cross_lingual", "code_switch",
	"unanswerable", "near_miss",
}

var dailyChatLanguages = []string{"zh", "en", "ja", "ko", "mixed"}

type dailyChatDataset struct {
	Schema  string            `json:"schema"`
	Name    string            `json:"name"`
	Split   string            `json:"split"`
	Samples []dailyChatSample `json:"samples"`
}

type dailyChatSample struct {
	ID           string `json:"id"`
	Language     string `json:"language"`
	Scene        string `json:"scene"`
	OwnerSpeaker string `json:"owner_speaker"`
	// Required for mixed samples: the languages actually spoken in the history.
	SourceLanguages []string            `json:"source_languages"`
	Sessions        []dailyChatSession  `json:"sessions"`
	Questions       []dailyChatQuestion `json:"questions"`
}

type dailyChatSession struct {
	ID    string          `json:"id"`
	At    string          `json:"at"`
	Turns []dailyChatTurn `json:"turns"`
}

type dailyChatTurn struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

type dailyChatQuestion struct {
	ID       string   `json:"id"`
	AskedAt  string   `json:"asked_at"`
	AskedBy  string   `json:"asked_by"`
	Language string   `json:"language"`
	Axis     string   `json:"axis"`
	Skills   []string `json:"skills"`
	Query    string   `json:"query"`
	Answer   string   `json:"answer"`
	Evidence []string `json:"evidence"`
}

func dailyChatAxisCategory(axis string) int {
	return slices.Index(dailyChatAxes, axis) + 1
}

func loadPipelineSamplesForFormat(format, path, sampleID string, perCategory int, captions bool) ([]pipelineSample, error) {
	switch format {
	case "", PipelineDatasetLOCOMO:
		return loadPipelineSamplesWithCaptions(path, sampleID, perCategory, captions)
	case PipelineDatasetDailyChat:
		if captions {
			return nil, fmt.Errorf("daily-chat datasets have no image caption track")
		}
		return loadDailyChatSamples(path, sampleID, perCategory)
	default:
		return nil, fmt.Errorf("unknown dataset format %q", format)
	}
}

func loadDailyChatSamples(path, sampleID string, perCategory int) ([]pipelineSample, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var dataset dailyChatDataset
	if err = json.Unmarshal(raw, &dataset); err != nil {
		return nil, err
	}
	samples, err := buildDailyChatSamples(dataset)
	if err != nil {
		return nil, err
	}
	out := []pipelineSample{}
	for _, s := range samples {
		if sampleID != "" && s.ID != sampleID {
			continue
		}
		if perCategory > 0 {
			// Same ID-hash stratification as LOCOMO, by primary axis; sessions are kept whole.
			sort.Slice(s.Questions, func(i, j int) bool { return pipelineHash(s.Questions[i].ID) < pipelineHash(s.Questions[j].ID) })
			counts := map[int]int{}
			selected := []pipelineQuestion{}
			for _, q := range s.Questions {
				if counts[q.Category] < perCategory {
					selected = append(selected, q)
					counts[q.Category]++
				}
			}
			s.Questions = selected
			sort.Slice(s.Questions, func(i, j int) bool { return s.Questions[i].ID < s.Questions[j].ID })
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no matching samples")
	}
	return out, nil
}

// buildDailyChatSamples validates the whole file before any selection, so a
// malformed question cannot hide behind a sample or category filter.
func buildDailyChatSamples(dataset dailyChatDataset) ([]pipelineSample, error) {
	if dataset.Schema != dailyChatSchema {
		return nil, fmt.Errorf("daily-chat schema must be %s", dailyChatSchema)
	}
	if dataset.Split == "" || len(dataset.Samples) == 0 {
		return nil, fmt.Errorf("daily-chat dataset requires a split and samples")
	}
	out := []pipelineSample{}
	seen := map[string]bool{}
	for _, s := range dataset.Samples {
		if !safePipelineID(s.ID) || seen[s.ID] {
			return nil, fmt.Errorf("empty, unsafe or duplicate sample identity %q", s.ID)
		}
		seen[s.ID] = true
		sample, err := buildDailyChatSample(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.ID, err)
		}
		out = append(out, sample)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func buildDailyChatSample(s dailyChatSample) (pipelineSample, error) {
	p := pipelineSample{ID: s.ID}
	if !slices.Contains(dailyChatLanguages, s.Language) {
		return p, fmt.Errorf("unsupported sample language %q", s.Language)
	}
	if (s.Language == "mixed") != (len(s.SourceLanguages) > 1) {
		return p, fmt.Errorf("mixed samples list two or more source_languages; others list none")
	}
	for _, language := range s.SourceLanguages {
		if language == "mixed" || !slices.Contains(dailyChatLanguages, language) {
			return p, fmt.Errorf("unsupported source language %q", language)
		}
	}
	if s.Scene != "assistant_1on1" && s.Scene != "group_chat" {
		return p, fmt.Errorf("scene must be assistant_1on1 or group_chat")
	}
	if s.Scene == "assistant_1on1" && s.OwnerSpeaker == "" {
		return p, fmt.Errorf("assistant_1on1 requires owner_speaker")
	}
	roles := map[string]string{}
	sessionIDs := map[string]bool{}
	var last time.Time
	for _, raw := range s.Sessions {
		at, err := time.Parse(time.RFC3339, raw.At)
		if err != nil {
			return p, fmt.Errorf("session %s: %w", raw.ID, err)
		}
		if !isLOCOMOSessionKey(raw.ID) || sessionIDs[raw.ID] {
			return p, fmt.Errorf("invalid or duplicate session id %q", raw.ID)
		}
		sessionIDs[raw.ID] = true
		if !at.After(last) {
			return p, fmt.Errorf("sessions must be strictly chronological")
		}
		last = at
		date := at.Format(dailyChatDateLayout)
		session := pipelineSession{ID: raw.ID, Date: date, At: at}
		for _, turn := range raw.Turns {
			if turn.ID == "" || turn.Speaker == "" || turn.Text == "" || roles[turn.ID] != "" {
				return p, fmt.Errorf("invalid or duplicate turn %q", turn.ID)
			}
			role := domain.RoleUser
			switch turn.Role {
			case "user":
				if s.Scene == "assistant_1on1" && turn.Speaker != s.OwnerSpeaker {
					return p, fmt.Errorf("turn %s: user speaker must be the owner in a 1:1 chat", turn.ID)
				}
			case "assistant":
				if s.Scene != "assistant_1on1" {
					return p, fmt.Errorf("turn %s: group chats contain human turns only", turn.ID)
				}
				role = domain.RoleAssistant
			default:
				return p, fmt.Errorf("turn %s: role must be user or assistant", turn.ID)
			}
			roles[turn.ID] = turn.Role
			// Same rendering as the LOCOMO pipeline; dataset turn IDs stay in metadata
			// for offline citation mapping and never decide retrieval.
			session.Messages = append(session.Messages, domain.UnifiedMessage{Role: role, Metadata: map[string]any{"ref": turn.ID, "speaker": turn.Speaker}, Text: fmt.Sprintf("[Conversation date: %s] [Speaker: %s] %s", date, turn.Speaker, turn.Text)})
			session.Refs = append(session.Refs, turn.ID)
		}
		if len(session.Messages) == 0 {
			return p, fmt.Errorf("empty session %s", raw.ID)
		}
		p.Sessions = append(p.Sessions, session)
	}
	if len(p.Sessions) == 0 {
		return p, fmt.Errorf("no sessions")
	}
	questionIDs := map[string]bool{}
	for _, q := range s.Questions {
		question, err := buildDailyChatQuestion(s, q, roles, last)
		if err != nil {
			return p, fmt.Errorf("question %s: %w", q.ID, err)
		}
		if questionIDs[question.ID] {
			return p, fmt.Errorf("duplicate question %s", q.ID)
		}
		questionIDs[question.ID] = true
		p.Questions = append(p.Questions, question)
	}
	if len(p.Questions) == 0 {
		return p, fmt.Errorf("no questions")
	}
	sort.Slice(p.Questions, func(i, j int) bool { return p.Questions[i].ID < p.Questions[j].ID })
	return p, nil
}

func buildDailyChatQuestion(s dailyChatSample, q dailyChatQuestion, roles map[string]string, lastSession time.Time) (pipelineQuestion, error) {
	var out pipelineQuestion
	if !safePipelineID(q.ID) || q.Query == "" || q.Answer == "" {
		return out, fmt.Errorf("requires a safe id, query and reference answer")
	}
	askedAt, err := time.Parse(time.RFC3339, q.AskedAt)
	if err != nil {
		return out, err
	}
	// Every session is ingested before any question is answered, so a question
	// asked before the final session would be able to see its own future.
	if !askedAt.After(lastSession) {
		return out, fmt.Errorf("asked_at must follow the final session")
	}
	if s.Scene == "assistant_1on1" && q.AskedBy != s.OwnerSpeaker {
		return out, fmt.Errorf("1:1 questions are asked by the owner")
	}
	if !slices.Contains(dailyChatLanguages, q.Language) || q.Language == "mixed" {
		return out, fmt.Errorf("question language must be one concrete language")
	}
	category := dailyChatAxisCategory(q.Axis)
	if category == 0 {
		return out, fmt.Errorf("unknown axis %q", q.Axis)
	}
	if len(q.Skills) == 0 {
		return out, fmt.Errorf("at least one skill is required")
	}
	skills := map[string]bool{}
	for _, skill := range q.Skills {
		if !slices.Contains(dailyChatSkills, skill) || skills[skill] {
			return out, fmt.Errorf("unknown or duplicate skill %q", skill)
		}
		skills[skill] = true
	}
	spoken := s.SourceLanguages
	if len(spoken) == 0 {
		spoken = []string{s.Language}
	}
	crossLingual := !slices.Contains(spoken, q.Language)
	if crossLingual != skills["cross_lingual"] {
		return out, fmt.Errorf("cross_lingual skill must match the question/source language difference")
	}
	abstain := q.Axis == "abstention"
	if abstain != (skills["unanswerable"] || skills["near_miss"]) {
		return out, fmt.Errorf("abstention axis requires unanswerable or near_miss, and only it may use them")
	}
	if abstain != (len(q.Evidence) == 0) {
		return out, fmt.Errorf("answerable questions need evidence; abstentions have none")
	}
	seen := map[string]bool{}
	for _, ref := range q.Evidence {
		role, ok := roles[ref]
		if !ok || seen[ref] {
			return out, fmt.Errorf("unresolved or duplicate evidence %q", ref)
		}
		seen[ref] = true
		// Assistant text is context, not proof of a personal fact.
		if role != "user" {
			return out, fmt.Errorf("evidence %q cites an assistant turn", ref)
		}
	}
	out = pipelineQuestion{ID: s.ID + "." + q.ID, Query: q.Query, Answer: q.Answer, Category: category, Evidence: append([]string(nil), q.Evidence...), AskedAt: askedAt.Format(dailyChatDateLayout), AskedBy: q.AskedBy, Language: q.Language, Axis: q.Axis, Skills: append([]string(nil), q.Skills...)}
	return out, nil
}
