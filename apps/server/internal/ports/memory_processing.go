package ports

import (
	"context"
	"encoding/json"
)

type ProcessingCheckpointRepository interface {
	ProcessingCheckpoint(context.Context, string, string, int64, string, json.RawMessage) (json.RawMessage, error)
}

// SummaryCheckpointRepository updates only private processing metadata. It must
// preserve the document body, edit version, and embedding state.
type SummaryCheckpointRepository interface {
	StoreSummaryCheckpoint(ctx context.Context, userID, generationID, summaryID, expectedContent, key string, value any) error
}
