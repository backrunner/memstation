package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	conversationservice "memstation/apps/server/internal/service/conversation"
)

type Scheduler struct {
	logger        *slog.Logger
	relay         *Relay
	conversations *conversationservice.Service
	mu            sync.RWMutex
	timeoutWindow time.Duration
}

func NewScheduler(logger *slog.Logger, relay *Relay, conversations *conversationservice.Service, timeoutWindow time.Duration) *Scheduler {
	return &Scheduler{
		logger:        logger,
		relay:         relay,
		conversations: conversations,
		timeoutWindow: timeoutWindow,
	}
}

func (s *Scheduler) UpdateTimeoutWindow(timeoutWindow time.Duration) {
	s.mu.Lock()
	s.timeoutWindow = timeoutWindow
	s.mu.Unlock()
}

func (s *Scheduler) Run(ctx context.Context) error {
	relayTicker := time.NewTicker(2 * time.Second)
	timeoutTicker := time.NewTicker(1 * time.Minute)
	defer relayTicker.Stop()
	defer timeoutTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-relayTicker.C:
			if err := s.relay.Flush(ctx); err != nil {
				s.logger.ErrorContext(ctx, "outbox relay flush failed", "error", err)
			}
		case <-timeoutTicker.C:
			timeoutWindow := s.currentTimeoutWindow()
			if s.conversations == nil || timeoutWindow <= 0 {
				s.logger.DebugContext(ctx, "conversation timeout scan skipped")
				continue
			}
			if err := s.conversations.FinalizeTimedOut(ctx, time.Now().UTC().Add(-timeoutWindow)); err != nil {
				s.logger.ErrorContext(ctx, "conversation timeout scan failed", "error", err)
			}
		}
	}
}

func (s *Scheduler) currentTimeoutWindow() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.timeoutWindow
}
