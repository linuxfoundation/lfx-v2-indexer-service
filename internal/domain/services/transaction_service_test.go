package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/mocks"
)

func TestTransactionService_ParseTransaction(t *testing.T) {
	mockTransactionRepo := mocks.NewMockTransactionRepository()
	mockAuthRepo := mocks.NewMockAuthRepository()

	service := NewTransactionService(mockTransactionRepo, mockAuthRepo)

	// Test data - simple transaction JSON
	transactionData := map[string]any{
		"action": "create",
		"data": map[string]any{
			"uid":    "test-project",
			"name":   "Test Project",
			"public": true,
		},
		"headers": map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	data, err := json.Marshal(transactionData)
	if err != nil {
		t.Fatalf("Failed to marshal test transaction: %v", err)
	}

	// Mock auth repository to return valid principal
	mockAuthRepo.SetValidToken("test-token", &entities.Principal{
		Principal: "test-user",
		Email:     "test@example.com",
	})

	// Parse the transaction
	result, err := service.ParseTransaction(context.Background(), data, "lfx.index.project")
	if err != nil {
		t.Fatalf("ParseTransaction failed: %v", err)
	}

	// Verify the result
	if result.Action != "create" {
		t.Errorf("Expected Action 'create', got '%s'", result.Action)
	}
	if result.ObjectType != "project" {
		t.Errorf("Expected ObjectType 'project', got '%s'", result.ObjectType)
	}
	if result.ParsedData["uid"] != "test-project" {
		t.Errorf("Expected ParsedData uid 'test-project', got '%v'", result.ParsedData["uid"])
	}
}

func TestTransactionService_ParseV1Transaction(t *testing.T) {
	mockTransactionRepo := mocks.NewMockTransactionRepository()
	mockAuthRepo := mocks.NewMockAuthRepository()

	service := NewTransactionService(mockTransactionRepo, mockAuthRepo)

	// Test V1 transaction data
	v1TransactionData := map[string]any{
		"v1_data": map[string]any{
			"uid":  "test-v1-project",
			"name": "Test V1 Project",
		},
		"headers": map[string]string{
			"Authorization": "Bearer test-v1-token",
		},
	}

	data, err := json.Marshal(v1TransactionData)
	if err != nil {
		t.Fatalf("Failed to marshal test V1 transaction: %v", err)
	}

	// Mock auth repository to return valid principal
	mockAuthRepo.SetValidToken("test-v1-token", &entities.Principal{
		Principal: "test-v1-user",
		Email:     "test-v1@example.com",
	})

	// Parse the V1 transaction
	result, err := service.ParseV1Transaction(context.Background(), data, "lfx.v1.index.project")
	if err != nil {
		t.Fatalf("ParseV1Transaction failed: %v", err)
	}

	// Verify the result
	if result.ObjectType != "project" {
		t.Errorf("Expected ObjectType 'project', got '%s'", result.ObjectType)
	}
	if !result.IsV1Transaction() {
		t.Error("Expected IsV1Transaction to be true")
	}
	if result.V1Data["uid"] != "test-v1-project" {
		t.Errorf("Expected V1Data uid 'test-v1-project', got '%v'", result.V1Data["uid"])
	}
}

func TestTransactionService_GenerateIndexBody(t *testing.T) {
	mockTransactionRepo := mocks.NewMockTransactionRepository()
	mockAuthRepo := mocks.NewMockAuthRepository()

	service := NewTransactionService(mockTransactionRepo, mockAuthRepo)

	// Test transaction
	testTransaction := &entities.LFXTransaction{
		Action:     "create",
		ObjectType: "project",
		ParsedData: map[string]any{
			"uid":    "test-generate-project",
			"name":   "Test Generate Project",
			"public": true,
		},
		ParsedPrincipals: []entities.Principal{
			{Principal: "test-generate-user", Email: "test-generate@example.com"},
		},
		Timestamp: time.Now(),
	}

	// Generate index body
	docID, body, err := service.GenerateIndexBody(context.Background(), testTransaction)
	if err != nil {
		t.Fatalf("GenerateIndexBody failed: %v", err)
	}

	// Verify document ID
	if docID == "" {
		t.Error("Expected non-empty document ID")
	}

	// Verify body content
	if body == nil {
		t.Error("Expected non-nil body")
	}

	// Read and verify body content
	bodyBytes := make([]byte, 1024)
	n, err := body.Read(bodyBytes)
	if err != nil && n == 0 {
		t.Errorf("Failed to read body: %v", err)
	}

	bodyString := string(bodyBytes[:n])
	if !strings.Contains(bodyString, "test-generate-project") {
		t.Errorf("Expected body to contain project ID, got: %s", bodyString)
	}
}

func TestTransactionService_ProcessTransaction(t *testing.T) {
	mockTransactionRepo := mocks.NewMockTransactionRepository()
	mockAuthRepo := mocks.NewMockAuthRepository()

	service := NewTransactionService(mockTransactionRepo, mockAuthRepo)

	// Test transaction
	testTransaction := &entities.LFXTransaction{
		Action:     "create",
		ObjectType: "project",
		ParsedData: map[string]any{
			"uid":    "test-process-project",
			"name":   "Test Process Project",
			"public": true,
		},
		ParsedPrincipals: []entities.Principal{
			{Principal: "test-process-user", Email: "test-process@example.com"},
		},
		Timestamp: time.Now(),
	}

	// Process the transaction
	result, err := service.ProcessTransaction(context.Background(), testTransaction, "test-index")
	if err != nil {
		t.Fatalf("ProcessTransaction failed: %v", err)
	}

	// Verify the result
	if !result.Success {
		t.Errorf("Expected success=true, got %v", result.Success)
	}
	if result.Error != nil {
		t.Errorf("Expected no error, got %v", result.Error)
	}
	if result.MessageID == "" {
		t.Error("Expected non-empty message ID")
	}
	if result.DocumentID == "" {
		t.Error("Expected non-empty document ID")
	}
	if !result.IndexSuccess {
		t.Errorf("Expected index success=true, got %v", result.IndexSuccess)
	}

	// Verify transaction repository was called
	if len(mockTransactionRepo.IndexCalls) != 1 {
		t.Errorf("Expected 1 index call, got %d", len(mockTransactionRepo.IndexCalls))
	}
}

func TestTransactionService_ProcessTransactionWithIndexError(t *testing.T) {
	mockTransactionRepo := mocks.NewMockTransactionRepository()
	mockAuthRepo := mocks.NewMockAuthRepository()

	service := NewTransactionService(mockTransactionRepo, mockAuthRepo)

	// Set up mock to return error on Index
	mockTransactionRepo.IndexError = errors.New("index error")

	// Test transaction
	testTransaction := &entities.LFXTransaction{
		Action:     "create",
		ObjectType: "project",
		ParsedData: map[string]any{
			"uid":    "test-error-project",
			"name":   "Test Error Project",
			"public": true,
		},
		ParsedPrincipals: []entities.Principal{
			{Principal: "test-error-user", Email: "test-error@example.com"},
		},
		Timestamp: time.Now(),
	}

	// Process the transaction (should fail)
	result, err := service.ProcessTransaction(context.Background(), testTransaction, "test-index")
	if err == nil {
		t.Error("Expected error, got nil")
	}

	// Verify the result
	if result.Success {
		t.Errorf("Expected success=false, got %v", result.Success)
	}
	if result.Error == nil {
		t.Error("Expected error in result, got nil")
	}
}
