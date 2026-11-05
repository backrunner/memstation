package domain

import (
	"testing"
	"time"
)

func TestRelationQueryTimeoutOverTheLimitIsCappedNotShortened(t *testing.T) {
	for given, want := range map[time.Duration]time.Duration{
		0:                       150 * time.Millisecond,
		50 * time.Millisecond:   50 * time.Millisecond,
		time.Second:             time.Second,
		1500 * time.Millisecond: time.Second,
		5 * time.Second:         time.Second,
	} {
		if got := (RelationQuery{Timeout: given}).Normalized().Timeout; got != want {
			t.Fatalf("timeout %s became %s, want %s", given, got, want)
		}
	}
}
