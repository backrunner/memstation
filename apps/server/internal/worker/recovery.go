package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
	analysisservice "memstation/apps/server/internal/service/analysis"
	sqlstore "memstation/apps/server/internal/store/sql"
	vectorstore "memstation/apps/server/internal/store/vector"
)

type RecoveryOptions struct {
	Conversations     int           `json:"conversations"`
	WorkerConcurrency int           `json:"worker_concurrency"`
	MessagesPerTurn   int           `json:"messages_per_turn"`
	QueueBuffer       int           `json:"queue_buffer"`
	Timeout           time.Duration `json:"timeout"`
}

type RecoveryReport struct {
	GeneratedAt              time.Time                    `json:"generated_at"`
	Conversations            int                          `json:"conversations"`
	WorkerConcurrency        int                          `json:"worker_concurrency"`
	MessagesPerTurn          int                          `json:"messages_per_turn"`
	TotalTasks               int                          `json:"total_tasks"`
	PeakBacklog              int                          `json:"peak_backlog"`
	DurationMillis           float64                      `json:"duration_millis"`
	ThroughputTasksPerSecond float64                      `json:"throughput_tasks_per_second"`
	EnqueuedByType           map[string]int               `json:"enqueued_by_type"`
	ProcessedByType          map[string]int               `json:"processed_by_type"`
	TaskTimings              map[string]TaskTimingSummary `json:"task_timings"`
	PendingOutbox            int                          `json:"pending_outbox"`
	CompletedConversations   int                          `json:"completed_conversations"`
	SummaryCompleted         int                          `json:"summary_completed"`
	AnalysisCompleted        int                          `json:"analysis_completed"`
	SummaryDocuments         int                          `json:"summary_documents"`
	FactDocuments            int                          `json:"fact_documents"`
	ImpressionDocuments      int                          `json:"impression_documents"`
	GenericDocuments         int                          `json:"generic_documents"`
	KeyMemories              int                          `json:"key_memories"`
}

type TaskTimingSummary struct {
	Count      int     `json:"count"`
	P50Millis  float64 `json:"p50_millis"`
	P95Millis  float64 `json:"p95_millis"`
	MeanMillis float64 `json:"mean_millis"`
	MaxMillis  float64 `json:"max_millis"`
}

type recoveryQueue struct {
	mu              sync.Mutex
	channel         chan domain.TaskMessage
	wait            sync.WaitGroup
	currentDepth    int
	peakDepth       int
	enqueuedByType  map[string]int
	processedByType map[string]int
	timingsByType   map[string][]float64
}

func RunRecoveryDrill(ctx context.Context, options RecoveryOptions) (RecoveryReport, error) {
	if options.Conversations <= 0 {
		options.Conversations = 64
	}
	if options.WorkerConcurrency <= 0 {
		options.WorkerConcurrency = 8
	}
	if options.MessagesPerTurn <= 0 {
		options.MessagesPerTurn = 3
	}
	if options.QueueBuffer <= 0 {
		options.QueueBuffer = maxInt(options.Conversations*8, 128)
	}
	if options.Timeout <= 0 {
		options.Timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	conversations := sqlstore.NewInMemoryConversationRepository()
	memories := sqlstore.NewInMemoryMemoryRepository()
	vectors := vectorstore.NewPGVectorStore(nil)
	outbox := sqlstore.NewInMemoryOutboxRepository()
	queue := newRecoveryQueue(options.QueueBuffer)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	services := Services{
		Conversations:  conversations,
		Memories:       memories,
		Vectors:        vectors,
		Publisher:      queue,
		Analysis:       analysisservice.NewService(),
		EmbeddingModel: "benchmark-recovery",
		AllowedKeyMemoryKeys: map[string]struct{}{
			"recent_conversation": {},
		},
	}
	relay := NewRelay(logger, outbox, queue)
	handlers := map[string]func(context.Context, *asynq.Task) error{
		TypeConversationFinalize: handleConversationFinalize(logger, services),
		TypeSummaryGenerate:      handleSummaryGenerate(logger, services),
		TypeEmbeddingGenerate:    handleEmbeddingGenerate(logger, services),
		TypeAnalysisExtract:      handleAnalysisExtract(logger, services),
		TypeKeyMemoryReconcile:   handleKeyMemoryReconcile(logger, services),
	}

	start := time.Now()
	userIDs, err := seedRecoveryFixtures(ctx, conversations, outbox, options)
	if err != nil {
		return RecoveryReport{}, err
	}
	if err := relay.Flush(ctx); err != nil {
		return RecoveryReport{}, err
	}
	var firstErr error
	var errMu sync.Mutex
	workers := sync.WaitGroup{}
	for idx := 0; idx < options.WorkerConcurrency; idx++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for task := range queue.channel {
				queue.markDequeued()
				handler, ok := handlers[task.Type]
				if !ok {
					queue.wait.Done()
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("no handler registered for task %s", task.Type)
					}
					errMu.Unlock()
					continue
				}
				asynqTask, err := recoveryTask(task)
				if err != nil {
					queue.wait.Done()
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					continue
				}
				taskStart := time.Now()
				if err := handler(ctx, asynqTask); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
				}
				queue.recordCompletion(task.Type, time.Since(taskStart))
				queue.wait.Done()
			}
		}()
	}

	waitDone := make(chan struct{})
	go func() {
		queue.wait.Wait()
		close(queue.channel)
		close(waitDone)
	}()

	select {
	case <-ctx.Done():
		return RecoveryReport{}, fmt.Errorf("worker recovery drill timed out after %s", options.Timeout)
	case <-waitDone:
	}
	workers.Wait()
	duration := time.Since(start)
	if firstErr != nil {
		return RecoveryReport{}, firstErr
	}

	pending, err := outbox.Pending(ctx, options.Conversations*8)
	if err != nil {
		return RecoveryReport{}, err
	}
	completedConversations, summaryCompleted, analysisCompleted, err := countRecoveryConversations(ctx, conversations, userIDs)
	if err != nil {
		return RecoveryReport{}, err
	}
	summaryDocs := countRecoveryDocuments(ctx, memories, userIDs, domain.MemoryTypeSummary)
	factDocs := countRecoveryDocuments(ctx, memories, userIDs, domain.MemoryTypeFact)
	impressionDocs := countRecoveryDocuments(ctx, memories, userIDs, domain.MemoryTypeImpression)
	genericDocs := countRecoveryDocuments(ctx, memories, userIDs,
		domain.MemoryTypeEvent,
		domain.MemoryTypeTask,
		domain.MemoryTypeProcedure,
		domain.MemoryTypeArtifact,
		domain.MemoryTypeEntity,
		domain.MemoryTypeAgentAction,
	)
	keyMemories := countRecoveryKeyMemories(ctx, memories, userIDs)

	report := RecoveryReport{
		GeneratedAt:            time.Now().UTC(),
		Conversations:          options.Conversations,
		WorkerConcurrency:      options.WorkerConcurrency,
		MessagesPerTurn:        options.MessagesPerTurn,
		TotalTasks:             queue.totalProcessed(),
		PeakBacklog:            queue.peakBacklog(),
		DurationMillis:         float64(duration.Microseconds()) / 1000,
		EnqueuedByType:         queue.copyCounts(queue.enqueuedByType),
		ProcessedByType:        queue.copyCounts(queue.processedByType),
		TaskTimings:            queue.timingSummary(),
		PendingOutbox:          len(pending),
		CompletedConversations: completedConversations,
		SummaryCompleted:       summaryCompleted,
		AnalysisCompleted:      analysisCompleted,
		SummaryDocuments:       summaryDocs,
		FactDocuments:          factDocs,
		ImpressionDocuments:    impressionDocs,
		GenericDocuments:       genericDocs,
		KeyMemories:            keyMemories,
	}
	if report.DurationMillis > 0 {
		report.ThroughputTasksPerSecond = float64(report.TotalTasks) / (report.DurationMillis / 1000)
	}
	return report, nil
}

func ValidateRecoveryReport(report RecoveryReport, maxDrain time.Duration) error {
	if report.PendingOutbox != 0 {
		return fmt.Errorf("expected pending outbox 0, got %d", report.PendingOutbox)
	}
	if report.CompletedConversations != report.Conversations {
		return fmt.Errorf("expected %d finalized conversations, got %d", report.Conversations, report.CompletedConversations)
	}
	if report.SummaryCompleted != report.Conversations {
		return fmt.Errorf("expected %d completed summaries, got %d", report.Conversations, report.SummaryCompleted)
	}
	if report.AnalysisCompleted != report.Conversations {
		return fmt.Errorf("expected %d completed analyses, got %d", report.Conversations, report.AnalysisCompleted)
	}
	if report.SummaryDocuments != report.Conversations {
		return fmt.Errorf("expected %d summary docs, got %d", report.Conversations, report.SummaryDocuments)
	}
	if report.FactDocuments < report.Conversations {
		return fmt.Errorf("expected at least %d fact docs, got %d", report.Conversations, report.FactDocuments)
	}
	if report.ImpressionDocuments != report.Conversations {
		return fmt.Errorf("expected %d impression docs, got %d", report.Conversations, report.ImpressionDocuments)
	}
	if report.GenericDocuments < report.Conversations {
		return fmt.Errorf("expected at least %d generic docs, got %d", report.Conversations, report.GenericDocuments)
	}
	if report.KeyMemories != report.Conversations {
		return fmt.Errorf("expected %d key memories, got %d", report.Conversations, report.KeyMemories)
	}
	if maxDrain > 0 && report.DurationMillis > float64(maxDrain.Microseconds())/1000 {
		return fmt.Errorf("recovery drill duration %.2fms exceeded %.2fms", report.DurationMillis, float64(maxDrain.Microseconds())/1000)
	}
	return nil
}

func seedRecoveryFixtures(ctx context.Context, conversations *sqlstore.InMemoryConversationRepository, outbox *sqlstore.InMemoryOutboxRepository, options RecoveryOptions) ([]string, error) {
	now := time.Now().UTC()
	userIDs := make([]string, 0, options.Conversations)
	for idx := 0; idx < options.Conversations; idx++ {
		userID := fmt.Sprintf("recovery-user-%03d", idx+1)
		conversationID := fmt.Sprintf("recovery-conv-%03d", idx+1)
		userIDs = append(userIDs, userID)
		messages := make([]domain.UnifiedMessage, 0, options.MessagesPerTurn*2)
		for turn := 0; turn < options.MessagesPerTurn; turn++ {
			messages = append(messages,
				domain.UnifiedMessage{Role: domain.RoleUser, Text: fmt.Sprintf("Plan rollout %d for service %d with checklist validation.", turn+1, idx+1)},
				domain.UnifiedMessage{Role: domain.RoleAssistant, Text: fmt.Sprintf("Captured rollout %d and the recovery checklist for service %d.", turn+1, idx+1)},
			)
		}
		if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
			ConversationID: conversationID,
			UserID:         userID,
			RequestFormat:  string(domain.ProtocolOpenAIChat),
			Provider:       domain.ProviderOpenAI,
			Mode:           domain.ModeInject,
			FinalizeStatus: domain.FinalizeStatusActive,
			SummaryStatus:  domain.ProcessingStatusPending,
			AnalysisStatus: domain.ProcessingStatusPending,
			Messages:       messages,
			CreatedAt:      now,
			UpdatedAt:      now,
		}); err != nil {
			return nil, err
		}
		if err := outbox.Enqueue(ctx, domain.OutboxEvent{
			ID:            TypeConversationFinalize + ":" + conversationID,
			UserID:        userID,
			EventType:     TypeConversationFinalize,
			AggregateType: "conversation",
			AggregateID:   conversationID,
			Payload: map[string]any{
				"conversation_id": conversationID,
				"user_id":         userID,
				"finalize_reason": "backlog_recovery_drill",
				"finalize_status": string(domain.FinalizeStatusEnded),
			},
			Status:      "pending",
			AvailableAt: now,
			CreatedAt:   now,
		}); err != nil {
			return nil, err
		}
	}
	return userIDs, nil
}

func recoveryTask(task domain.TaskMessage) (*asynq.Task, error) {
	raw, err := json.Marshal(task.Payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(task.Type, raw), nil
}

func newRecoveryQueue(size int) *recoveryQueue {
	return &recoveryQueue{
		channel:         make(chan domain.TaskMessage, size),
		enqueuedByType:  map[string]int{},
		processedByType: map[string]int{},
		timingsByType:   map[string][]float64{},
	}
}

func (q *recoveryQueue) Publish(ctx context.Context, task domain.TaskMessage) error {
	q.wait.Add(1)
	q.mu.Lock()
	q.currentDepth++
	if q.currentDepth > q.peakDepth {
		q.peakDepth = q.currentDepth
	}
	q.enqueuedByType[task.Type]++
	q.mu.Unlock()
	select {
	case q.channel <- task:
		return nil
	case <-ctx.Done():
		q.wait.Done()
		q.mu.Lock()
		q.currentDepth--
		q.mu.Unlock()
		return ctx.Err()
	}
}

func (q *recoveryQueue) markDequeued() {
	q.mu.Lock()
	if q.currentDepth > 0 {
		q.currentDepth--
	}
	q.mu.Unlock()
}

func (q *recoveryQueue) recordCompletion(taskType string, duration time.Duration) {
	q.mu.Lock()
	q.processedByType[taskType]++
	q.timingsByType[taskType] = append(q.timingsByType[taskType], float64(duration.Microseconds())/1000)
	q.mu.Unlock()
}

func (q *recoveryQueue) totalProcessed() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	total := 0
	for _, count := range q.processedByType {
		total += count
	}
	return total
}

func (q *recoveryQueue) peakBacklog() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.peakDepth
}

func (q *recoveryQueue) copyCounts(source map[string]int) map[string]int {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[string]int, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func (q *recoveryQueue) timingSummary() map[string]TaskTimingSummary {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[string]TaskTimingSummary, len(q.timingsByType))
	for taskType, values := range q.timingsByType {
		sorted := append([]float64{}, values...)
		sort.Float64s(sorted)
		mean := 0.0
		for _, value := range sorted {
			mean += value
		}
		if len(sorted) > 0 {
			mean /= float64(len(sorted))
		}
		out[taskType] = TaskTimingSummary{
			Count:      len(sorted),
			P50Millis:  percentile(sorted, 0.50),
			P95Millis:  percentile(sorted, 0.95),
			MeanMillis: mean,
			MaxMillis:  maxFloat(sorted),
		}
	}
	return out
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return values[0]
	}
	if p >= 1 {
		return values[len(values)-1]
	}
	index := p * float64(len(values)-1)
	lower := int(index)
	upper := lower + 1
	if upper >= len(values) {
		return values[lower]
	}
	weight := index - float64(lower)
	return values[lower] + (values[upper]-values[lower])*weight
}

func maxFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	maxValue := values[0]
	for _, value := range values[1:] {
		maxValue = math.Max(maxValue, value)
	}
	return maxValue
}

func countRecoveryConversations(ctx context.Context, conversations *sqlstore.InMemoryConversationRepository, userIDs []string) (int, int, int, error) {
	completedConversations := 0
	summaryCompleted := 0
	analysisCompleted := 0
	for _, userID := range userIDs {
		items, err := conversations.ListConversations(ctx, userID)
		if err != nil {
			return 0, 0, 0, err
		}
		for _, item := range items {
			if item.FinalizeStatus == domain.FinalizeStatusEnded {
				completedConversations++
			}
			if item.SummaryStatus == domain.ProcessingStatusCompleted {
				summaryCompleted++
			}
			if item.AnalysisStatus == domain.ProcessingStatusCompleted {
				analysisCompleted++
			}
		}
	}
	return completedConversations, summaryCompleted, analysisCompleted, nil
}

func countRecoveryDocuments(ctx context.Context, memories *sqlstore.InMemoryMemoryRepository, userIDs []string, memoryTypes ...domain.MemoryType) int {
	count := 0
	for _, userID := range userIDs {
		items, err := memories.ListDocuments(ctx, userID, memoryTypes, 128)
		if err == nil {
			count += len(items)
		}
	}
	return count
}

func countRecoveryKeyMemories(ctx context.Context, memories *sqlstore.InMemoryMemoryRepository, userIDs []string) int {
	count := 0
	for _, userID := range userIDs {
		items, err := memories.ListKeyMemories(ctx, userID, 10)
		if err == nil {
			count += len(items)
		}
	}
	return count
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
