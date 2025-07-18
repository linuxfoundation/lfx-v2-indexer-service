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

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
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
	storageRepo contracts.StorageRepository
	logger      *slog.Logger
	index       string
	workerWG    sync.WaitGroup
	shutdown    chan struct{}
	isRunning   bool
	mu          sync.RWMutex
}

// NewJanitorService creates a new janitor service matching production implementation
func NewJanitorService(storageRepo contracts.StorageRepository, logger *slog.Logger, index string) *JanitorService {
	return &JanitorService{
		storageRepo: storageRepo,
		logger:      logging.WithComponent(logger, "janitor_service"),
		index:       index,
		shutdown:    make(chan struct{}),
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
			if r := recover(); r != nil {
				j.logger.Error("Janitor worker panic recovered",
					"panic", r,
					"stack", fmt.Sprintf("%+v", r))
			}
			j.workerWG.Done()
			j.mu.Lock()
			j.isRunning = false
			j.mu.Unlock()
			j.logger.Info("Janitor service shutdown completed")
		}()

		j.logger.Info("Janitor worker started, beginning item processing loop")
		itemsProcessed := 0
		itemsSkipped := 0
		conflictsResolved := 0
		errors := 0
		startTime := time.Now()
		lastHealthLog := time.Now()

		for {
			select {
			case <-j.shutdown:
				j.logger.Info("Janitor shutdown signal received",
					"items_processed", itemsProcessed,
					"items_skipped", itemsSkipped,
					"conflicts_resolved", conflictsResolved,
					"errors", errors,
					"uptime", time.Since(startTime))
				return
			case <-ctx.Done():
				j.logger.Info("Janitor context cancelled",
					"items_processed", itemsProcessed,
					"items_skipped", itemsSkipped,
					"conflicts_resolved", conflictsResolved,
					"errors", errors,
					"uptime", time.Since(startTime),
					"context_error", ctx.Err())
				return
			case objectRef, more := <-globalJanitorChan:
				if !more {
					j.logger.Warn("Janitor channel closed unexpectedly",
						"items_processed", itemsProcessed)
					return
				}

				// Worker health logging every 100 items or 5 minutes
				if itemsProcessed%100 == 0 || time.Since(lastHealthLog) > 5*time.Minute {
					j.logWorkerHealth(itemsProcessed, itemsSkipped, conflictsResolved, errors, startTime)
					lastHealthLog = time.Now()
				}

				itemsProcessed++
				j.logger.Debug("Janitor item received for processing",
					"object_ref", safeLogString(objectRef),
					"items_processed", itemsProcessed,
					"queue_length", len(globalJanitorChan))

				result := j.processItem(ctx, objectRef)
				switch result {
				case "skipped":
					itemsSkipped++
				case "conflict_resolved":
					conflictsResolved++
				case "error":
					errors++
				}
			}
		}
	}()

	j.logger.Info("Janitor service started successfully")
}

// Shutdown gracefully shuts down the janitor service
func (j *JanitorService) Shutdown() {
	shutdownStartTime := time.Now()

	j.mu.Lock()
	if !j.isRunning {
		j.mu.Unlock()
		j.logger.Info("Janitor service shutdown called but not running")
		return
	}
	j.mu.Unlock()

	j.logger.Info("Janitor service shutdown initiated",
		"queue_length", len(globalJanitorChan),
		"queue_capacity", cap(globalJanitorChan))

	// Send shutdown signal
	close(j.shutdown)

	// Wait for worker to finish
	j.logger.Info("Waiting for janitor worker to finish...")
	j.workerWG.Wait()

	j.logger.Info("Janitor service shutdown completed",
		"shutdown_duration", time.Since(shutdownStartTime))
}

// processItem processes a single janitor item with enhanced logging
func (j *JanitorService) processItem(ctx context.Context, objectRef *string) string {
	startTime := time.Now()

	if objectRef == nil || *objectRef == "" {
		j.logger.Debug("Skipping nil or empty object reference")
		return "skipped"
	}
	if strings.Contains(*objectRef, `"`) {
		j.logger.Error("Invalid object reference contains quotes",
			"object_ref", safeLogString(objectRef))
		return "error"
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
	docs, err := j.storageRepo.SearchWithVersions(ctx, j.index, query)
	if err != nil {
		j.logger.Error("Janitor search failed",
			"object_ref", safeLogString(objectRef),
			"error", err.Error(),
			"duration", time.Since(startTime))
		return "error"
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
		return "skipped"
	}

	if hitCount == 1 {
		j.logger.Info("Single document found, no janitor action needed",
			"object_ref", safeLogString(objectRef),
			"document_id", docs[0].ID,
			"total_duration", time.Since(startTime))
		return "skipped"
	}

	// Multiple hits found - need conflict resolution
	conflictStartTime := time.Now()
	j.logger.Warn("Multiple latest documents found, initiating conflict resolution",
		"object_ref", safeLogString(objectRef),
		"hit_count", hitCount,
		"document_ids", getDocumentIDs(docs))

	// Find the _id that "wins": if any have `deleted_at`, it automatically wins.
	// Otherwise, the one with the latest `updated_at` wins.
	// This matches the production winner determination logic exactly.
	var winningID string
	var winningUpdatedAt string
	deletedDocCount := 0
	updatedDocCount := 0

	for _, doc := range docs {
		// Log the document details
		j.logger.Debug("Analyzing document for conflict resolution",
			"object_ref", safeLogString(objectRef),
			"document_id", doc.ID,
			"seq_no", *doc.SeqNo,
			"primary_term", *doc.PrimaryTerm)

		// Unmarshal the source into a TransactionBodyStub
		hitBody := new(TransactionBodyStub)
		sourceBytes, err := json.Marshal(doc.Source)
		if err != nil {
			j.logger.Error("Document marshal error during conflict resolution",
				"object_ref", safeLogString(objectRef),
				"document_id", doc.ID,
				"error", err.Error())
			continue
		}

		err = json.Unmarshal(sourceBytes, hitBody)
		if err != nil {
			j.logger.Error("Document unmarshal error during conflict resolution",
				"object_ref", safeLogString(objectRef),
				"document_id", doc.ID,
				"error", err.Error())
			// Skip this document and continue with the next one.
			continue
		}

		// Check if the hit has a `deleted_at` field (deletion priority)
		if hitBody.DeletedAt != nil && *hitBody.DeletedAt != "" {
			deletedDocCount++
			j.logger.Info("Document with deletion timestamp found (takes priority)",
				"object_ref", safeLogString(objectRef),
				"document_id", doc.ID,
				"deleted_at", *hitBody.DeletedAt)

			// The hit has a `deleted_at` field, so it wins.
			winningID = doc.ID
			break
		}

		// Check if the hit has an `updated_at` field
		if hitBody.UpdatedAt != nil && *hitBody.UpdatedAt != "" {
			updatedDocCount++
			j.logger.Debug("Document with updated timestamp found",
				"object_ref", safeLogString(objectRef),
				"document_id", doc.ID,
				"updated_at", *hitBody.UpdatedAt,
				"current_winner", winningID)

			// If the `updated_at` field is newer than the current `winningUpdatedAt` winner, it wins.
			if winningID == "" || *hitBody.UpdatedAt > winningUpdatedAt {
				winningID = doc.ID
				winningUpdatedAt = *hitBody.UpdatedAt
			}
		}
	}

	j.logger.Info("Conflict resolution analysis completed",
		"object_ref", safeLogString(objectRef),
		"winning_id", winningID,
		"winning_updated_at", winningUpdatedAt,
		"deleted_docs", deletedDocCount,
		"updated_docs", updatedDocCount,
		"analysis_duration", time.Since(conflictStartTime))

	// Don't update anything if there is no winning hit
	if winningID == "" {
		j.logger.Info("Janitor: no winning hit", slog.String("object_ref", *objectRef))
		return "skipped"
	}

	// Set all hits to `latest=false` except for the winning hit
	updatesAttempted := 0
	updatesSuccessful := 0
	updateStartTime := time.Now()

	for _, doc := range docs {
		if doc.ID == winningID {
			// The winning hit must stay `latest=true`, so it doesn't need any update.
			j.logger.Info("Skipping update for winning document",
				"object_ref", safeLogString(objectRef),
				"document_id", doc.ID,
				"reason", "winning_document")
			continue
		}

		updatesAttempted++
		j.logger.Debug("Attempting to update document to latest=false",
			"object_ref", safeLogString(objectRef),
			"document_id", doc.ID,
			"attempt", updatesAttempted)

		// Update the hit to `latest=false` with optimistic concurrency control
		if err := j.updateLatestFlag(ctx, doc, false, *objectRef); err != nil {
			// Check for version conflict
			if vErr, ok := err.(*contracts.VersionConflictError); ok {
				j.logger.Warn("Version conflict detected, scheduling retry",
					"object_ref", safeLogString(objectRef),
					"document_id", vErr.DocumentID,
					"conflict_type", "optimistic_lock")

				// Async retry with production delays (5-10 seconds)
				j.asyncRetry(ctx, *objectRef, vErr.DocumentID)
				// Don't attempt to update any other hits either; wait for the next check.
				return "conflict_resolved"
			}
			j.logger.Error("Document update failed",
				"object_ref", safeLogString(objectRef),
				"document_id", doc.ID,
				"error", err.Error(),
				"error_type", "storage_error")
		} else {
			updatesSuccessful++
			j.logger.Debug("Document updated successfully",
				"object_ref", safeLogString(objectRef),
				"document_id", doc.ID,
				"new_latest", false)
		}
	}

	j.logger.Info("Janitor processing completed successfully",
		"object_ref", safeLogString(objectRef),
		"winning_id", winningID,
		"updates_attempted", updatesAttempted,
		"updates_successful", updatesSuccessful,
		"update_duration", time.Since(updateStartTime),
		"total_duration", time.Since(startTime))
	return "conflict_resolved"
}

// updateLatestFlag updates the latest flag for a document with optimistic concurrency control
func (j *JanitorService) updateLatestFlag(ctx context.Context, doc contracts.VersionedDocument, latest bool, objectRef string) error {
	startTime := time.Now()

	j.logger.Debug("Updating latest flag for document",
		"object_ref", objectRef,
		"document_id", doc.ID,
		"latest", latest,
		"seq_no", *doc.SeqNo,
		"primary_term", *doc.PrimaryTerm)

	updateBody := map[string]any{
		"doc": map[string]any{
			"latest": latest,
		},
	}

	bodyBytes, err := json.Marshal(updateBody)
	if err != nil {
		j.logger.Error("Failed to marshal update body",
			"object_ref", objectRef,
			"document_id", doc.ID,
			"error", err.Error())
		return fmt.Errorf("failed to marshal update body: %w", err)
	}

	params := &contracts.OptimisticUpdateParams{
		SeqNo:       doc.SeqNo,
		PrimaryTerm: doc.PrimaryTerm,
	}

	err = j.storageRepo.UpdateWithOptimisticLock(ctx, j.index, doc.ID,
		strings.NewReader(string(bodyBytes)), params)

	if err != nil {
		j.logger.Error("Storage update failed",
			"object_ref", objectRef,
			"document_id", doc.ID,
			"latest", latest,
			"error", err.Error(),
			"duration", time.Since(startTime))
		return err
	}

	j.logger.Info("Document latest flag updated successfully",
		"object_ref", objectRef,
		"document_id", doc.ID,
		"latest", latest,
		"duration", time.Since(startTime))

	return nil
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

// logWorkerHealth logs worker health metrics and performance data
func (j *JanitorService) logWorkerHealth(itemsProcessed, itemsSkipped, conflictsResolved, errors int, startTime time.Time) {
	uptime := time.Since(startTime)
	queueLength := len(globalJanitorChan)

	j.logger.Info("Janitor worker health check",
		"items_processed", itemsProcessed,
		"items_skipped", itemsSkipped,
		"conflicts_resolved", conflictsResolved,
		"errors", errors,
		"uptime", uptime,
		"queue_length", queueLength,
		"processing_rate", fmt.Sprintf("%.2f items/min", float64(itemsProcessed)/uptime.Minutes()))

	// Log warning if queue is backing up
	if queueLength > cap(globalJanitorChan)/2 {
		j.logger.Warn("Janitor queue backing up",
			"queue_length", queueLength,
			"queue_capacity", cap(globalJanitorChan),
			"queue_utilization", fmt.Sprintf("%.1f%%", float64(queueLength)/float64(cap(globalJanitorChan))*100))
	}
}

// GetMetrics returns janitor metrics for monitoring
func (j *JanitorService) GetMetrics() map[string]interface{} {
	j.mu.RLock()
	isRunning := j.isRunning
	j.mu.RUnlock()

	queueLength := len(globalJanitorChan)
	queueCapacity := cap(globalJanitorChan)
	overflows := janitorOverflows.Value()
	queueUtilization := float64(queueLength) / float64(queueCapacity) * 100

	metrics := map[string]interface{}{
		"queue_size":        queueCapacity,
		"queue_length":      queueLength,
		"queue_utilization": queueUtilization,
		"overflows":         overflows,
		"is_running":        isRunning,
		"index":             j.index,
		"health_status":     j.getHealthStatus(queueUtilization, isRunning),
	}

	// Log metrics periodically for monitoring
	j.logger.Debug("Janitor service metrics",
		"queue_length", queueLength,
		"queue_capacity", queueCapacity,
		"queue_utilization", fmt.Sprintf("%.1f%%", queueUtilization),
		"overflows", overflows,
		"is_running", isRunning,
		"health_status", metrics["health_status"])

	return metrics
}

// getHealthStatus returns the health status based on queue utilization and running state
func (j *JanitorService) getHealthStatus(queueUtilization float64, isRunning bool) string {
	if !isRunning {
		return "stopped"
	}
	if queueUtilization > 90 {
		return "critical"
	}
	if queueUtilization > 70 {
		return "warning"
	}
	return "healthy"
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
func getDocumentIDs(docs []contracts.VersionedDocument) []string {
	ids := make([]string, len(docs))
	for i, doc := range docs {
		ids[i] = doc.ID
	}
	return ids
}
