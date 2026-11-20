// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/cleanup"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

// Test helper functions
func setupTestLogger() *slog.Logger {
	return logging.NewLogger(true) // Enable debug mode
}

// Mock implementations
type MockMessagingRepository struct {
	mock.Mock
}

func (m *MockMessagingRepository) Subscribe(ctx context.Context, subject string, handler contracts.MessageHandler) error {
	args := m.Called(ctx, subject, handler)
	return args.Error(0)
}

func (m *MockMessagingRepository) QueueSubscribe(ctx context.Context, subject, queue string, handler contracts.MessageHandler) error {
	args := m.Called(ctx, subject, queue, handler)
	return args.Error(0)
}

func (m *MockMessagingRepository) QueueSubscribeWithReply(ctx context.Context, subject, queue string, handler contracts.MessageHandlerWithReply) error {
	args := m.Called(ctx, subject, queue, handler)
	return args.Error(0)
}

func (m *MockMessagingRepository) Publish(ctx context.Context, subject string, data []byte) error {
	args := m.Called(ctx, subject, data)
	return args.Error(0)
}

func (m *MockMessagingRepository) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMessagingRepository) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockMessagingRepository) DrainWithTimeout() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMessagingRepository) ValidateToken(ctx context.Context, token string) (*contracts.Principal, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	principal, ok := args.Get(0).(*contracts.Principal)
	if !ok {
		return nil, args.Error(1)
	}
	return principal, args.Error(1)
}

func (m *MockMessagingRepository) ParsePrincipals(ctx context.Context, headers map[string]string) ([]contracts.Principal, error) {
	args := m.Called(ctx, headers)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	principals, ok := args.Get(0).([]contracts.Principal)
	if !ok {
		return nil, args.Error(1)
	}
	return principals, args.Error(1)
}

func (m *MockMessagingRepository) ConsumeWithJetStream(ctx context.Context, streamName string, filterSubjects []string, handler func(context.Context, []byte, string) error) error {
	args := m.Called(ctx, streamName, filterSubjects, handler)
	return args.Error(0)
}

type MockStorageRepository struct {
	mock.Mock
}

func (m *MockStorageRepository) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	args := m.Called(ctx, index, docID, body)
	return args.Error(0)
}

func (m *MockStorageRepository) Search(ctx context.Context, index string, query map[string]any) ([]map[string]any, error) {
	args := m.Called(ctx, index, query)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]any), args.Error(1)
}

func (m *MockStorageRepository) Get(ctx context.Context, index string, docID string) (map[string]any, error) {
	args := m.Called(ctx, index, docID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]any), args.Error(1)
}

func (m *MockStorageRepository) Update(ctx context.Context, index string, docID string, body io.Reader) error {
	args := m.Called(ctx, index, docID, body)
	return args.Error(0)
}

func (m *MockStorageRepository) Delete(ctx context.Context, index string, docID string) error {
	args := m.Called(ctx, index, docID)
	return args.Error(0)
}

func (m *MockStorageRepository) BulkIndex(ctx context.Context, operations []contracts.BulkOperation) ([]error, error) {
	args := m.Called(ctx, operations)
	itemErrors, _ := args.Get(0).([]error)
	return itemErrors, args.Error(1)
}

func (m *MockStorageRepository) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockStorageRepository) UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *contracts.OptimisticUpdateParams) error {
	args := m.Called(ctx, index, docID, body, params)
	return args.Error(0)
}

func (m *MockStorageRepository) SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]contracts.VersionedDocument, error) {
	args := m.Called(ctx, index, query)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]contracts.VersionedDocument), args.Error(1)
}

// Test setup with actual services
func setupTestMessageProcessor() (*MessageProcessor, *MockMessagingRepository, *MockStorageRepository) {
	logger := setupTestLogger()
	mockMessagingRepo := &MockMessagingRepository{}
	mockStorageRepo := &MockStorageRepository{}

	// Create actual services
	indexerService := services.NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)
	cleanupRepo := cleanup.NewCleanupRepository(mockStorageRepo, logger, constants.DefaultIndex)

	mp := NewMessageProcessor(indexerService, mockMessagingRepo, cleanupRepo, logger)
	return mp, mockMessagingRepo, mockStorageRepo
}

// Test constructor
func TestNewMessageProcessor(t *testing.T) {
	logger := setupTestLogger()
	mockMessagingRepo := &MockMessagingRepository{}
	mockStorageRepo := &MockStorageRepository{}

	indexerService := services.NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)
	cleanupRepo := cleanup.NewCleanupRepository(mockStorageRepo, logger, constants.DefaultIndex)

	mp := NewMessageProcessor(indexerService, mockMessagingRepo, cleanupRepo, logger)

	assert.NotNil(t, mp)
	assert.NotNil(t, mp.indexerService)
	assert.NotNil(t, mp.messagingRepo)
	assert.NotNil(t, mp.cleanupRepository)
	assert.NotNil(t, mp.logger)
	assert.Equal(t, constants.DefaultIndex, mp.index)
	assert.Equal(t, constants.DefaultQueue, mp.queue)
	assert.Equal(t, constants.AllSubjects, mp.indexingSubject)
}

func TestMessageProcessor_Configure(t *testing.T) {
	mp, _, _ := setupTestMessageProcessor()

	mp.Configure("custom-index", "custom-queue", "custom.index.>")

	assert.Equal(t, "custom-index", mp.index)
	assert.Equal(t, "custom-queue", mp.queue)
	assert.Equal(t, "custom.index.>", mp.indexingSubject)
}

// Test ProcessIndexingMessage success case
func TestMessageProcessor_ProcessIndexingMessage_Success(t *testing.T) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	// Test data
	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
		"headers": map[string]string{
			"x-trace-id":    "trace-123",
			"authorization": "Bearer test-token",
		},
		"indexing_config": map[string]any{
			"object_id":              "test-123",
			"access_check_object":    "project:test-123",
			"access_check_relation":  "viewer",
			"history_check_object":   "project:test-123",
			"history_check_relation": "viewer",
		},
	}
	data, err := json.Marshal(testData)
	require.NoError(t, err, "Failed to marshal test data")
	subject := "lfx.index.project"

	// Mock expectations
	mockStorageRepo.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockMessagingRepo.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{}, nil)
	mockMessagingRepo.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute
	err = mp.ProcessIndexingMessage(ctx, data, subject)

	// Assertions
	assert.NoError(t, err)
	mockStorageRepo.AssertExpectations(t)
	mockMessagingRepo.AssertExpectations(t)
}

func TestMessageProcessor_ProcessIndexingMessage_UsesConfiguredIndex(t *testing.T) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	mp.Configure("configured-index", "", "")
	ctx := context.Background()

	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
		"indexing_config": map[string]any{
			"object_id":              "test-123",
			"access_check_object":    "project:test-123",
			"access_check_relation":  "viewer",
			"history_check_object":   "project:test-123",
			"history_check_relation": "viewer",
		},
	}
	data, err := json.Marshal(testData)
	require.NoError(t, err)

	mockStorageRepo.On("Index", mock.Anything, "configured-index", "project:test-123", mock.Anything).Return(nil)
	mockMessagingRepo.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{}, nil)
	mockMessagingRepo.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	err = mp.ProcessIndexingMessage(ctx, data, "lfx.index.project")

	assert.NoError(t, err)
	mockStorageRepo.AssertExpectations(t)
	mockMessagingRepo.AssertExpectations(t)
}

// Test ProcessIndexingMessage with invalid JSON
func TestMessageProcessor_ProcessIndexingMessage_InvalidJSON(t *testing.T) {
	mp, _, _ := setupTestMessageProcessor()
	ctx := context.Background()

	// Invalid JSON data
	invalidData := []byte(`{invalid json}`)
	subject := "lfx.index.project"

	// Execute
	err := mp.ProcessIndexingMessage(ctx, invalidData, subject)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal message data")
}

// Test ProcessIndexingMessage with invalid subject
func TestMessageProcessor_ProcessIndexingMessage_InvalidSubject(t *testing.T) {
	mp, _, _ := setupTestMessageProcessor()
	ctx := context.Background()

	// Valid JSON but invalid subject
	testData := map[string]any{
		"action": "created",
		"data":   map[string]any{"id": "test-123"},
	}
	data, err := json.Marshal(testData)
	require.NoError(t, err, "Failed to marshal test data")
	subject := "invalid.subject"

	// Execute
	err = mp.ProcessIndexingMessage(ctx, data, subject)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid V2 subject format")
}

// Test ProcessIndexingMessage with processing error
func TestMessageProcessor_ProcessIndexingMessage_ProcessingError(t *testing.T) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	// Test data
	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
		"indexing_config": map[string]any{
			"object_id":              "test-123",
			"access_check_object":    "project:test-123",
			"access_check_relation":  "viewer",
			"history_check_object":   "project:test-123",
			"history_check_relation": "viewer",
		},
	}
	data, _ := json.Marshal(testData)
	subject := "lfx.index.project"

	// Mock expectations with error
	mockStorageRepo.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("storage error"))
	mockMessagingRepo.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{}, nil)

	// Execute
	err := mp.ProcessIndexingMessage(ctx, data, subject)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to process transaction")
	mockStorageRepo.AssertExpectations(t)
	mockMessagingRepo.AssertExpectations(t)
}

// Test ProcessIndexingMessage with missing authorization header
func TestMessageProcessor_ProcessIndexingMessage_MissingAuthHeader(t *testing.T) {
	mp, _, _ := setupTestMessageProcessor()
	ctx := context.Background()

	// Test data without authorization header
	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
	}
	data, _ := json.Marshal(testData)
	subject := "lfx.index.project"

	// Execute
	err := mp.ProcessIndexingMessage(ctx, data, subject)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "authorization header is required")
}

func TestMessageProcessor_RetiredProtocolRejected(t *testing.T) {
	for _, tt := range []struct {
		name, subject, action, wantError string
		headers                          map[string]string
	}{
		{"retired subject", "lfx.v1.index.project", "created", "invalid V2 subject", map[string]string{constants.AuthorizationHeader: "Bearer fixture-token"}},
		{"retired subject and action", "lfx.v1.index.project", "create", "invalid V2 subject", nil},
		{"present-tense create", constants.ProjectSubject, "create", "invalid V2 transaction action", nil},
		{"present-tense update", constants.ProjectSubject, "update", "invalid V2 transaction action", nil},
		{"present-tense delete", constants.ProjectSubject, "delete", "invalid V2 transaction action", nil},
		{"unsigned legacy headers", constants.ProjectSubject, "created", "authorization header is required", map[string]string{"x-username": "fixture-user", "x-email": "user-1@example.com"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			processor, messaging, storage := setupTestMessageProcessor()
			payload, err := json.Marshal(map[string]any{
				"action": tt.action, "headers": tt.headers, "data": map[string]any{"id": "fixture-resource"},
				"indexing_config": map[string]string{"object_id": "fixture-resource", "access_check_object": "project:fixture-resource", "access_check_relation": "viewer", "history_check_object": "project:fixture-resource", "history_check_relation": "auditor"},
			})
			require.NoError(t, err)
			err = processor.ProcessIndexingMessage(context.Background(), payload, tt.subject)
			require.ErrorContains(t, err, tt.wantError)
			storage.AssertNumberOfCalls(t, "Index", 0)
			messaging.AssertNumberOfCalls(t, "Publish", 0)
			messaging.AssertNumberOfCalls(t, "ParsePrincipals", 0)
		})
	}
}

// Test StartSubscriptions success case
func TestMessageProcessor_StartSubscriptions_Success(t *testing.T) {
	mp, mockMessagingRepo, _ := setupTestMessageProcessor()
	ctx := context.Background()
	mp.Configure("", "custom-queue", "custom.index.>")

	// Mock expectations
	mockMessagingRepo.On("QueueSubscribeWithReply", mock.Anything, "custom.index.>", "custom-queue", mock.Anything).Return(nil)

	// Execute
	err := mp.StartSubscriptions(ctx)

	// Assertions
	assert.NoError(t, err)
	mockMessagingRepo.AssertExpectations(t)
	mockMessagingRepo.AssertNumberOfCalls(t, "QueueSubscribeWithReply", 1)
}

// Test StartSubscriptions with V2 subscription error
func TestMessageProcessor_StartSubscriptions_V2Error(t *testing.T) {
	mp, mockMessagingRepo, _ := setupTestMessageProcessor()
	ctx := context.Background()

	// Mock expectations with V2 error
	mockMessagingRepo.On("QueueSubscribeWithReply", mock.Anything, constants.AllSubjects, constants.DefaultQueue, mock.Anything).Return(errors.New("subscription error"))

	// Execute
	err := mp.StartSubscriptions(ctx)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to subscribe to V2 messages")
	mockMessagingRepo.AssertExpectations(t)
}

// Test indexingHandler HandleWithReply with V2 message
func TestIndexingHandler_HandleWithReply_V2Message(t *testing.T) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	// Test data
	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
		"indexing_config": map[string]any{
			"object_id":              "test-123",
			"access_check_object":    "project:test-123",
			"access_check_relation":  "viewer",
			"history_check_object":   "project:test-123",
			"history_check_relation": "viewer",
		},
	}
	data, _ := json.Marshal(testData)
	subject := "lfx.index.project"

	// Mock reply function
	replyCalled := false
	replyFunc := func(response []byte) error {
		replyCalled = true
		assert.Equal(t, "OK", string(response))
		return nil
	}

	// Mock expectations
	mockStorageRepo.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockMessagingRepo.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{}, nil)
	mockMessagingRepo.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Create handler
	handler := &indexingHandler{useCase: mp}

	// Execute
	err := handler.HandleWithReply(ctx, data, subject, replyFunc)

	// Assertions
	assert.NoError(t, err)
	assert.True(t, replyCalled)
	mockStorageRepo.AssertExpectations(t)
	mockMessagingRepo.AssertExpectations(t)
}

// Test indexingHandler HandleWithReply with processing error
func TestIndexingHandler_HandleWithReply_ProcessingError(t *testing.T) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	// Test data
	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
		"indexing_config": map[string]any{
			"object_id":              "test-123",
			"access_check_object":    "project:test-123",
			"access_check_relation":  "viewer",
			"history_check_object":   "project:test-123",
			"history_check_relation": "viewer",
		},
	}
	data, _ := json.Marshal(testData)
	subject := "lfx.index.project"

	// Mock reply function
	replyCalled := false
	replyFunc := func(response []byte) error {
		replyCalled = true
		assert.Contains(t, string(response), "ERROR:")
		return nil
	}

	// Mock expectations with error
	mockStorageRepo.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("storage error"))
	mockMessagingRepo.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{}, nil)

	// Create handler
	handler := &indexingHandler{useCase: mp}

	// Execute
	err := handler.HandleWithReply(ctx, data, subject, replyFunc)

	// Assertions
	assert.Error(t, err)
	assert.True(t, replyCalled)
	mockStorageRepo.AssertExpectations(t)
}

// Test indexingHandler HandleWithReply without reply function
func TestIndexingHandler_HandleWithReply_NoReply(t *testing.T) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	// Test data
	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
		"indexing_config": map[string]any{
			"object_id":              "test-123",
			"access_check_object":    "project:test-123",
			"access_check_relation":  "viewer",
			"history_check_object":   "project:test-123",
			"history_check_relation": "viewer",
		},
	}
	data, _ := json.Marshal(testData)
	subject := "lfx.index.project"

	// Mock expectations
	mockStorageRepo.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockMessagingRepo.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{
		{Principal: "test-user", Email: "test@example.com"},
	}, nil)
	mockMessagingRepo.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Create handler
	handler := &indexingHandler{useCase: mp}

	// Execute (no reply function)
	err := handler.HandleWithReply(ctx, data, subject, nil)

	// Assertions
	assert.NoError(t, err)
	mockStorageRepo.AssertExpectations(t)
	mockMessagingRepo.AssertExpectations(t)
}

// Test that HandleWithReply sets refresh=wait_for in context when reply is provided
func TestIndexingHandler_HandleWithReply_SetsRefreshWaitFor_WithReply(t *testing.T) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":     "test-123",
			"name":   "Test Project",
			"public": true,
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
		"indexing_config": map[string]any{
			"object_id":              "test-123",
			"access_check_object":    "project:test-123",
			"access_check_relation":  "viewer",
			"history_check_object":   "project:test-123",
			"history_check_relation": "viewer",
		},
	}
	data, _ := json.Marshal(testData)
	subject := "lfx.index.project"

	replyFunc := func(_ []byte) error { return nil }

	// Index must be called with a context that has NeedsRefreshWaitFor=true
	mockStorageRepo.On("Index", mock.MatchedBy(func(c context.Context) bool {
		return logging.NeedsRefreshWaitFor(c)
	}), mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockMessagingRepo.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{}, nil)
	mockMessagingRepo.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	handler := &indexingHandler{useCase: mp}
	err := handler.HandleWithReply(ctx, data, subject, replyFunc)

	assert.NoError(t, err)
	mockStorageRepo.AssertExpectations(t)
	mockMessagingRepo.AssertExpectations(t)
}

// Test that HandleWithReply does NOT set refresh=wait_for when no reply
func TestIndexingHandler_HandleWithReply_NoRefreshWaitFor_WithoutReply(t *testing.T) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":     "test-123",
			"name":   "Test Project",
			"public": true,
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
		"indexing_config": map[string]any{
			"object_id":              "test-123",
			"access_check_object":    "project:test-123",
			"access_check_relation":  "viewer",
			"history_check_object":   "project:test-123",
			"history_check_relation": "viewer",
		},
	}
	data, _ := json.Marshal(testData)
	subject := "lfx.index.project"

	// Index must be called with a context that does NOT have NeedsRefreshWaitFor=true
	mockStorageRepo.On("Index", mock.MatchedBy(func(c context.Context) bool {
		return !logging.NeedsRefreshWaitFor(c)
	}), mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockMessagingRepo.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{
		{Principal: "test-user", Email: "test@example.com"},
	}, nil)
	mockMessagingRepo.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	handler := &indexingHandler{useCase: mp}
	err := handler.HandleWithReply(ctx, data, subject, nil)

	assert.NoError(t, err)
	mockStorageRepo.AssertExpectations(t)
	mockMessagingRepo.AssertExpectations(t)
}

// Test generateMessageID uniqueness
func TestMessageProcessor_GenerateMessageID(t *testing.T) {
	mp, _, _ := setupTestMessageProcessor()

	// Generate multiple IDs
	id1 := mp.generateMessageID()
	time.Sleep(1 * time.Microsecond) // Ensure time difference
	id2 := mp.generateMessageID()
	id3 := mp.generateMessageID()

	// Assertions
	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEmpty(t, id3)
	assert.NotEqual(t, id1, id2)
	assert.NotEqual(t, id2, id3)
	assert.NotEqual(t, id1, id3)
	assert.Contains(t, id1, "msg_")
	assert.Contains(t, id2, "msg_")
	assert.Contains(t, id3, "msg_")
}

// Test helper methods
func TestMessageProcessor_HelperMethods(t *testing.T) {
	mp, _, _ := setupTestMessageProcessor()

	t.Run("generateMessageID", func(t *testing.T) {
		// Test message ID generation through public interface
		ctx := context.Background()

		// Since we can't access the private method directly,
		// we test that the processor works correctly
		assert.NotNil(t, mp)
		assert.NotNil(t, mp.logger)

		// This ensures the processor is properly initialized
		// and can handle message processing
		_, cancel := context.WithTimeout(ctx, time.Second)
		cancel() // Immediately cancel to avoid blocking
	})
}

// Test edge cases
func TestMessageProcessor_EdgeCases(t *testing.T) {
	mp, _, _ := setupTestMessageProcessor()
	ctx := context.Background()

	t.Run("empty_data", func(t *testing.T) {
		err := mp.ProcessIndexingMessage(ctx, []byte{}, "lfx.index.project")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal message data")
	})

	t.Run("nil_data", func(t *testing.T) {
		err := mp.ProcessIndexingMessage(ctx, nil, "lfx.index.project")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal message data")
	})

	t.Run("empty_subject", func(t *testing.T) {
		testData := map[string]any{"action": "created", "data": map[string]any{"id": "test"}}
		data, _ := json.Marshal(testData)
		err := mp.ProcessIndexingMessage(ctx, data, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid V2 subject format")
	})

	t.Run("malformed_subject", func(t *testing.T) {
		testData := map[string]any{"action": "created", "data": map[string]any{"id": "test"}}
		data, _ := json.Marshal(testData)
		err := mp.ProcessIndexingMessage(ctx, data, "not.a.valid.subject")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid V2 subject format")
	})
}

// Integration test with real workflow
func TestMessageProcessor_IntegrationWorkflow(t *testing.T) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	// Setup subscription mocks
	mockMessagingRepo.On("QueueSubscribeWithReply", mock.Anything, constants.AllSubjects, constants.DefaultQueue, mock.Anything).Return(nil)

	// Setup storage mocks
	mockStorageRepo.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Setup ParsePrincipals mock for V2 messages
	mockMessagingRepo.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{}, nil)
	mockMessagingRepo.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Start subscriptions
	err := mp.StartSubscriptions(ctx)
	require.NoError(t, err)

	// Process V2 message
	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
		"indexing_config": map[string]any{
			"object_id":              "test-123",
			"access_check_object":    "project:test-123",
			"access_check_relation":  "viewer",
			"history_check_object":   "project:test-123",
			"history_check_relation": "viewer",
		},
	}
	data, _ := json.Marshal(testData)
	err = mp.ProcessIndexingMessage(ctx, data, "lfx.index.project")
	require.NoError(t, err)

	// Legacy-backed resource names still use the supported platform V2 protocol.
	err = mp.ProcessIndexingMessage(ctx, data, constants.IndexPrefix+"v1_meeting")
	require.NoError(t, err)

	// Verify all expectations
	mockMessagingRepo.AssertExpectations(t)
	mockStorageRepo.AssertExpectations(t)
}

// Performance benchmarks
func BenchmarkMessageProcessor_GenerateMessageID(b *testing.B) {
	mp, _, _ := setupTestMessageProcessor()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mp.generateMessageID()
	}
}

func BenchmarkMessageProcessor_ProcessIndexingMessage(b *testing.B) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	// Setup mocks
	mockStorageRepo.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockMessagingRepo.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Test data
	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
	}
	data, _ := json.Marshal(testData)
	subject := "lfx.index.project"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mp.ProcessIndexingMessage(ctx, data, subject)
	}
}

func BenchmarkIndexingHandler_HandleWithReply(b *testing.B) {
	mp, mockMessagingRepo, mockStorageRepo := setupTestMessageProcessor()
	ctx := context.Background()

	// Setup mocks
	mockStorageRepo.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockMessagingRepo.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Test data
	testData := map[string]any{
		"action": "created",
		"data": map[string]any{
			"id":   "test-123",
			"name": "Test Project",
		},
		"headers": map[string]string{
			"authorization": "Bearer test-token",
		},
	}
	data, _ := json.Marshal(testData)
	subject := "lfx.index.project"

	handler := &indexingHandler{useCase: mp}
	replyFunc := func(_ []byte) error { return nil }

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = handler.HandleWithReply(ctx, data, subject, replyFunc)
	}
}
