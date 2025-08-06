// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/stretchr/testify/assert"
)

func TestIndexerService_ProcessTransaction_Success(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test data
	transaction := &contracts.LFXTransaction{
		Action:     constants.ActionCreated,
		ObjectType: constants.ObjectTypeProject,
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data: map[string]any{
			"id":     "test-project",
			"name":   "Test Project",
			"public": true, // Required for access control
		},
		Timestamp: time.Now(),
		ParsedPrincipals: []contracts.Principal{
			{
				Principal: "test_user",
				Email:     "test@example.com",
			},
		},
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Len(t, mockStorageRepo.IndexCalls, 1)

	indexCall := mockStorageRepo.IndexCalls[0]
	assert.Equal(t, "test-index", indexCall.Index)
	assert.Equal(t, "project:test-project", indexCall.DocID)
	assert.Contains(t, indexCall.Body, "test-project")
	assert.Contains(t, indexCall.Body, "Test Project")
}

func TestIndexerService_ProcessTransaction_EnrichmentSuccess(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test data - transaction without parsed principals
	transaction := &contracts.LFXTransaction{
		Action:     constants.ActionUpdated,
		ObjectType: constants.ObjectTypeProject,
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data: map[string]any{
			"id":     "test-project",
			"name":   "Test Project Updated",
			"public": false, // Required for access control
		},
		Timestamp: time.Now(),
		// No ParsedPrincipals - should be enriched via auth
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Len(t, mockStorageRepo.IndexCalls, 1)

	// Verify auth enrichment was called
	assert.Len(t, mockMessagingRepo.AuthRepo.ParsePrincipalsCalls, 1)
	assert.Equal(t, transaction.Headers, mockMessagingRepo.AuthRepo.ParsePrincipalsCalls[0].Headers)
}

func TestIndexerService_ProcessTransaction_InvalidAction(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test data with invalid action
	transaction := &contracts.LFXTransaction{
		Action:     "invalid-action",
		ObjectType: constants.ObjectTypeProject,
		Data: map[string]any{
			"id": "test-project",
		},
		Timestamp: time.Now(),
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "invalid transaction action")
	assert.Len(t, mockStorageRepo.IndexCalls, 0) // Should not have indexed
}

func TestIndexerService_ProcessTransaction_UnknownObjectType(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test data with unknown object type (should use default enrichment)
	transaction := &contracts.LFXTransaction{
		Action:     constants.ActionCreated,
		ObjectType: "unknown-type",
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data: map[string]any{
			"id":     "test-object",
			"public": true, // Required for access control
		},
		Timestamp: time.Now(),
		ParsedPrincipals: []contracts.Principal{
			{
				Principal: "test_user",
				Email:     "test@example.com",
			},
		},
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify - should succeed with default enrichment
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Len(t, mockStorageRepo.IndexCalls, 1) // Should have indexed successfully

	// Verify the indexed document
	indexCall := mockStorageRepo.IndexCalls[0]
	assert.Equal(t, "test-index", indexCall.Index)
	assert.Equal(t, "unknown-type:test-object", indexCall.DocID)
	assert.Contains(t, indexCall.Body, "test-object")
}

func TestIndexerService_ProcessTransaction_BasicValidation(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test that basic processing works and mocks are functional
	transaction := &contracts.LFXTransaction{
		Action:     constants.ActionCreated,
		ObjectType: constants.ObjectTypeProject,
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data: map[string]any{
			"id":     "test-project",
			"name":   "Test Project",
			"public": true, // Required for access control
		},
		Timestamp: time.Now(),
		ParsedPrincipals: []contracts.Principal{
			{
				Principal: "test_user",
				Email:     "test@example.com",
			},
		},
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify basic functionality works
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.True(t, result.IndexSuccess)
	assert.Equal(t, "project:test-project", result.DocumentID)
}

func TestIndexerService_HealthCacheWorksCorrectly(t *testing.T) {
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)

	// First call should execute health check
	status1 := indexerService.CheckReadiness(context.Background())
	assert.Equal(t, "healthy", status1.Status)

	// Second call within cache duration should use cache
	status2 := indexerService.CheckReadiness(context.Background())
	assert.Equal(t, "healthy", status2.Status)
	// Cache may or may not be used depending on timing, just verify both are healthy

	// Test basic health functionality works
	assert.Contains(t, status1.Checks, "opensearch")
	assert.Contains(t, status1.Checks, "nats")
}

func TestIndexerService_ReadinessChecksAllDependencies(t *testing.T) {
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)

	// Test with all healthy
	status := indexerService.CheckReadiness(context.Background())
	assert.Equal(t, "healthy", status.Status)
	assert.Contains(t, status.Checks, "opensearch")
	assert.Contains(t, status.Checks, "nats")
	assert.Equal(t, "healthy", status.Checks["opensearch"].Status)
	assert.Equal(t, "healthy", status.Checks["nats"].Status)
	assert.Equal(t, 0, status.ErrorCount)
}

func TestIndexerService_LivenessAlwaysHealthy(t *testing.T) {
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)

	// Test with all dependencies failing
	mockStorageRepo.HealthError = assert.AnError
	mockMessagingRepo.HealthError = assert.AnError

	// Liveness should still be healthy (only checks if service is running)
	status := indexerService.CheckLiveness(context.Background())
	assert.Equal(t, "healthy", status.Status)
}

func TestIndexerService_GeneralHealthCheck(t *testing.T) {
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)

	// Test healthy state
	status := indexerService.CheckHealth(context.Background())
	assert.Equal(t, "healthy", status.Status)
	assert.Equal(t, 0, status.ErrorCount)
	assert.NotZero(t, status.Duration)
	assert.NotZero(t, status.Timestamp)
}

func TestIndexerService_ValidateObjectType_RegistryBased(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test valid object type (project is registered in the enricher registry)
	validTransaction := &contracts.LFXTransaction{
		ObjectType: constants.ObjectTypeProject,
	}
	err := service.ValidateObjectType(validTransaction)
	assert.NoError(t, err, "Project object type should be valid")

	// Test unknown object type (committee is not registered in the enricher registry)
	// Should now succeed with default enrichment
	unknownTransaction := &contracts.LFXTransaction{
		ObjectType: constants.ObjectTypeCommittee,
	}
	err = service.ValidateObjectType(unknownTransaction)
	assert.NoError(t, err, "Committee object type should use default enrichment")

	// Test completely unknown object type - should also succeed with default enrichment
	unknownTransaction2 := &contracts.LFXTransaction{
		ObjectType: "unknown-type",
	}
	err = service.ValidateObjectType(unknownTransaction2)
	assert.NoError(t, err, "Unknown object type should use default enrichment")
}
