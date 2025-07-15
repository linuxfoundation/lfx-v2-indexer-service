package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
)

// JanitorUseCase handles background maintenance tasks
type JanitorUseCase struct {
	transactionRepo repositories.TransactionRepository
	index           string
	interval        time.Duration
	batchSize       int
}

// NewJanitorUseCase creates a new janitor use case
func NewJanitorUseCase(
	transactionRepo repositories.TransactionRepository,
	index string,
	interval time.Duration,
	batchSize int,
) *JanitorUseCase {
	return &JanitorUseCase{
		transactionRepo: transactionRepo,
		index:           index,
		interval:        interval,
		batchSize:       batchSize,
	}
}

// StartJanitorTasks starts the background janitor tasks
func (uc *JanitorUseCase) StartJanitorTasks(ctx context.Context) error {
	log.Printf("Starting janitor tasks with interval: %v", uc.interval)

	ticker := time.NewTicker(uc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Janitor tasks stopped")
			return ctx.Err()
		case <-ticker.C:
			if err := uc.runJanitorTasks(ctx); err != nil {
				log.Printf("Error running janitor tasks: %v", err)
			} else {
				log.Printf("Janitor tasks completed successfully")
			}
		}
	}
}

// runJanitorTasks executes all janitor tasks
func (uc *JanitorUseCase) runJanitorTasks(ctx context.Context) error {
	log.Printf("Running janitor tasks...")

	// Update latest flags
	if err := uc.updateLatestFlags(ctx); err != nil {
		return fmt.Errorf("failed to update latest flags: %w", err)
	}

	// Clean up old documents
	if err := uc.cleanupOldDocuments(ctx); err != nil {
		return fmt.Errorf("failed to cleanup old documents: %w", err)
	}

	return nil
}

// updateLatestFlags updates the latest flags for all objects
func (uc *JanitorUseCase) updateLatestFlags(ctx context.Context) error {
	log.Printf("Updating latest flags...")

	// Query for all unique object keys
	query := map[string]any{
		"size": 0,
		"aggs": map[string]any{
			"unique_objects": map[string]any{
				"terms": map[string]any{
					"field": "object_key",
					"size":  10000,
				},
			},
		},
	}

	docs, err := uc.transactionRepo.Search(ctx, uc.index, query)
	if err != nil {
		return fmt.Errorf("failed to query unique objects: %w", err)
	}

	// Process each unique object
	for _, doc := range docs {
		if objectKey, ok := doc["object_key"].(string); ok {
			if err := uc.updateLatestFlagsForObject(ctx, objectKey); err != nil {
				log.Printf("Failed to update latest flags for object %s: %v", objectKey, err)
				continue
			}
		}
	}

	return nil
}

// updateLatestFlagsForObject updates the latest flags for a specific object
func (uc *JanitorUseCase) updateLatestFlagsForObject(ctx context.Context, objectKey string) error {
	// Query for all documents for this object, sorted by timestamp
	query := map[string]any{
		"query": map[string]any{
			"term": map[string]any{
				"object_key": objectKey,
			},
		},
		"sort": []map[string]any{
			{
				"@timestamp": map[string]any{
					"order": "desc",
				},
			},
		},
		"size": uc.batchSize,
	}

	docs, err := uc.transactionRepo.Search(ctx, uc.index, query)
	if err != nil {
		return fmt.Errorf("failed to query documents for object %s: %w", objectKey, err)
	}

	if len(docs) == 0 {
		return nil
	}

	return uc.updateLatestFlagsForDocs(ctx, docs)
}

// updateLatestFlagsForDocs updates the latest flags for a set of documents
func (uc *JanitorUseCase) updateLatestFlagsForDocs(ctx context.Context, docs []map[string]any) error {
	operations := make([]repositories.BulkOperation, 0, len(docs))

	for i, doc := range docs {
		docID := uc.getDocumentID(doc)
		if docID == "" {
			continue
		}

		// First document is the latest
		isLatest := i == 0

		// Update the latest flag
		doc["latest"] = isLatest

		// Create update operation
		updateBody := map[string]any{
			"doc": map[string]any{
				"latest": isLatest,
			},
		}

		// Convert to JSON and create Reader
		bodyBytes, err := json.Marshal(updateBody)
		if err != nil {
			log.Printf("Failed to marshal update body for doc %s: %v", docID, err)
			continue
		}

		operations = append(operations, repositories.BulkOperation{
			Index:  uc.index,
			DocID:  docID,
			Action: "update",
			Body:   strings.NewReader(string(bodyBytes)),
		})
	}

	if len(operations) > 0 {
		return uc.transactionRepo.BulkIndex(ctx, operations)
	}

	return nil
}

// cleanupOldDocuments removes old documents based on retention policy
func (uc *JanitorUseCase) cleanupOldDocuments(ctx context.Context) error {
	log.Printf("Cleaning up old documents...")

	// For now, this is a placeholder - implement retention policy as needed
	// This could delete documents older than X days, keep only Y versions per object, etc.

	return nil
}

// getDocumentID extracts the document ID from a document
func (uc *JanitorUseCase) getDocumentID(doc map[string]any) string {
	if id, ok := doc["_id"].(string); ok {
		return id
	}
	if source, ok := doc["_source"].(map[string]any); ok {
		if id, ok := source["id"].(string); ok {
			return id
		}
	}
	return ""
}

// GetMetrics returns janitor metrics (placeholder for monitoring)
func (uc *JanitorUseCase) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"interval":   uc.interval.String(),
		"batch_size": uc.batchSize,
		"index":      uc.index,
		"last_run":   time.Now(), // This would be tracked in a real implementation
	}
}
