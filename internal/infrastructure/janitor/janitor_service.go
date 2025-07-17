package janitor

import (
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"log/slog"
	mathRand "math/rand"
	"strings"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/logging"
)

var (
	// Global janitor channel for up to 50 queued item-janitor requests (matches production)
	globalJanitorChan = make(chan *string, 50)
	janitorOverflows  *expvar.Int
)

func init() {
	janitorOverflows = expvar.NewInt("janitor_overflows")
}

// TransactionBodyStub is used for the janitor to check the latest resource.
// Unlike TransactionBody, the dates are left as strings for simplicity.
// This matches the production janitor.go implementation exactly.
type TransactionBodyStub struct {
	CreatedAt *string `json:"created_at"`
	UpdatedAt *string `json:"updated_at"`
	DeletedAt *string `json:"deleted_at"`
}

// JanitorService handles background maintenance tasks using the production-proven pattern
type JanitorService struct {
	transactionRepo repositories.TransactionRepository
	logger          *slog.Logger
	index           string
	workerWG        sync.WaitGroup
	shutdown        chan struct{}
	isRunning       bool
	mu              sync.RWMutex
}

// NewJanitorService creates a new janitor service matching production implementation
func NewJanitorService(transactionRepo repositories.TransactionRepository, logger *slog.Logger, index string) *JanitorService {
	return &JanitorService{
		transactionRepo: transactionRepo,
		logger:          logging.WithComponent(logger, "janitor_service"),
		index:           index,
		shutdown:        make(chan struct{}),
	}
}

// CheckItem queues a resource for the janitor to check.
// This matches the janitorCheckItem function from production janitor.go exactly.
func (j *JanitorService) CheckItem(objectRef string) {
	if objectRef == "" {
		j.logger.Debug("Skipping empty object reference")
		return
	}

	j.logger.Debug("Attempting to queue janitor item",
		"object_ref", safeLogString(&objectRef),
		"queue_length", len(globalJanitorChan),
		"queue_capacity", cap(globalJanitorChan))

	select {
	case globalJanitorChan <- &objectRef:
		// The item was queued successfully.
		j.logger.Debug("Janitor item queued successfully",
			"object_ref", safeLogString(&objectRef),
			"queue_length", len(globalJanitorChan))
	default:
		// The item was dropped.
		janitorOverflows.Add(1)
		j.logger.Warn("Janitor queue overflow, item dropped",
			"object_ref", safeLogString(&objectRef),
			"queue_capacity", cap(globalJanitorChan),
			"total_overflows", janitorOverflows.Value())
	}
}

// StartItemLoop is a long-running goroutine that processes janitor item checks on specific object refs.
// This matches the janitorItemLoop function from production janitor.go exactly.
func (j *JanitorService) StartItemLoop(ctx context.Context) {
	j.mu.Lock()
	if j.isRunning {
		j.mu.Unlock()
		j.logger.Warn("Janitor service already running, ignoring start request")
		return // Already running
	}
	j.isRunning = true
	j.mu.Unlock()

	j.logger.Info("Janitor service startup initiated",
		"index", j.index,
		"queue_capacity", cap(globalJanitorChan),
		"worker_count", 1)

	j.workerWG.Add(1)

	go func() {
		defer func() {
			j.workerWG.Done()
			j.mu.Lock()
			j.isRunning = false
			j.mu.Unlock()
			j.logger.Info("Janitor service shutdown completed")
		}()

		j.logger.Info("Janitor worker started, beginning item processing loop")
		itemsProcessed := 0
		startTime := time.Now()

		for {
			select {
			case <-j.shutdown:
				j.logger.Info("Janitor shutdown signal received",
					"items_processed", itemsProcessed,
					"uptime", time.Since(startTime))
				return
			case <-ctx.Done():
				j.logger.Info("Janitor context cancelled",
					"items_processed", itemsProcessed,
					"uptime", time.Since(startTime),
					"context_error", ctx.Err())
				return
			case objectRef, more := <-globalJanitorChan:
				if !more {
					j.logger.Warn("Janitor channel closed unexpectedly",
						"items_processed", itemsProcessed)
					return
				}

				itemsProcessed++
				j.logger.Debug("Janitor item received for processing",
					"object_ref", safeLogString(objectRef),
					"items_processed", itemsProcessed,
					"queue_length", len(globalJanitorChan))

				j.processItem(ctx, objectRef)
			}
		}
	}()

	j.logger.Info("Janitor service started successfully")
}

// Shutdown gracefully shuts down the janitor service
func (j *JanitorService) Shutdown() {
	j.mu.Lock()
	if !j.isRunning {
		j.mu.Unlock()
		return
	}
	j.mu.Unlock()

	// Send shutdown signal
	close(j.shutdown)

	// Wait for worker to finish
	j.workerWG.Wait()

	j.logger.Info("Janitor: shutdown complete")
}

// processItem processes a single janitor item with enhanced logging
func (j *JanitorService) processItem(ctx context.Context, objectRef *string) {
	startTime := time.Now()

	if objectRef == nil || *objectRef == "" {
		j.logger.Debug("Skipping nil or empty object reference")
		return
	}
	if strings.Contains(*objectRef, `"`) {
		j.logger.Error("Invalid object reference contains quotes",
			"object_ref", safeLogString(objectRef))
		return
	}

	j.logger.Info("Janitor processing started",
		"object_ref", safeLogString(objectRef))

	// Search for all documents with this object_ref and latest=true
	// This matches the production query exactly
	query := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"object_ref": *objectRef}},
					{"term": map[string]any{"latest": true}},
				},
			},
		},
	}

	j.logger.Debug("Executing janitor search query",
		"object_ref", safeLogString(objectRef),
		"index", j.index)

	// Use enhanced search to get version information (seq_no, primary_term)
	docs, err := j.transactionRepo.SearchWithVersions(ctx, j.index, query)
	if err != nil {
		j.logger.Error("Janitor search failed",
			"object_ref", safeLogString(objectRef),
			"error", err.Error(),
			"duration", time.Since(startTime))
		return
	}

	// Log the number of hits with analysis
	hitCount := len(docs)
	j.logger.Info("Janitor search completed",
		"object_ref", safeLogString(objectRef),
		"hits", hitCount,
		"search_duration", time.Since(startTime))

	if hitCount == 0 {
		j.logger.Info("No documents found for janitor processing",
			"object_ref", safeLogString(objectRef),
			"total_duration", time.Since(startTime))
		return
	}

	if hitCount == 1 {
		j.logger.Info("Single document found, no janitor action needed",
			"object_ref", safeLogString(objectRef),
			"document_id", docs[0].ID,
			"total_duration", time.Since(startTime))
		return
	}

	// Multiple hits found - need conflict resolution
	j.logger.Warn("Multiple latest documents found, initiating conflict resolution",
		"object_ref", safeLogString(objectRef),
		"hit_count", hitCount,
		"document_ids", getDocumentIDs(docs))

	// Find the _id that "wins": if any have `deleted_at`, it automatically wins.
	// Otherwise, the one with the latest `updated_at` wins.
	// This matches the production winner determination logic exactly.
	var winningID string
	var winningUpdatedAt string

	for _, doc := range docs {
		// Log the document details
		j.logger.Info("Janitor: hit",
			slog.String("object_ref", *objectRef),
			slog.String("id", doc.ID),
			slog.Int64("seq_no", *doc.SeqNo),
			slog.Int64("primary_term", *doc.PrimaryTerm))

		// Unmarshal the source into a TransactionBodyStub
		hitBody := new(TransactionBodyStub)
		sourceBytes, err := json.Marshal(doc.Source)
		if err != nil {
			j.logger.Error("Janitor: marshal error", slog.String("object_ref", *objectRef), slog.String("id", doc.ID), slog.Any("error", err))
			continue
		}

		err = json.Unmarshal(sourceBytes, hitBody)
		if err != nil {
			j.logger.Error("Janitor: unmarshal error", slog.String("object_ref", *objectRef), slog.String("id", doc.ID), slog.Any("error", err))
			// Skip this document and continue with the next one.
			continue
		}

		// Check if the hit has a `deleted_at` field (deletion priority)
		if hitBody.DeletedAt != nil && *hitBody.DeletedAt != "" {
			// The hit has a `deleted_at` field, so it wins.
			winningID = doc.ID
			break
		}

		// Check if the hit has an `updated_at` field
		if hitBody.UpdatedAt != nil && *hitBody.UpdatedAt != "" {
			// If the `updated_at` field is newer than the current `winningUpdatedAt` winner, it wins.
			if winningID == "" || *hitBody.UpdatedAt > winningUpdatedAt {
				winningID = doc.ID
				winningUpdatedAt = *hitBody.UpdatedAt
			}
		}
	}

	// Don't update anything if there is no winning hit
	if winningID == "" {
		j.logger.Info("Janitor: no winning hit", slog.String("object_ref", *objectRef))
		return
	}

	// Set all hits to `latest=false` except for the winning hit
	for _, doc := range docs {
		if doc.ID == winningID {
			// The winning hit must stay `latest=true`, so it doesn't need any update.
			continue
		}

		// Update the hit to `latest=false` with optimistic concurrency control
		if err := j.updateLatestFlag(ctx, doc, false, *objectRef); err != nil {
			// Check for version conflict
			if vErr, ok := err.(*repositories.VersionConflictError); ok {
				// Async retry with production delays (5-10 seconds)
				j.asyncRetry(ctx, *objectRef, vErr.DocumentID)
				// Don't attempt to update any other hits either; wait for the next check.
				return
			}
			j.logger.Error("Janitor: update error", slog.String("object_ref", *objectRef), slog.String("id", doc.ID), slog.Any("error", err))
		}
	}
}

// updateLatestFlag updates the latest flag for a document with optimistic concurrency control
func (j *JanitorService) updateLatestFlag(ctx context.Context, doc repositories.VersionedDocument, latest bool, objectRef string) error {
	updateBody := map[string]any{
		"doc": map[string]any{
			"latest": latest,
		},
	}

	bodyBytes, err := json.Marshal(updateBody)
	if err != nil {
		return fmt.Errorf("failed to marshal update body: %w", err)
	}

	params := &repositories.OptimisticUpdateParams{
		SeqNo:       doc.SeqNo,
		PrimaryTerm: doc.PrimaryTerm,
	}

	return j.transactionRepo.UpdateWithOptimisticLock(ctx, j.index, doc.ID,
		strings.NewReader(string(bodyBytes)), params)
}

// asyncRetry handles version conflicts with enhanced logging
func (j *JanitorService) asyncRetry(ctx context.Context, objectRef, docID string) {
	retryDelay := time.Duration(5+mathRand.Intn(5)) * time.Second

	j.logger.Info("Scheduling janitor retry due to version conflict",
		"object_ref", objectRef,
		"document_id", docID,
		"retry_delay", retryDelay)

	go func() {
		select {
		case <-time.After(retryDelay):
			j.logger.Info("Executing scheduled janitor retry",
				"object_ref", objectRef,
				"document_id", docID,
				"delay_elapsed", retryDelay)
			j.CheckItem(objectRef)
		case <-ctx.Done():
			j.logger.Info("Janitor retry cancelled due to context",
				"object_ref", objectRef,
				"document_id", docID,
				"context_error", ctx.Err())
		case <-j.shutdown:
			j.logger.Info("Janitor retry cancelled due to shutdown",
				"object_ref", objectRef,
				"document_id", docID)
		}
	}()
}

// GetMetrics returns janitor metrics for monitoring
func (j *JanitorService) GetMetrics() map[string]interface{} {
	j.mu.RLock()
	isRunning := j.isRunning
	j.mu.RUnlock()

	return map[string]interface{}{
		"queue_size": cap(globalJanitorChan),
		"queue_len":  len(globalJanitorChan),
		"overflows":  janitorOverflows.Value(),
		"is_running": isRunning,
		"index":      j.index,
	}
}

// IsRunning returns whether the janitor is currently processing items
func (j *JanitorService) IsRunning() bool {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.isRunning
}

// Helper functions for safe logging

// safeLogString safely logs a string pointer without exposing sensitive data
func safeLogString(s *string) string {
	if s == nil {
		return "<nil>"
	}
	if *s == "" {
		return "<empty>"
	}
	// Log first 50 characters to avoid log spam while maintaining debugging capability
	if len(*s) > 50 {
		return (*s)[:47] + "..."
	}
	return *s
}

// getDocumentIDs extracts document IDs for logging
func getDocumentIDs(docs []repositories.VersionedDocument) []string {
	ids := make([]string, len(docs))
	for i, doc := range docs {
		ids[i] = doc.ID
	}
	return ids
}
