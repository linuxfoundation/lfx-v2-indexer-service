package janitor

import (
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"log"
	mathRand "math/rand"
	"strings"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
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
	index           string
	workerWG        sync.WaitGroup
	shutdown        chan struct{}
	isRunning       bool
	mu              sync.RWMutex
}

// NewJanitorService creates a new janitor service matching production implementation
func NewJanitorService(transactionRepo repositories.TransactionRepository, index string) *JanitorService {
	return &JanitorService{
		transactionRepo: transactionRepo,
		index:           index,
		shutdown:        make(chan struct{}),
	}
}

// CheckItem queues a resource for the janitor to check.
// This matches the janitorCheckItem function from production janitor.go exactly.
func (j *JanitorService) CheckItem(objectRef string) {
	select {
	case globalJanitorChan <- &objectRef:
		// The item was queued successfully.
	default:
		// The item was dropped.
		janitorOverflows.Add(1)
	}
}

// StartItemLoop is a long-running goroutine that processes janitor item checks on specific object refs.
// This matches the janitorItemLoop function from production janitor.go exactly.
func (j *JanitorService) StartItemLoop(ctx context.Context) {
	j.mu.Lock()
	if j.isRunning {
		j.mu.Unlock()
		return // Already running
	}
	j.isRunning = true
	j.mu.Unlock()

	log.Println("Janitor: starting item loop")
	j.workerWG.Add(1)

	go func() {
		defer func() {
			j.workerWG.Done()
			j.mu.Lock()
			j.isRunning = false
			j.mu.Unlock()
			log.Println("Janitor: exiting item loop")
		}()

		for {
			select {
			case <-j.shutdown:
				return
			case <-ctx.Done():
				return
			case objectRef, more := <-globalJanitorChan:
				if !more {
					// The channel was closed and there are no more items.
					return
				}
				j.processItem(ctx, objectRef)
			}
		}
	}()
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

	log.Println("Janitor: shutdown complete")
}

// processItem processes a single janitor item.
// This matches the janitorProcessItem function from production janitor.go exactly.
func (j *JanitorService) processItem(ctx context.Context, objectRef *string) {
	if objectRef == nil || *objectRef == "" {
		// The object ref is nil or empty; ignore it.
		return
	}
	if strings.Contains(*objectRef, `"`) {
		// The object ref is invalid.
		log.Printf("Janitor: invalid object ref: %s", *objectRef)
		return
	}

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

	// Use enhanced search to get version information (seq_no, primary_term)
	docs, err := j.transactionRepo.SearchWithVersions(ctx, j.index, query)
	if err != nil {
		log.Printf("Janitor: search error for object_ref %s: %v", *objectRef, err)
		return
	}

	// Log the number of hits
	log.Printf("Janitor: search complete for object_ref %s, hits: %d", *objectRef, len(docs))

	if len(docs) > 1 {
		// There are multiple hits for the object ref that also have "latest=true".
		// Find the _id that "wins": if any have `deleted_at`, it automatically wins.
		// Otherwise, the one with the latest `updated_at` wins.
		// This matches the production winner determination logic exactly.
		var winningID string
		var winningUpdatedAt string

		for _, doc := range docs {
			// Log the document details
			log.Printf("Janitor: hit - object_ref: %s, _id: %s, _seq_no: %d, _primary_term: %d",
				*objectRef, doc.ID, *doc.SeqNo, *doc.PrimaryTerm)

			// Unmarshal the source into a TransactionBodyStub
			hitBody := new(TransactionBodyStub)
			sourceBytes, err := json.Marshal(doc.Source)
			if err != nil {
				log.Printf("Janitor: marshal error for object_ref %s, _id %s: %v",
					*objectRef, doc.ID, err)
				continue
			}

			err = json.Unmarshal(sourceBytes, hitBody)
			if err != nil {
				log.Printf("Janitor: unmarshal error for object_ref %s, _id %s: %v",
					*objectRef, doc.ID, err)
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
			log.Printf("Janitor: no winning hit for object_ref: %s", *objectRef)
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
				log.Printf("Janitor: update error for object_ref %s, _id %s: %v",
					*objectRef, doc.ID, err)
			}
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

// asyncRetry handles version conflicts with async retry matching production behavior
func (j *JanitorService) asyncRetry(ctx context.Context, objectRef, docID string) {
	go func() {
		// Production retry delay: between 5 and 10 seconds with jitter
		sleepMilliseconds := mathRand.Intn(5000) + 5000
		log.Printf("Janitor: update conflict, will retry - object_ref: %s, _id: %s, sleep_ms: %d",
			objectRef, docID, sleepMilliseconds)

		time.Sleep(time.Duration(sleepMilliseconds) * time.Millisecond)

		// Queue the object ref for re-checking (exactly like production)
		j.CheckItem(objectRef)
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
