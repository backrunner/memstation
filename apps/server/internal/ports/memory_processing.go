package ports

import "context"

// SummaryCheckpointRepository updates only private processing metadata. It must
// preserve the document body, edit version, and embedding state.
type SummaryCheckpointRepository interface {
	StoreSummaryCheckpoint(ctx context.Context, userID, generationID, summaryID, expectedContent, key string, value any) error
}
