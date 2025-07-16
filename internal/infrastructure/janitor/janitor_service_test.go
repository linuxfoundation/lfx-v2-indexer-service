package janitor

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockTransactionRepository implements the repositories.TransactionRepository interface for testing
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

func (m *MockTransactionRepository) BulkIndex(ctx context.Context, operations []repositories.BulkOperation) error {
	args := m.Called(ctx, operations)
	return args.Error(0)
}

func (m *MockTransactionRepository) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockTransactionRepository) UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *repositories.OptimisticUpdateParams) error {
	args := m.Called(ctx, index, docID, body, params)
	return args.Error(0)
}

func (m *MockTransactionRepository) SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]repositories.VersionedDocument, error) {
	args := m.Called(ctx, index, query)
	return args.Get(0).([]repositories.VersionedDocument), args.Error(1)
}

func TestNewJanitorService(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	assert.NotNil(t, service)
	assert.Equal(t, "test-index", service.index)
	assert.False(t, service.IsRunning())
}

func TestJanitorService_CheckItem(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	// Clear the channel first
	for len(globalJanitorChan) > 0 {
		<-globalJanitorChan
	}

	initialOverflows := janitorOverflows.Value()

	// Test successful queueing
	service.CheckItem("test-object-1")
	assert.Equal(t, 1, len(globalJanitorChan))

	// Fill up the channel to test overflow
	for i := 0; i < 49; i++ { // Channel capacity is 50, we already have 1
		service.CheckItem("overflow-test")
	}
	assert.Equal(t, 50, len(globalJanitorChan))

	// This should cause an overflow
	service.CheckItem("overflow-item")
	assert.Equal(t, initialOverflows+1, janitorOverflows.Value())
}

func TestJanitorService_WinnerDetermination_DeletedDocument(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	seqNo1, seqNo2, seqNo3 := int64(1), int64(2), int64(3)
	primaryTerm := int64(1)

	// Create test documents with one deleted
	docs := []repositories.VersionedDocument{
		{
			ID:          "doc1",
			SeqNo:       &seqNo1,
			PrimaryTerm: &primaryTerm,
			Source: map[string]interface{}{
				"updated_at": "2023-01-01T12:00:00Z",
			},
		},
		{
			ID:          "doc2",
			SeqNo:       &seqNo2,
			PrimaryTerm: &primaryTerm,
			Source: map[string]interface{}{
				"deleted_at": "2023-01-02T12:00:00Z",
				"updated_at": "2023-01-01T10:00:00Z",
			},
		},
		{
			ID:          "doc3",
			SeqNo:       &seqNo3,
			PrimaryTerm: &primaryTerm,
			Source: map[string]interface{}{
				"updated_at": "2023-01-01T15:00:00Z", // Latest, but deleted wins
			},
		},
	}

	// Mock the search to return these documents
	mockRepo.On("SearchWithVersions", mock.Anything, "test-index", mock.Anything).Return(docs, nil)

	// Mock updates for non-winning documents (doc1 and doc3)
	mockRepo.On("UpdateWithOptimisticLock", mock.Anything, "test-index", "doc1", mock.Anything, &repositories.OptimisticUpdateParams{
		SeqNo:       &seqNo1,
		PrimaryTerm: &primaryTerm,
	}).Return(nil)

	mockRepo.On("UpdateWithOptimisticLock", mock.Anything, "test-index", "doc3", mock.Anything, &repositories.OptimisticUpdateParams{
		SeqNo:       &seqNo3,
		PrimaryTerm: &primaryTerm,
	}).Return(nil)

	ctx := context.Background()
	objectRef := "test-object"

	// Process the item
	service.processItem(ctx, &objectRef)

	// Verify that the correct updates were called
	mockRepo.AssertExpectations(t)
}

func TestJanitorService_WinnerDetermination_LatestUpdated(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	seqNo1, seqNo2, seqNo3 := int64(1), int64(2), int64(3)
	primaryTerm := int64(1)

	// Create test documents without deletions
	docs := []repositories.VersionedDocument{
		{
			ID:          "doc1",
			SeqNo:       &seqNo1,
			PrimaryTerm: &primaryTerm,
			Source: map[string]interface{}{
				"updated_at": "2023-01-01T12:00:00Z",
			},
		},
		{
			ID:          "doc2",
			SeqNo:       &seqNo2,
			PrimaryTerm: &primaryTerm,
			Source: map[string]interface{}{
				"updated_at": "2023-01-01T10:00:00Z",
			},
		},
		{
			ID:          "doc3",
			SeqNo:       &seqNo3,
			PrimaryTerm: &primaryTerm,
			Source: map[string]interface{}{
				"updated_at": "2023-01-01T15:00:00Z", // Latest should win
			},
		},
	}

	// Mock the search to return these documents
	mockRepo.On("SearchWithVersions", mock.Anything, "test-index", mock.Anything).Return(docs, nil)

	// Mock updates for non-winning documents (doc1 and doc2)
	mockRepo.On("UpdateWithOptimisticLock", mock.Anything, "test-index", "doc1", mock.Anything, &repositories.OptimisticUpdateParams{
		SeqNo:       &seqNo1,
		PrimaryTerm: &primaryTerm,
	}).Return(nil)

	mockRepo.On("UpdateWithOptimisticLock", mock.Anything, "test-index", "doc2", mock.Anything, &repositories.OptimisticUpdateParams{
		SeqNo:       &seqNo2,
		PrimaryTerm: &primaryTerm,
	}).Return(nil)

	ctx := context.Background()
	objectRef := "test-object"

	// Process the item
	service.processItem(ctx, &objectRef)

	// Verify that the correct updates were called (doc3 is the winner)
	mockRepo.AssertExpectations(t)
}

func TestJanitorService_VersionConflictRetry(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	seqNo1, seqNo2 := int64(1), int64(2)
	primaryTerm := int64(1)

	// Create test documents with duplicates to trigger updates
	docs := []repositories.VersionedDocument{
		{
			ID:          "doc1",
			SeqNo:       &seqNo1,
			PrimaryTerm: &primaryTerm,
			Source: map[string]interface{}{
				"updated_at": "2023-01-01T10:00:00Z",
			},
		},
		{
			ID:          "doc2",
			SeqNo:       &seqNo2,
			PrimaryTerm: &primaryTerm,
			Source: map[string]interface{}{
				"updated_at": "2023-01-01T12:00:00Z", // Winner
			},
		},
	}

	// Mock the search to return multiple documents
	mockRepo.On("SearchWithVersions", mock.Anything, "test-index", mock.Anything).Return(docs, nil)

	// Mock version conflict on first attempt for doc1 (loser)
	versionConflictError := &repositories.VersionConflictError{
		DocumentID: "doc1",
		Err:        fmt.Errorf("version conflict"),
	}
	mockRepo.On("UpdateWithOptimisticLock", mock.Anything, "test-index", "doc1", mock.Anything, &repositories.OptimisticUpdateParams{
		SeqNo:       &seqNo1,
		PrimaryTerm: &primaryTerm,
	}).Return(versionConflictError)

	ctx := context.Background()
	objectRef := "test-object"

	// Clear the channel
	for len(globalJanitorChan) > 0 {
		<-globalJanitorChan
	}

	// Process the item (should trigger async retry)
	service.processItem(ctx, &objectRef)

	// Wait up to 12 seconds for the async retry (production uses 5-10 second delays)
	retryFound := false
	for i := 0; i < 120; i++ { // Check every 100ms for 12 seconds
		if len(globalJanitorChan) > 0 {
			retryFound = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify that the item was requeued
	assert.True(t, retryFound, "Item should be requeued after version conflict")

	mockRepo.AssertExpectations(t)
}

func TestJanitorService_NoDuplicates(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	seqNo := int64(1)
	primaryTerm := int64(1)

	// Single document (no duplicates)
	docs := []repositories.VersionedDocument{
		{
			ID:          "doc1",
			SeqNo:       &seqNo,
			PrimaryTerm: &primaryTerm,
			Source: map[string]interface{}{
				"updated_at": "2023-01-01T12:00:00Z",
			},
		},
	}

	// Mock the search to return single document
	mockRepo.On("SearchWithVersions", mock.Anything, "test-index", mock.Anything).Return(docs, nil)

	ctx := context.Background()
	objectRef := "test-object"

	// Process the item
	service.processItem(ctx, &objectRef)

	// Verify no updates were attempted (no duplicates)
	mockRepo.AssertNotCalled(t, "UpdateWithOptimisticLock")
	mockRepo.AssertExpectations(t)
}

func TestJanitorService_InvalidObjectRef(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	ctx := context.Background()

	// Test nil object ref
	service.processItem(ctx, nil)

	// Test empty object ref
	emptyRef := ""
	service.processItem(ctx, &emptyRef)

	// Test invalid object ref with quotes
	invalidRef := `test"object`
	service.processItem(ctx, &invalidRef)

	// Verify no searches were attempted
	mockRepo.AssertNotCalled(t, "SearchWithVersions")
}

func TestJanitorService_StartStopLifecycle(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	// Clear the global channel to avoid interference from other tests
	for len(globalJanitorChan) > 0 {
		<-globalJanitorChan
	}

	// Set up default mock to handle any unexpected calls gracefully
	mockRepo.On("SearchWithVersions", mock.Anything, mock.Anything, mock.Anything).
		Return([]repositories.VersionedDocument{}, nil).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initially not running
	assert.False(t, service.IsRunning())

	// Start the service
	service.StartItemLoop(ctx)

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)
	assert.True(t, service.IsRunning())

	// Starting again should be a no-op
	service.StartItemLoop(ctx)
	assert.True(t, service.IsRunning())

	// Stop the service
	service.Shutdown()

	// Give it a moment to stop
	time.Sleep(50 * time.Millisecond)
	assert.False(t, service.IsRunning())

	// Shutdown again should be a no-op
	service.Shutdown()
	assert.False(t, service.IsRunning())
}

func TestJanitorService_GetMetrics(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	metrics := service.GetMetrics()

	assert.Equal(t, 50, metrics["queue_size"])
	assert.Equal(t, "test-index", metrics["index"])
	assert.Equal(t, false, metrics["is_running"])
	assert.NotNil(t, metrics["queue_len"])
	assert.NotNil(t, metrics["overflows"])
}

func TestJanitorService_SearchError(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	// Mock search error
	mockRepo.On("SearchWithVersions", mock.Anything, "test-index", mock.Anything).Return(
		[]repositories.VersionedDocument{}, assert.AnError)

	ctx := context.Background()
	objectRef := "test-object"

	// Process the item (should handle search error gracefully)
	service.processItem(ctx, &objectRef)

	// Verify no updates were attempted due to search error
	mockRepo.AssertNotCalled(t, "UpdateWithOptimisticLock")
	mockRepo.AssertExpectations(t)
}

func TestJanitorService_UpdateBodyGeneration(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	seqNo := int64(123)
	primaryTerm := int64(1)
	doc := repositories.VersionedDocument{
		ID:          "test-doc",
		SeqNo:       &seqNo,
		PrimaryTerm: &primaryTerm,
		Source:      map[string]interface{}{},
	}

	// Capture the body parameter to verify its content
	var capturedBody io.Reader
	mockRepo.On("UpdateWithOptimisticLock",
		mock.Anything,
		"test-index",
		"test-doc",
		mock.MatchedBy(func(body io.Reader) bool {
			capturedBody = body
			return true
		}),
		&repositories.OptimisticUpdateParams{
			SeqNo:       &seqNo,
			PrimaryTerm: &primaryTerm,
		}).Return(nil)

	ctx := context.Background()
	err := service.updateLatestFlag(ctx, doc, false, "test-object")

	assert.NoError(t, err)

	// Verify the body content
	bodyBytes, err := io.ReadAll(capturedBody)
	assert.NoError(t, err)
	expectedBody := `{"doc":{"latest":false}}`
	assert.JSONEq(t, expectedBody, string(bodyBytes))

	mockRepo.AssertExpectations(t)
}

func TestJanitorService_AsyncRetryMechanism(t *testing.T) {
	mockRepo := &MockTransactionRepository{}
	service := NewJanitorService(mockRepo, "test-index")

	// Clear the channel
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
