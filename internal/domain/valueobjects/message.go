package valueobjects

import (
	"time"
)

// ProcessingResult represents the result of processing a message
type ProcessingResult struct {
	Success      bool
	Error        error
	ProcessedAt  time.Time
	Duration     time.Duration
	MessageID    string
	DocumentID   string
	IndexSuccess bool
}
