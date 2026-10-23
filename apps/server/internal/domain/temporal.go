package domain

import (
	"math"
	"strings"
	"time"
)

type TimeTier string

const (
	TimeTierActive    TimeTier = "active"
	TimeTierShortTerm TimeTier = "short_term"
	TimeTierLongTerm  TimeTier = "long_term"
	TimeTierArchived  TimeTier = "archived"
)

func ParseTimeTier(value string) TimeTier {
	switch TimeTier(strings.TrimSpace(strings.ToLower(value))) {
	case TimeTierActive:
		return TimeTierActive
	case TimeTierShortTerm:
		return TimeTierShortTerm
	case TimeTierLongTerm:
		return TimeTierLongTerm
	case TimeTierArchived:
		return TimeTierArchived
	default:
		return ""
	}
}

func ClampImportance(value float64) float64 {
	if value <= 0 {
		return 1
	}
	if value < 0.5 {
		return 0.5
	}
	if value > 2 {
		return 2
	}
	return value
}

func ClampDecayFactor(value float64) float64 {
	if value <= 0 {
		return 1
	}
	if value < 0.5 {
		return 0.5
	}
	if value > 3 {
		return 3
	}
	return value
}

func BaseWindowForMemoryType(memoryType MemoryType) time.Duration {
	switch memoryType {
	case MemoryTypeSummary:
		return 90 * 24 * time.Hour
	case MemoryTypeSegment:
		return 7 * 24 * time.Hour
	case MemoryTypeImpression:
		return 30 * 24 * time.Hour
	case MemoryTypeFact:
		return 60 * 24 * time.Hour
	case MemoryTypeKeyMemory:
		return 180 * 24 * time.Hour
	case MemoryTypeEvent:
		return 45 * 24 * time.Hour
	case MemoryTypeTask:
		return 21 * 24 * time.Hour
	case MemoryTypeProcedure:
		return 180 * 24 * time.Hour
	case MemoryTypeArtifact:
		return 120 * 24 * time.Hour
	case MemoryTypeEntity:
		return 180 * 24 * time.Hour
	case MemoryTypeAgentAction:
		return 30 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

func DeriveActivityAt(activityAt, updatedAt, createdAt time.Time) time.Time {
	switch {
	case !activityAt.IsZero():
		return activityAt.UTC()
	case !updatedAt.IsZero():
		return updatedAt.UTC()
	case !createdAt.IsZero():
		return createdAt.UTC()
	default:
		return time.Now().UTC()
	}
}

func ClassifyTimeTier(activityAt time.Time, now time.Time) TimeTier {
	if activityAt.IsZero() {
		return TimeTierShortTerm
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	age := now.Sub(activityAt.UTC())
	switch {
	case age <= 7*24*time.Hour:
		return TimeTierActive
	case age <= 30*24*time.Hour:
		return TimeTierShortTerm
	case age <= 180*24*time.Hour:
		return TimeTierLongTerm
	default:
		return TimeTierArchived
	}
}

func TimeDecay(memoryType MemoryType, activityAt time.Time, decayFactor, importance float64, now time.Time) float64 {
	activityAt = DeriveActivityAt(activityAt, time.Time{}, time.Time{})
	if now.IsZero() {
		now = time.Now().UTC()
	}
	window := BaseWindowForMemoryType(memoryType)
	if window <= 0 {
		window = 30 * 24 * time.Hour
	}
	age := now.Sub(activityAt)
	if age < 0 {
		age = 0
	}
	denominator := window.Seconds() * ClampDecayFactor(decayFactor) * ClampImportance(importance)
	if denominator <= 0 {
		denominator = window.Seconds()
	}
	value := math.Exp(-age.Seconds() / denominator)
	if value < 0.15 {
		return 0.15
	}
	if value > 1 {
		return 1
	}
	return value
}

func IsStaleMemory(doc MemoryDocument, now time.Time) bool {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if doc.Type == MemoryTypeFact {
		if metadataBool(doc.Metadata, "is_superseded") {
			return true
		}
		if metadataString(doc.Metadata, "superseded_by_id") != "" {
			return true
		}
		if validTo := metadataTime(doc.Metadata, "valid_to"); !validTo.IsZero() && validTo.Before(now) {
			return true
		}
	}
	return false
}

func metadataBool(metadata map[string]any, key string) bool {
	value, ok := metadata[key]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func StalePenalty(doc MemoryDocument, now time.Time) float64 {
	if IsStaleMemory(doc, now) {
		return 0.35
	}
	return 1
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}

func metadataTime(metadata map[string]any, key string) time.Time {
	value, ok := metadata[key]
	if !ok || value == nil {
		return time.Time{}
	}
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC()
	case *time.Time:
		if typed != nil {
			return typed.UTC()
		}
	case string:
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed)); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

// FactValidAt uses a half-open validity interval. Superseded today does not
// imply invalid at a historical instant.
func FactValidAt(doc MemoryDocument, at *time.Time) bool {
	if at == nil {
		return !IsStaleMemory(doc, time.Now().UTC())
	}
	from := metadataTime(doc.Metadata, "valid_from")
	if from.IsZero() {
		from = DeriveActivityAt(doc.ActivityAt, doc.UpdatedAt, doc.CreatedAt)
	}
	to := metadataTime(doc.Metadata, "valid_to")
	return !at.Before(from) && (to.IsZero() || at.Before(to))
}
