package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/constants"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
)

// JanitorServiceInterface defines the interface for janitor service operations
// This allows the application layer to depend on abstractions, not concrete implementations
type JanitorServiceInterface interface {
	CheckItem(objectRef string)
}

// MessageProcessingUseCase orchestrates the complete message processing workflow
// Following clean architecture: coordinates application-level business workflows
type MessageProcessingUseCase struct {
	indexingUseCase *IndexingUseCase
	janitorService  JanitorServiceInterface
}

// NewMessageProcessingUseCase creates a new message processing use case
func NewMessageProcessingUseCase(
	indexingUseCase *IndexingUseCase,
	janitorService JanitorServiceInterface,
) *MessageProcessingUseCase {
	return &MessageProcessingUseCase{
		indexingUseCase: indexingUseCase,
		janitorService:  janitorService,
	}
}

// ProcessIndexingMessage processes V2 indexing messages
// Coordinates both indexing and janitor operations
func (uc *MessageProcessingUseCase) ProcessIndexingMessage(ctx context.Context, data []byte, subject string) error {
	// 1. Process the indexing message through the indexing use case
	err := uc.indexingUseCase.HandleIndexingMessage(ctx, data, subject)
	if err != nil {
		return fmt.Errorf("failed to process indexing message: %w", err)
	}

	// 2. Extract object reference for janitor coordination
	objectRef, err := uc.extractObjectRef(data, subject, false)
	if err != nil {
		// Log warning but don't fail the entire process
		// The indexing succeeded, janitor failure shouldn't break the flow
		return nil
	}

	// 3. Queue janitor check if we have a valid object reference
	if objectRef != "" {
		uc.janitorService.CheckItem(objectRef)
	}

	return nil
}

// ProcessV1IndexingMessage processes V1 indexing messages
// Coordinates both indexing and janitor operations
func (uc *MessageProcessingUseCase) ProcessV1IndexingMessage(ctx context.Context, data []byte, subject string) error {
	// 1. Process the V1 indexing message through the indexing use case
	err := uc.indexingUseCase.HandleV1IndexingMessage(ctx, data, subject)
	if err != nil {
		return fmt.Errorf("failed to process V1 indexing message: %w", err)
	}

	// 2. Extract object reference for janitor coordination
	objectRef, err := uc.extractObjectRef(data, subject, true)
	if err != nil {
		// Log warning but don't fail the entire process
		// The indexing succeeded, janitor failure shouldn't break the flow
		return nil
	}

	// 3. Queue janitor check if we have a valid object reference
	if objectRef != "" {
		uc.janitorService.CheckItem(objectRef)
	}

	return nil
}

// extractObjectRef extracts the object reference from NATS message data and subject
// This replaces the incomplete implementation in the container
func (uc *MessageProcessingUseCase) extractObjectRef(data []byte, subject string, isV1 bool) (string, error) {
	// Parse the transaction data to extract object ID
	var transaction entities.LFXTransaction
	if err := json.Unmarshal(data, &transaction); err != nil {
		return "", fmt.Errorf("failed to parse transaction data: %w", err)
	}

	// Extract object type from subject using simple string operations
	var objectType string
	var found bool
	if isV1 {
		// V1 format: "lfx.v1.index.project" -> "project"
		objectType, found = strings.CutPrefix(subject, constants.FromV1Prefix)
	} else {
		// V2 format: "lfx.index.project" -> "project"
		objectType, found = strings.CutPrefix(subject, constants.IndexPrefix)
	}

	if !found {
		return "", fmt.Errorf("invalid subject format: %s", subject)
	}

	// Extract object ID from transaction data
	var objectID string

	switch transaction.Action {
	case "delete":
		// For delete actions, data is the object ID
		if id, ok := transaction.Data.(string); ok {
			objectID = id
		}
	case "create", "update":
		// For create/update actions, extract from data map
		if dataMap, ok := transaction.Data.(map[string]any); ok {
			if uid, ok := dataMap["uid"].(string); ok {
				objectID = uid
			}
		}
		// Also check V1 data if present
		if objectID == "" && transaction.V1Data != nil {
			if uid, ok := transaction.V1Data["uid"].(string); ok {
				objectID = uid
			}
		}
	}

	// Return empty string if we can't extract object ID
	// This is not an error - some messages might not have extractable IDs
	if objectID == "" {
		return "", nil
	}

	// Create object reference in the format "type:id"
	return fmt.Sprintf("%s:%s", objectType, objectID), nil
}
