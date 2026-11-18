// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package cleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

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
			{"audit_source_revision": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "long"}},
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at", "audit_source_revision"},
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

// TestCleanupRepository_QuotedObjectRef preserves literal IDs in the structured
// query and retires the legacy live duplicate after an audited delete.
func TestCleanupRepository_QuotedObjectRef(t *testing.T) {
	for _, audited := range []bool{false, true} {
		t.Run(fmt.Sprintf("audited=%t", audited), func(t *testing.T) {
			mockRepo := &MockTransactionRepository{}
			logger, _ := logging.TestLogger(t)
			service := NewCleanupRepository(mockRepo, logger, "resources")
			ctx := context.Background()
			objectRef := `fixture_resource:resource"child\leaf`
			seq, term := int64(1), int64(1)
			docs := []contracts.VersionedDocument{
				{ID: "legacy-live", SeqNo: &seq, PrimaryTerm: &term,
					Source: map[string]any{"updated_at": "2026-11-05T11:00:00Z"}},
				{ID: objectRef, SeqNo: &seq, PrimaryTerm: &term,
					Source: map[string]any{"deleted_at": "2026-11-05T12:00:00Z", "audit_source_revision": 2}},
			}
			mockRepo.On("SearchWithVersions", ctx, "resources", mock.MatchedBy(func(query map[string]any) bool {
				// Check the wire representation as well as the map: quotes and
				// backslashes must remain one literal keyword term, not query syntax.
				encoded, err := json.Marshal(query)
				require.NoError(t, err)
				var decoded map[string]any
				require.NoError(t, json.Unmarshal(encoded, &decoded))
				must := decoded["query"].(map[string]any)["bool"].(map[string]any)["must"].([]any)
				return len(must) == 2 && must[0].(map[string]any)["term"].(map[string]any)["object_ref"] == objectRef &&
					must[1].(map[string]any)["term"].(map[string]any)["latest"] == true
			})).Return(docs, nil).Once()
			mockRepo.On("UpdateWithOptimisticLock", ctx, "resources", "legacy-live", mock.Anything,
				&contracts.OptimisticUpdateParams{SeqNo: &seq, PrimaryTerm: &term}).Run(func(args mock.Arguments) {
				var update map[string]any
				require.NoError(t, json.NewDecoder(args.Get(3).(io.Reader)).Decode(&update))
				require.Equal(t, false, update["doc"].(map[string]any)["latest"])
			}).Return(nil).Once()
			require.Equal(t, "conflict_resolved", service.processItemWithPolicy(ctx, janitorRequest{objectRef: objectRef, requireAuditedCanonical: audited}))
			mockRepo.AssertExpectations(t)
			mockRepo.AssertNotCalled(t, "UpdateWithOptimisticLock", ctx, "resources", objectRef, mock.Anything, mock.Anything)
		})
	}
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
			{"audit_source_revision": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "long"}},
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at", "audit_source_revision"},
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

func TestCleanupRepository_PrefersAuditedCanonicalOverLegacyTombstone(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(mockRepo, logger, "resources")
	ctx := context.Background()
	objectRef := "fixture_resource:fixture-resource"
	legacySeq, auditedSeq, term := int64(1), int64(2), int64(1)
	docs := []contracts.VersionedDocument{
		{ID: "legacy-tombstone", SeqNo: &legacySeq, PrimaryTerm: &term,
			Source: map[string]any{"deleted_at": "2026-11-05T12:00:00Z"}},
		{ID: objectRef, SeqNo: &auditedSeq, PrimaryTerm: &term,
			Source: map[string]any{"updated_at": "2026-11-05T11:00:00Z", "audit_source_revision": 7}},
	}
	mockRepo.On("SearchWithVersions", ctx, "resources", mock.Anything).Return(docs, nil)
	mockRepo.On("UpdateWithOptimisticLock", ctx, "resources", "legacy-tombstone", mock.Anything,
		&contracts.OptimisticUpdateParams{SeqNo: &legacySeq, PrimaryTerm: &term}).Return(nil)
	assert.Equal(t, "conflict_resolved", service.processItem(ctx, &objectRef))
	mockRepo.AssertExpectations(t)
	mockRepo.AssertNotCalled(t, "UpdateWithOptimisticLock", ctx, "resources", objectRef, mock.Anything, mock.Anything)
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
			{"audit_source_revision": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "long"}},
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at", "audit_source_revision"},
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
	service.processItem(ctx, &objectRef)

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
			{"audit_source_revision": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "long"}},
			{"deleted_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
			{"updated_at": map[string]any{"order": "desc", "missing": "_last", "unmapped_type": "date"}},
		},
		"_source": []string{"created_at", "updated_at", "deleted_at", "audit_source_revision"},
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

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); service.retryWG.Wait() }()
	request := janitorRequest{objectRef: "test-object", requireAuditedCanonical: true, visibilityAttempts: 3}
	mockRepo.On("SearchWithVersions", mock.Anything, "test-index", mock.Anything).Return([]contracts.VersionedDocument(nil), nil).Once()
	assert.Equal(t, "retry_scheduled", service.processItemWithPolicy(ctx, request))

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
		assert.Equal(t, "test-object", queuedItem.objectRef)
		assert.True(t, queuedItem.requireAuditedCanonical)
		assert.Equal(t, 4, queuedItem.visibilityAttempts)
	}
}

// TestCleanupRepository_AuditedVisibility defers all reconciliation until the
// guarded canonical document appears, even when legacy hits already exist.
func TestCleanupRepository_AuditedVisibility(t *testing.T) {
	for _, tt := range []struct {
		name string
		docs []contracts.VersionedDocument
	}{
		{"no search-visible document", nil},
		{"one legacy live document", []contracts.VersionedDocument{{ID: "legacy-live"}}},
		{"multiple legacy copies", []contracts.VersionedDocument{{ID: "legacy-live"}, {ID: "legacy-tombstone"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &MockTransactionRepository{}
			logger, _ := logging.TestLogger(t)
			service := NewCleanupRepository(mockRepo, logger, "test-index")
			ctx, cancel := context.WithCancel(context.Background())
			defer func() { cancel(); service.retryWG.Wait() }()
			mockRepo.On("SearchWithVersions", mock.Anything, "test-index", mock.Anything).Return(tt.docs, nil).Once()
			request := janitorRequest{objectRef: "fixture_resource:fixture-resource", requireAuditedCanonical: true}
			require.Equal(t, "retry_scheduled", service.processItemWithPolicy(ctx, request))
			mockRepo.AssertNotCalled(t, "UpdateWithOptimisticLock", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			mockRepo.AssertExpectations(t)
		})
	}
}

// TestCleanupRepository_VisibilityRetryLimit checks bounded escalation and still
// permits reconciliation when the canonical document appears on the final try.
func TestCleanupRepository_VisibilityRetryLimit(t *testing.T) {
	for _, tt := range []struct {
		name     string
		attempts int
		visible  bool
		result   string
	}{
		{"budget remains", janitorMaxVisibilityRetries - 1, false, "retry_scheduled"},
		{"exhausted", janitorMaxVisibilityRetries, false, "error"},
		{"over budget", janitorMaxVisibilityRetries + 1, false, "error"},
		{"canonical visible on final attempt", janitorMaxVisibilityRetries, true, "skipped"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &MockTransactionRepository{}
			logger, logs := logging.TestLogger(t)
			service := NewCleanupRepository(mockRepo, logger, "test-index")
			ctx, cancel := context.WithCancel(context.Background())
			request := janitorRequest{objectRef: "fixture_resource:fixture-resource", requireAuditedCanonical: true, visibilityAttempts: tt.attempts}
			defer func() { cancel(); service.retryWG.Wait() }()
			var docs []contracts.VersionedDocument
			if tt.visible {
				docs = []contracts.VersionedDocument{{ID: request.objectRef, Source: map[string]any{"audit_source_revision": 2}}}
			}
			mockRepo.On("SearchWithVersions", mock.Anything, "test-index", mock.Anything).Return(docs, nil).Once()
			require.Equal(t, tt.result, service.processItemWithPolicy(ctx, request))
			cancel()
			service.retryWG.Wait()
			if tt.result == "error" {
				require.Contains(t, logs.String(), `"level":"WARN"`)
				require.Contains(t, logs.String(), "visibility retries exhausted; reconciliation required")
				require.Contains(t, logs.String(), request.objectRef)
				require.NotContains(t, logs.String(), "Scheduling janitor retry")
			} else {
				require.NotContains(t, logs.String(), "visibility retries exhausted")
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

// TestCleanupRepository_CheckAuditedItem keeps audited visibility requirements
// on queued work without changing the legacy CheckItem policy.
func TestCleanupRepository_CheckAuditedItem(t *testing.T) {
	for len(globalJanitorChan) > 0 {
		<-globalJanitorChan
	}
	logger, _ := logging.TestLogger(t)
	service := NewCleanupRepository(&MockTransactionRepository{}, logger, "test-index")
	service.CheckAuditedItem("fixture_resource:audited")
	service.CheckItem("fixture_resource:legacy")
	first, second := <-globalJanitorChan, <-globalJanitorChan
	require.True(t, first.requireAuditedCanonical)
	require.Equal(t, "fixture_resource:audited", first.objectRef)
	require.False(t, second.requireAuditedCanonical)
	require.Equal(t, "fixture_resource:legacy", second.objectRef)
}
