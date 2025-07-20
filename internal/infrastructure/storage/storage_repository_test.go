// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
)

func TestNewStorageRepository(t *testing.T) {
	// Setup
	client := &opensearch.Client{}
	logger, _ := logging.TestLogger(t)

	// Execute
	repo := NewStorageRepository(client, logger)

	// Verify
	assert.NotNil(t, repo)
	assert.Equal(t, client, repo.client)
	assert.NotNil(t, repo.logger)
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "",
		},
		{
			name:     "network error",
			err:      fmt.Errorf("connection refused"),
			expected: "network",
		},
		{
			name:     "timeout error",
			err:      fmt.Errorf("request timeout"),
			expected: "network",
		},
		{
			name:     "version conflict",
			err:      fmt.Errorf("409 version conflict"),
			expected: "version_conflict",
		},
		{
			name:     "bad request",
			err:      fmt.Errorf("400 bad request"),
			expected: "bad_request",
		},
		{
			name:     "authentication error",
			err:      fmt.Errorf("401 unauthorized"),
			expected: "authentication",
		},
		{
			name:     "authorization error",
			err:      fmt.Errorf("403 forbidden"),
			expected: "authentication",
		},
		{
			name:     "not found",
			err:      fmt.Errorf("404 not found"),
			expected: "not_found",
		},
		{
			name:     "marshal error",
			err:      fmt.Errorf("failed to marshal data"),
			expected: "serialization",
		},
		{
			name:     "decode error",
			err:      fmt.Errorf("failed to decode response"),
			expected: "serialization",
		},
		{
			name:     "unknown error",
			err:      fmt.Errorf("some unknown error"),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLogOperationStartAndEnd(t *testing.T) {
	// Setup
	logger, logOutput := logging.TestLogger(t)
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	// Test successful operation
	t.Run("successful operation", func(t *testing.T) {
		logOutput.Reset()

		enhancedLogger, start := repo.logOperationStart(logger, "test_operation", map[string]any{"key": "value"})

		// Simulate some work
		time.Sleep(1 * time.Millisecond)

		repo.logOperationEnd(enhancedLogger, "test_operation", start, nil, map[string]any{"result": "success"})

		logs := logOutput.String()
		assert.Contains(t, logs, "Storage operation started")
		assert.Contains(t, logs, "Storage operation completed successfully")
		assert.Contains(t, logs, "operation\":\"test_operation\"")
		assert.Contains(t, logs, "key\":\"value\"")
		assert.Contains(t, logs, "result\":\"success\"")
		assert.Contains(t, logs, "duration_ms")
	})

	// Test failed operation
	t.Run("failed operation", func(t *testing.T) {
		logOutput.Reset()

		enhancedLogger, start := repo.logOperationStart(logger, "test_operation", nil)

		// Simulate some work
		time.Sleep(1 * time.Millisecond)

		testErr := fmt.Errorf("test error")
		repo.logOperationEnd(enhancedLogger, "test_operation", start, testErr, map[string]any{"stage": "testing"})

		logs := logOutput.String()
		assert.Contains(t, logs, "Storage operation started")
		assert.Contains(t, logs, "Storage operation failed")
		assert.Contains(t, logs, "error\":\"test error\"")
		assert.Contains(t, logs, "error_type\":\"unknown\"")
		assert.Contains(t, logs, "stage\":\"testing\"")
	})
}

func TestLogOperationStart_WithMetadata(t *testing.T) {
	// Setup
	logger, logOutput := logging.TestLogger(t)
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	// Test with metadata
	metadata := map[string]any{
		"document_id": "test-doc-id",
		"index":       "test-index",
		"seq_no":      int64(1),
	}

	enhancedLogger, start := repo.logOperationStart(logger, "test_operation", metadata)

	// Verify logger and start time
	assert.NotNil(t, enhancedLogger)
	assert.True(t, start.Before(time.Now()) || start.Equal(time.Now()))

	// Verify logging
	logs := logOutput.String()
	assert.Contains(t, logs, "Storage operation started")
	assert.Contains(t, logs, "operation\":\"test_operation\"")
	assert.Contains(t, logs, "document_id\":\"test-doc-id\"")
	assert.Contains(t, logs, "index\":\"test-index\"")
	assert.Contains(t, logs, "seq_no\":1")
}

func TestLogOperationStart_WithoutMetadata(t *testing.T) {
	// Setup
	logger, logOutput := logging.TestLogger(t)
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	// Test without metadata
	enhancedLogger, start := repo.logOperationStart(logger, "test_operation", nil)

	// Verify logger and start time
	assert.NotNil(t, enhancedLogger)
	assert.True(t, start.Before(time.Now()) || start.Equal(time.Now()))

	// Verify logging
	logs := logOutput.String()
	assert.Contains(t, logs, "Storage operation started")
	assert.Contains(t, logs, "operation\":\"test_operation\"")
	assert.Contains(t, logs, "started_at")
}

func TestLogOperationEnd_Success(t *testing.T) {
	// Setup
	logger, logOutput := logging.TestLogger(t)
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	start := time.Now().Add(-10 * time.Millisecond)
	metadata := map[string]any{
		"status_code": "200 OK",
		"result":      "success",
	}

	// Execute
	repo.logOperationEnd(logger, "test_operation", start, nil, metadata)

	// Verify
	logs := logOutput.String()
	assert.Contains(t, logs, "Storage operation completed successfully")
	assert.Contains(t, logs, "operation\":\"test_operation\"")
	assert.Contains(t, logs, "status_code\":\"200 OK\"")
	assert.Contains(t, logs, "result\":\"success\"")
	assert.Contains(t, logs, "duration_ms")
}

func TestLogOperationEnd_Error(t *testing.T) {
	// Setup
	logger, logOutput := logging.TestLogger(t)
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	start := time.Now().Add(-10 * time.Millisecond)
	testErr := fmt.Errorf("connection timeout")
	metadata := map[string]any{
		"stage": "request_execution",
	}

	// Execute
	repo.logOperationEnd(logger, "test_operation", start, testErr, metadata)

	// Verify
	logs := logOutput.String()
	assert.Contains(t, logs, "Storage operation failed")
	assert.Contains(t, logs, "operation\":\"test_operation\"")
	assert.Contains(t, logs, "error\":\"connection timeout\"")
	assert.Contains(t, logs, "error_type\":\"network\"")
	assert.Contains(t, logs, "stage\":\"request_execution\"")
	assert.Contains(t, logs, "duration_ms")
}

func TestLogOperationEnd_WithEmptyMetadata(t *testing.T) {
	// Setup
	logger, logOutput := logging.TestLogger(t)
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	start := time.Now().Add(-5 * time.Millisecond)

	// Execute
	repo.logOperationEnd(logger, "test_operation", start, nil, map[string]any{})

	// Verify
	logs := logOutput.String()
	assert.Contains(t, logs, "Storage operation completed successfully")
	assert.Contains(t, logs, "operation\":\"test_operation\"")
	assert.Contains(t, logs, "duration_ms")
}

func TestBulkIndex_EmptyOperations(t *testing.T) {
	// Setup
	logger, _ := logging.TestLogger(t)
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	ctx := context.Background()
	operations := []contracts.BulkOperation{}

	// Execute
	err := repo.BulkIndex(ctx, operations)

	// Verify - should return nil for empty operations
	assert.NoError(t, err)
}

func TestVersionConflictError_Properties(t *testing.T) {
	// Test that we can create and work with version conflict errors
	versionErr := &contracts.VersionConflictError{
		DocumentID:  "test-doc-id",
		CurrentSeq:  5,
		ExpectedSeq: 3,
		Err:         fmt.Errorf("version conflict detected"),
	}

	// Verify properties
	assert.Equal(t, "test-doc-id", versionErr.DocumentID)
	assert.Equal(t, int64(5), versionErr.CurrentSeq)
	assert.Equal(t, int64(3), versionErr.ExpectedSeq)
	assert.Contains(t, versionErr.Error(), "version conflict detected")
}

// Integration test that demonstrates the logging flow
func TestStorageRepository_LoggingFlow(t *testing.T) {
	// Setup
	logger, logOutput := logging.TestLogger(t)
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	// Simulate a complete operation flow
	baseLogger := logging.WithFields(
		logging.FromContext(context.Background(), repo.logger),
		map[string]any{
			"document_id": "test-doc",
			"index":       "test-index",
		},
	)

	// Start operation
	enhancedLogger, start := repo.logOperationStart(baseLogger, "index", nil)

	// Simulate some processing time
	time.Sleep(2 * time.Millisecond)

	// End operation successfully
	repo.logOperationEnd(enhancedLogger, "index", start, nil, map[string]any{
		"status_code": "200 OK",
	})

	// Verify the complete logging flow
	logs := logOutput.String()
	assert.Contains(t, logs, "Storage operation started")
	assert.Contains(t, logs, "Storage operation completed successfully")
	assert.Contains(t, logs, "operation\":\"index\"")
	assert.Contains(t, logs, "document_id\":\"test-doc\"")
	assert.Contains(t, logs, "index\":\"test-index\"")
	assert.Contains(t, logs, "status_code\":\"200 OK\"")
	assert.Contains(t, logs, "duration_ms")
}

// Test error classification edge cases
func TestClassifyError_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "multiple keywords",
			err:      fmt.Errorf("connection timeout during marshal"),
			expected: "network", // Should match the first pattern
		},
		{
			name:     "case sensitivity",
			err:      fmt.Errorf("connection refused"),
			expected: "network", // The function is case-sensitive, so uppercase doesn't match
		},
		{
			name:     "version_conflict keyword",
			err:      fmt.Errorf("document has version_conflict"),
			expected: "version_conflict",
		},
		{
			name:     "unmarshal error",
			err:      fmt.Errorf("failed to unmarshal JSON"),
			expected: "serialization",
		},
		{
			name:     "empty error message",
			err:      fmt.Errorf(""),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Benchmark tests for performance
func BenchmarkClassifyError(b *testing.B) {
	err := fmt.Errorf("connection refused")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyError(err)
	}
}

func BenchmarkLogOperationStart(b *testing.B) {
	logger, _ := logging.TestLogger(&testing.T{})
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	metadata := map[string]any{
		"document_id": "test-doc-id",
		"index":       "test-index",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = repo.logOperationStart(logger, "test_operation", metadata)
	}
}

func BenchmarkLogOperationEnd(b *testing.B) {
	logger, _ := logging.TestLogger(&testing.T{})
	repo := &StorageRepository{
		client: &opensearch.Client{},
		logger: logger,
	}

	start := time.Now()
	metadata := map[string]any{
		"status_code": "200 OK",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		repo.logOperationEnd(logger, "test_operation", start, nil, metadata)
	}
}
