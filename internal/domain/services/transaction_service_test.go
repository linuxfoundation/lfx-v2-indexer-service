package services

import (
	"context"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/mocks"
)

func TestTransactionService_ProcessTransaction_Success(t *testing.T) {
	// Setup
	mockRepo := mocks.NewMockTransactionRepository()
	mockAuth := mocks.NewMockAuthRepository()
	service := NewTransactionService(mockRepo, mockAuth)

	// Test data
	transaction := &entities.LFXTransaction{
		Action:     "created",
		ObjectType: "project",
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data: map[string]any{
			"id":   "test-project",
			"name": "Test Project",
		},
		Timestamp: time.Now(),
		ParsedPrincipals: []entities.Principal{
			{
				Principal: "test_user",
				Email:     "test@example.com",
			},
		},
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got failure")
	}

	if !result.IndexSuccess {
		t.Errorf("Expected index success, got failure")
	}
}

func TestTransactionService_ProcessTransaction_ValidationError(t *testing.T) {
	// Setup
	mockRepo := mocks.NewMockTransactionRepository()
	mockAuth := mocks.NewMockAuthRepository()
	service := NewTransactionService(mockRepo, mockAuth)

	// Test data with invalid action
	transaction := &entities.LFXTransaction{
		Action:     "invalid",
		ObjectType: "project",
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data: map[string]any{
			"id":   "test-project",
			"name": "Test Project",
		},
		Timestamp: time.Now(),
		ParsedPrincipals: []entities.Principal{
			{
				Principal: "test_user",
				Email:     "test@example.com",
			},
		},
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	if err == nil {
		t.Errorf("Expected error for invalid action, got none")
	}

	if result.Success {
		t.Errorf("Expected failure, got success")
	}
}

func TestTransactionService_ProcessTransaction_DeleteAction(t *testing.T) {
	// Setup
	mockRepo := mocks.NewMockTransactionRepository()
	mockAuth := mocks.NewMockAuthRepository()
	service := NewTransactionService(mockRepo, mockAuth)

	// Test data for delete action
	transaction := &entities.LFXTransaction{
		Action:     "deleted",
		ObjectType: "project",
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data:      "test-project-id",
		Timestamp: time.Now(),
		ParsedPrincipals: []entities.Principal{
			{
				Principal: "test_user",
				Email:     "test@example.com",
			},
		},
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got failure")
	}

	if !result.IndexSuccess {
		t.Errorf("Expected index success, got failure")
	}
}
