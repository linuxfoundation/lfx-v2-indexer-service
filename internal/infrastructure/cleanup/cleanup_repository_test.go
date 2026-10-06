// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package cleanup

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

// MockTransactionRepository implements the contracts.TransactionRepository interface for testing
type MockTransactionRepository struct {
	mock.Mock
}

func (m *MockTransactionRepository) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	args := m.Called(ctx, index, docID, body)
	return args.Error(0)
}

func (m *MockTransactionRepository) Search(ctx context.Context, index string, query map[string]any) ([]map[string]any, error) {
	args := m.Called(ctx, index, query)
	return args.Get(0).([]map[string]any), args.Error(1)
}

func (m *MockTransactionRepository) Update(ctx context.Context, index string, docID string, body io.Reader) error {
	args := m.Called(ctx, index, docID, body)
	return args.Error(0)
}

func (m *MockTransactionRepository) Delete(ctx context.Context, index string, docID string) error {
	args := m.Called(ctx, index, docID)
	return args.Error(0)
}

func (m *MockTransactionRepository) BulkIndex(ctx context.Context, operations []contracts.BulkOperation) ([]error, error) {
	args := m.Called(ctx, operations)
	itemErrors, _ := args.Get(0).([]error)
	return itemErrors, args.Error(1)
}

func (m *MockTransactionRepository) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockTransactionRepository) UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *contracts.OptimisticUpdateParams) error {
	args := m.Called(ctx, index, docID, body, params)
	return args.Error(0)
}

func (m *MockTransactionRepository) SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]contracts.VersionedDocument, error) {
	args := m.Called(ctx, index, query)
	return args.Get(0).([]contracts.VersionedDocument), args.Error(1)
}

func TestNewCleanupRepository(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	assert.NotNil(t, service)
	assert.Equal(t, "test-index", service.index)
	assert.False(t, service.IsRunning())
}

func TestCleanupRepository_CheckItem(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	// Clear the channel first
	for len(globalJanitorChan) > 0 {
		<-globalJanitorChan
	}

	// Test case where object ref is valid and gets processed
	ctx := context.Background()
	objectRef := "test-object-ref"

	// Set up mock expectations
	expectedQuery := map[string]any{
		"size":                janitorMaxDuplicates + 1,
		"seq_no_primary_term": true,
		"sort": []map[string]any{
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at"},
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"object_ref": objectRef}},
					{"term": map[string]any{"latest": true}},
				},
			},
		},
	}

	mockDocs := []contracts.VersionedDocument{
		{
			ID:          "doc1",
			SeqNo:       &[]int64{1}[0],
			PrimaryTerm: &[]int64{1}[0],
			Source: map[string]any{
				"updated_at": "2023-01-01T00:00:00Z",
			},
		},
	}

	mockRepo.On("SearchWithVersions", ctx, "test-index", expectedQuery).Return(mockDocs, nil)

	// Process the item
	service.processItem(ctx, &objectRef)

	// Verify the mock was called
	mockRepo.AssertExpectations(t)
}

func TestCleanupRepository_ProcessItemWithError(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	ctx := context.Background()
	objectRef := "test-object-ref"

	// Set up mock to return an error
	mockRepo.On("SearchWithVersions", mock.Anything, mock.Anything, mock.Anything).Return([]contracts.VersionedDocument{}, fmt.Errorf("search error"))

	// Process the item - should not panic
	service.processItem(ctx, &objectRef)

	// Verify the mock was called
	mockRepo.AssertExpectations(t)
}

func TestCleanupRepository_ProcessItemWithInvalidObjectRef(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	ctx := context.Background()
	objectRef := `test"object"ref` // Contains quotes - should be invalid

	// Process the item - should return early without calling repository
	service.processItem(ctx, &objectRef)

	// Verify no repository calls were made
	mockRepo.AssertNotCalled(t, "SearchWithVersions")
}

func TestCleanupRepository_ProcessMultipleHits(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	ctx := context.Background()
	objectRef := "test-object-ref"

	expectedQuery := map[string]any{
		"size":                janitorMaxDuplicates + 1,
		"seq_no_primary_term": true,
		"sort": []map[string]any{
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at"},
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"object_ref": objectRef}},
					{"term": map[string]any{"latest": true}},
				},
			},
		},
	}

	// Create mock documents with different timestamps
	seqNo1, primaryTerm1 := int64(1), int64(1)
	seqNo2, primaryTerm2 := int64(2), int64(1)

	mockDocs := []contracts.VersionedDocument{
		{
			ID:          "doc1",
			SeqNo:       &seqNo1,
			PrimaryTerm: &primaryTerm1,
			Source: map[string]any{
				"updated_at": "2023-01-02T00:00:00Z", // Newer
			},
		},
		{
			ID:          "doc2",
			SeqNo:       &seqNo2,
			PrimaryTerm: &primaryTerm2,
			Source: map[string]any{
				"updated_at": "2023-01-01T00:00:00Z", // Older
			},
		},
	}

	mockRepo.On("SearchWithVersions", ctx, "test-index", expectedQuery).Return(mockDocs, nil)

	// Expect the newer document (doc1) to be preserved and the older (doc2) to be updated
	updateParams := &contracts.OptimisticUpdateParams{
		SeqNo:       &seqNo2,
		PrimaryTerm: &primaryTerm2,
	}
	mockRepo.On("UpdateWithOptimisticLock", ctx, "test-index", "doc2", mock.Anything, updateParams).Return(nil)

	// Process the item
	service.processItem(ctx, &objectRef)

	// Verify the mocks were called
	mockRepo.AssertExpectations(t)
}

func TestCleanupRepository_ProcessVersionConflict(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	// Use a cancelled context to prevent async retry from executing
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	objectRef := "test-object-ref"

	seqNo1, primaryTerm1 := int64(1), int64(1)
	seqNo2, primaryTerm2 := int64(2), int64(1)

	mockDocs := []contracts.VersionedDocument{
		{
			ID:          "doc1",
			SeqNo:       &seqNo1,
			PrimaryTerm: &primaryTerm1,
			Source: map[string]any{
				"updated_at": "2023-01-02T00:00:00Z",
			},
		},
		{
			ID:          "doc2",
			SeqNo:       &seqNo2,
			PrimaryTerm: &primaryTerm2,
			Source: map[string]any{
				"updated_at": "2023-01-01T00:00:00Z",
			},
		},
	}

	expectedQuery := map[string]any{
		"size":                janitorMaxDuplicates + 1,
		"seq_no_primary_term": true,
		"sort": []map[string]any{
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at"},
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"object_ref": objectRef}},
					{"term": map[string]any{"latest": true}},
				},
			},
		},
	}

	mockRepo.On("SearchWithVersions", ctx, "test-index", expectedQuery).Return(mockDocs, nil)

	// Mock version conflict error
	updateParams := &contracts.OptimisticUpdateParams{
		SeqNo:       &seqNo2,
		PrimaryTerm: &primaryTerm2,
	}
	mockRepo.On("UpdateWithOptimisticLock", ctx, "test-index", "doc2", mock.Anything, updateParams).Return(&contracts.VersionConflictError{
		DocumentID: "doc2",
		Err:        fmt.Errorf("version conflict"),
	})

	// Process the item - should handle version conflict gracefully
	result := service.processItem(ctx, &objectRef)

	// The retry is only scheduled, not yet successful: the doc stays
	// latest=true until that retry completes, so this must not be reported
	// as "conflict_resolved" or the stale duplicate goes unnoticed in the
	// interim.
	assert.Equal(t, "retry_scheduled", result)

	// Verify the mocks were called
	mockRepo.AssertExpectations(t)
}

func TestCleanupRepository_ProcessWithMarshalError(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	ctx := context.Background()
	objectRef := "test-object-ref"

	seqNo1 := int64(1)
	primaryTerm1 := int64(1)

	// Create document with source that can't be marshaled properly
	mockDocs := []contracts.VersionedDocument{
		{
			ID:          "doc1",
			SeqNo:       &seqNo1,
			PrimaryTerm: &primaryTerm1,
			Source: map[string]any{
				"invalid_field": make(chan int), // This will cause json.Marshal to fail
			},
		},
	}

	expectedQuery := map[string]any{
		"size":                janitorMaxDuplicates + 1,
		"seq_no_primary_term": true,
		"sort": []map[string]any{
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at"},
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"object_ref": objectRef}},
					{"term": map[string]any{"latest": true}},
				},
			},
		},
	}

	mockRepo.On("SearchWithVersions", ctx, "test-index", expectedQuery).Return(mockDocs, nil)

	// Process the item - should handle marshal error gracefully
	service.processItem(ctx, &objectRef)

	// Verify the mock was called but no update was attempted
	mockRepo.AssertExpectations(t)
	mockRepo.AssertNotCalled(t, "UpdateWithOptimisticLock")
}

func TestCleanupRepository_ProcessTruncatesAtDuplicateCap(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	ctx := context.Background()
	objectRef := "test-object-ref"

	// janitorMaxDuplicates+1 hits (the cap-probing size requested by the
	// query) with the winner first, janitorMaxDuplicates-1 losers next, and
	// one more hit appended past the cap. If truncation didn't actually
	// drop that last hit, it would need its own latest=false update since
	// it's older than the winner; this test confirms it's silently dropped
	// at the search-result boundary and never reaches conflict resolution.
	mockDocs := make([]contracts.VersionedDocument, 0, janitorMaxDuplicates+1)
	winnerSeqNo, winnerPrimaryTerm := int64(0), int64(1)
	mockDocs = append(mockDocs, contracts.VersionedDocument{
		ID:          "doc-winner",
		SeqNo:       &winnerSeqNo,
		PrimaryTerm: &winnerPrimaryTerm,
		Source:      map[string]any{"updated_at": "2023-06-01T00:00:00Z"},
	})
	for i := 1; i < janitorMaxDuplicates; i++ {
		seqNo, primaryTerm := int64(i), int64(1)
		mockDocs = append(mockDocs, contracts.VersionedDocument{
			ID:          fmt.Sprintf("doc-loser-%d", i),
			SeqNo:       &seqNo,
			PrimaryTerm: &primaryTerm,
			Source:      map[string]any{"updated_at": "2023-01-01T00:00:00Z"},
		})
	}
	excludedSeqNo, excludedPrimaryTerm := int64(999), int64(1)
	mockDocs = append(mockDocs, contracts.VersionedDocument{
		ID:          "doc-excluded",
		SeqNo:       &excludedSeqNo,
		PrimaryTerm: &excludedPrimaryTerm,
		Source:      map[string]any{"updated_at": "2022-01-01T00:00:00Z"},
	})

	expectedQuery := map[string]any{
		"size":                janitorMaxDuplicates + 1,
		"seq_no_primary_term": true,
		"sort": []map[string]any{
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at"},
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"object_ref": objectRef}},
					{"term": map[string]any{"latest": true}},
				},
			},
		},
	}
	mockRepo.On("SearchWithVersions", ctx, "test-index", expectedQuery).Return(mockDocs, nil)
	mockRepo.On("UpdateWithOptimisticLock", ctx, "test-index", mock.AnythingOfType("string"), mock.Anything, mock.Anything).Return(nil)

	service.processItem(ctx, &objectRef)

	// Exactly the capped losers (janitorMaxDuplicates - 1) are updated; the
	// hit past the cap never gets attempted.
	mockRepo.AssertNumberOfCalls(t, "UpdateWithOptimisticLock", janitorMaxDuplicates-1)
	mockRepo.AssertNotCalled(t, "UpdateWithOptimisticLock", ctx, "test-index", "doc-excluded", mock.Anything, mock.Anything)
}

func TestCleanupRepository_ProcessWithNilSeqNoAndPrimaryTerm(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	ctx := context.Background()
	objectRef := "test-object-ref"

	// Both hits are missing _seq_no/_primary_term (e.g. OpenSearch omitted
	// them despite seq_no_primary_term:true). safeLogInt64 must not panic
	// dereferencing these, and updateLatestFlag must skip the update rather
	// than send an OptimisticUpdateParams with nil SeqNo/PrimaryTerm, which
	// would make UpdateWithOptimisticLock perform an unconditional update
	// and defeat the optimistic lock entirely.
	mockDocs := []contracts.VersionedDocument{
		{
			ID:     "doc-winner",
			Source: map[string]any{"updated_at": "2023-01-02T00:00:00Z"},
		},
		{
			ID:     "doc-loser",
			Source: map[string]any{"updated_at": "2023-01-01T00:00:00Z"},
		},
	}

	expectedQuery := map[string]any{
		"size":                janitorMaxDuplicates + 1,
		"seq_no_primary_term": true,
		"sort": []map[string]any{
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at"},
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"object_ref": objectRef}},
					{"term": map[string]any{"latest": true}},
				},
			},
		},
	}
	mockRepo.On("SearchWithVersions", ctx, "test-index", expectedQuery).Return(mockDocs, nil)

	var result string
	assert.NotPanics(t, func() {
		result = service.processItem(ctx, &objectRef)
	})

	mockRepo.AssertNotCalled(t, "UpdateWithOptimisticLock", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockRepo.AssertExpectations(t)

	// doc-loser's update was skipped (missing version metadata), not
	// resolved: it stays latest=true, so this must not be reported as
	// "conflict_resolved" or the stale duplicate goes unnoticed.
	assert.Equal(t, "error", result)
}

func TestCleanupRepository_StartStopItemLoop(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the item loop
	service.StartItemLoop(ctx)

	// Verify it's running
	assert.True(t, service.IsRunning())

	// Stop the service
	service.Shutdown()

	// Wait longer for shutdown to complete and ensure all goroutines are done
	time.Sleep(200 * time.Millisecond)

	// Verify it's no longer running
	assert.False(t, service.IsRunning())
}

func TestCleanupRepository_ProcessQueuedItem(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "test-index")

	// Clear the channel first
	for len(globalJanitorChan) > 0 {
		<-globalJanitorChan
	}

	ctx := context.Background()

	// Test async retry
	service.asyncRetry(ctx, "test-object", "test-doc")

	// Wait up to 12 seconds for the async retry (production uses 5-10 second delays)
	retryFound := false
	for i := 0; i < 120; i++ { // Check every 100ms for 12 seconds
		if len(globalJanitorChan) > 0 {
			retryFound = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify the item was requeued
	assert.True(t, retryFound, "Object should be requeued for retry")

	if retryFound {
		// Verify the queued item is correct
		queuedItem := <-globalJanitorChan
		assert.Equal(t, "test-object", *queuedItem)
	}
}
