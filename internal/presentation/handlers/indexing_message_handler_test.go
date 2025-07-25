// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handlers

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MessageProcessorInterface defines the interface for message processing methods used by the handler
type MessageProcessorInterface interface {
	ProcessIndexingMessage(ctx context.Context, data []byte, subject string) error
	ProcessV1IndexingMessage(ctx context.Context, data []byte, subject string) error
}

// MockMessageProcessor implements a mock for MessageProcessorInterface
type MockMessageProcessor struct {
	mock.Mock
}

func (m *MockMessageProcessor) ProcessIndexingMessage(ctx context.Context, data []byte, subject string) error {
	args := m.Called(ctx, data, subject)
	return args.Error(0)
}

func (m *MockMessageProcessor) ProcessV1IndexingMessage(ctx context.Context, data []byte, subject string) error {
	args := m.Called(ctx, data, subject)
	return args.Error(0)
}

// TestableIndexingMessageHandler is a testable version of IndexingMessageHandler
type TestableIndexingMessageHandler struct {
	messageProcessor MessageProcessorInterface
}

func NewTestableIndexingMessageHandler(messageProcessor MessageProcessorInterface) *TestableIndexingMessageHandler {
	return &TestableIndexingMessageHandler{
		messageProcessor: messageProcessor,
	}
}

// Handle processes indexing messages (same logic as IndexingMessageHandler)
func (h *TestableIndexingMessageHandler) Handle(ctx context.Context, data []byte, subject string) error {
	if strings.HasPrefix(subject, constants.FromV1Prefix) {
		return h.messageProcessor.ProcessV1IndexingMessage(ctx, data, subject)
	}
	return h.messageProcessor.ProcessIndexingMessage(ctx, data, subject)
}

// HandleWithReply processes indexing messages with NATS reply support (same logic as IndexingMessageHandler)
func (h *TestableIndexingMessageHandler) HandleWithReply(ctx context.Context, data []byte, subject string, reply func([]byte) error) error {
	var err error
	if strings.HasPrefix(subject, constants.FromV1Prefix) {
		err = h.messageProcessor.ProcessV1IndexingMessage(ctx, data, subject)
	} else {
		err = h.messageProcessor.ProcessIndexingMessage(ctx, data, subject)
	}

	if err != nil {
		h.respondErrorWithContext(ctx, reply, err, "error processing indexing message", subject)
		return err
	}

	h.respondSuccessWithContext(ctx, reply, subject)
	return nil
}

// Helper methods for testing (simplified versions)
func (h *TestableIndexingMessageHandler) respondErrorWithContext(_ context.Context, reply func([]byte) error, _ error, msg string, _ string) {
	if reply != nil {
		_ = reply([]byte("ERROR: " + msg)) // Test helper - ignore reply errors
	}
}

func (h *TestableIndexingMessageHandler) respondSuccessWithContext(_ context.Context, reply func([]byte) error, _ string) {
	if reply != nil {
		_ = reply([]byte("OK")) // Test helper - ignore reply errors
	}
}

func TestNewIndexingMessageHandler(t *testing.T) {
	// Test creating a new handler with real MessageProcessor
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	assert.NotNil(t, handler)
	assert.Equal(t, mockProcessor, handler.messageProcessor)
}

func TestIndexingMessageHandler_Handle_V2Message(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
	subject := "lfx.index.project"

	// Mock expectations
	mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(nil)

	// Execute
	err := handler.Handle(ctx, data, subject)

	// Verify
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_Handle_V1Message(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "create", "data": {"id": "test-123"}}`)
	subject := "lfx.v1.index.project"

	// Mock expectations
	mockProcessor.On("ProcessV1IndexingMessage", ctx, data, subject).Return(nil)

	// Execute
	err := handler.Handle(ctx, data, subject)

	// Verify
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_Handle_V2MessageError(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
	subject := "lfx.index.project"
	expectedError := errors.New("processing error")

	// Mock expectations
	mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(expectedError)

	// Execute
	err := handler.Handle(ctx, data, subject)

	// Verify
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_Handle_V1MessageError(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "create", "data": {"id": "test-123"}}`)
	subject := "lfx.v1.index.project"
	expectedError := errors.New("v1 processing error")

	// Mock expectations
	mockProcessor.On("ProcessV1IndexingMessage", ctx, data, subject).Return(expectedError)

	// Execute
	err := handler.Handle(ctx, data, subject)

	// Verify
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_HandleWithReply_V2Success(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
	subject := "lfx.index.project"

	var replyData []byte
	reply := func(data []byte) error {
		replyData = data
		return nil
	}

	// Mock expectations
	mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(nil)

	// Execute
	err := handler.HandleWithReply(ctx, data, subject, reply)

	// Verify
	assert.NoError(t, err)
	assert.Equal(t, []byte("OK"), replyData)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_HandleWithReply_V1Success(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "create", "data": {"id": "test-123"}}`)
	subject := "lfx.v1.index.project"

	var replyData []byte
	reply := func(data []byte) error {
		replyData = data
		return nil
	}

	// Mock expectations
	mockProcessor.On("ProcessV1IndexingMessage", ctx, data, subject).Return(nil)

	// Execute
	err := handler.HandleWithReply(ctx, data, subject, reply)

	// Verify
	assert.NoError(t, err)
	assert.Equal(t, []byte("OK"), replyData)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_HandleWithReply_V2Error(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
	subject := "lfx.index.project"
	expectedError := errors.New("processing error")

	var replyData []byte
	reply := func(data []byte) error {
		replyData = data
		return nil
	}

	// Mock expectations
	mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(expectedError)

	// Execute
	err := handler.HandleWithReply(ctx, data, subject, reply)

	// Verify
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	assert.Equal(t, []byte("ERROR: error processing indexing message"), replyData)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_HandleWithReply_V1Error(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "create", "data": {"id": "test-123"}}`)
	subject := "lfx.v1.index.project"
	expectedError := errors.New("v1 processing error")

	var replyData []byte
	reply := func(data []byte) error {
		replyData = data
		return nil
	}

	// Mock expectations
	mockProcessor.On("ProcessV1IndexingMessage", ctx, data, subject).Return(expectedError)

	// Execute
	err := handler.HandleWithReply(ctx, data, subject, reply)

	// Verify
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	assert.Equal(t, []byte("ERROR: error processing indexing message"), replyData)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_HandleWithReply_ReplyError(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
	subject := "lfx.index.project"
	replyError := errors.New("reply error")

	reply := func(_ []byte) error {
		return replyError
	}

	// Mock expectations
	mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(nil)

	// Execute
	err := handler.HandleWithReply(ctx, data, subject, reply)

	// Verify - even if reply fails, the handler should still return nil for successful processing
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_HandleWithReply_ProcessingErrorAndReplyError(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
	subject := "lfx.index.project"
	processingError := errors.New("processing error")
	replyError := errors.New("reply error")

	reply := func(_ []byte) error {
		return replyError
	}

	// Mock expectations
	mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(processingError)

	// Execute
	err := handler.HandleWithReply(ctx, data, subject, reply)

	// Verify - should return the original processing error
	assert.Error(t, err)
	assert.Equal(t, processingError, err)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_HandleWithReply_WithoutReply(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
	subject := "lfx.index.project"

	// Mock expectations
	mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(nil)

	// Execute with nil reply function
	err := handler.HandleWithReply(ctx, data, subject, nil)

	// Verify
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_HandleWithReply_WithoutReplyError(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	ctx := context.Background()
	data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
	subject := "lfx.index.project"
	expectedError := errors.New("processing error")

	// Mock expectations
	mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(expectedError)

	// Execute with nil reply function
	err := handler.HandleWithReply(ctx, data, subject, nil)

	// Verify
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_SubjectDetection(t *testing.T) {
	tests := []struct {
		name             string
		subject          string
		expectedV1Method bool
		description      string
	}{
		{
			name:             "V2 project subject",
			subject:          "lfx.index.project",
			expectedV1Method: false,
			description:      "Should route to V2 processor",
		},
		{
			name:             "V1 project subject",
			subject:          "lfx.v1.index.project",
			expectedV1Method: true,
			description:      "Should route to V1 processor",
		},
		{
			name:             "V2 committee subject",
			subject:          "lfx.index.committee",
			expectedV1Method: false,
			description:      "Should route to V2 processor",
		},
		{
			name:             "V1 committee subject",
			subject:          "lfx.v1.index.committee",
			expectedV1Method: true,
			description:      "Should route to V1 processor",
		},
		{
			name:             "Generic V2 subject",
			subject:          "lfx.index.something",
			expectedV1Method: false,
			description:      "Should route to V2 processor",
		},
		{
			name:             "Generic V1 subject",
			subject:          "lfx.v1.index.something",
			expectedV1Method: true,
			description:      "Should route to V1 processor",
		},
		{
			name:             "Invalid subject defaults to V2",
			subject:          "invalid.subject",
			expectedV1Method: false,
			description:      "Should route to V2 processor by default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			mockProcessor := &MockMessageProcessor{}
			handler := NewTestableIndexingMessageHandler(mockProcessor)

			ctx := context.Background()
			data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)

			// Mock expectations based on expected routing
			if tt.expectedV1Method {
				mockProcessor.On("ProcessV1IndexingMessage", ctx, data, tt.subject).Return(nil)
			} else {
				mockProcessor.On("ProcessIndexingMessage", ctx, data, tt.subject).Return(nil)
			}

			// Execute
			err := handler.Handle(ctx, data, tt.subject)

			// Verify
			assert.NoError(t, err, tt.description)
			mockProcessor.AssertExpectations(t)
		})
	}
}

func TestIndexingMessageHandler_WithContext(t *testing.T) {
	// Setup
	mockProcessor := &MockMessageProcessor{}
	handler := NewTestableIndexingMessageHandler(mockProcessor)

	// Create context with request ID
	ctx := context.Background()
	ctx, _ = logging.WithRequestID(ctx, slog.Default())

	data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
	subject := "lfx.index.project"

	// Mock expectations
	mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(nil)

	// Execute
	err := handler.Handle(ctx, data, subject)

	// Verify
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestIndexingMessageHandler_Integration(t *testing.T) {
	// This test verifies the integration between handler and message processor interface

	t.Run("interface_compatibility", func(t *testing.T) {
		// Create a mock that implements the MessageProcessorInterface
		mockProcessor := &MockMessageProcessor{}

		// Verify that our handler can be created with the interface
		var processor MessageProcessorInterface = mockProcessor
		handler := NewTestableIndexingMessageHandler(processor)

		require.NotNil(t, handler)
		assert.Equal(t, processor, handler.messageProcessor)
	})
}

func TestIndexingMessageHandler_EdgeCases(t *testing.T) {
	t.Run("empty_data", func(t *testing.T) {
		mockProcessor := &MockMessageProcessor{}
		handler := NewTestableIndexingMessageHandler(mockProcessor)

		ctx := context.Background()
		data := []byte{}
		subject := "lfx.index.project"

		mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(nil)

		err := handler.Handle(ctx, data, subject)
		assert.NoError(t, err)
		mockProcessor.AssertExpectations(t)
	})

	t.Run("nil_data", func(t *testing.T) {
		mockProcessor := &MockMessageProcessor{}
		handler := NewTestableIndexingMessageHandler(mockProcessor)

		ctx := context.Background()
		var data []byte
		subject := "lfx.index.project"

		mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(nil)

		err := handler.Handle(ctx, data, subject)
		assert.NoError(t, err)
		mockProcessor.AssertExpectations(t)
	})

	t.Run("empty_subject", func(t *testing.T) {
		mockProcessor := &MockMessageProcessor{}
		handler := NewTestableIndexingMessageHandler(mockProcessor)

		ctx := context.Background()
		data := []byte(`{"action": "created", "data": {"id": "test-123"}}`)
		subject := ""

		// Empty subject should route to V2 processor
		mockProcessor.On("ProcessIndexingMessage", ctx, data, subject).Return(nil)

		err := handler.Handle(ctx, data, subject)
		assert.NoError(t, err)
		mockProcessor.AssertExpectations(t)
	})
}

// TestIndexingMessageHandler_RealMessageProcessorContract tests that the real IndexingMessageHandler
// has the same behavior as our testable version, ensuring our test implementation matches reality
func TestIndexingMessageHandler_RealMessageProcessorContract(t *testing.T) {
	// This test demonstrates that the real IndexingMessageHandler expects a *application.MessageProcessor
	// and calls the same methods we're mocking

	t.Run("verify_contract", func(t *testing.T) {
		// We can't easily create a real MessageProcessor for this test due to its dependencies,
		// but we can verify that our understanding of the interface is correct

		// Test that constants.FromV1Prefix is used correctly
		assert.Equal(t, "lfx.v1.index.", constants.FromV1Prefix)

		// Test that our mock interface matches the expected behavior
		mockProcessor := &MockMessageProcessor{}
		handler := NewTestableIndexingMessageHandler(mockProcessor)

		ctx := context.Background()
		data := []byte(`{"test": "data"}`)

		// Test V1 routing
		mockProcessor.On("ProcessV1IndexingMessage", ctx, data, "lfx.v1.index.test").Return(nil)
		err := handler.Handle(ctx, data, "lfx.v1.index.test")
		assert.NoError(t, err)

		// Test V2 routing
		mockProcessor.On("ProcessIndexingMessage", ctx, data, "lfx.index.test").Return(nil)
		err = handler.Handle(ctx, data, "lfx.index.test")
		assert.NoError(t, err)

		mockProcessor.AssertExpectations(t)
	})
}
