package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/constants"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
)

// IndexingUseCase orchestrates the indexing workflow
type IndexingUseCase struct {
	transactionService *services.TransactionService
	messageRepo        repositories.MessageRepository
	index              string
	queue              string
}

// NewIndexingUseCase creates a new indexing use case
func NewIndexingUseCase(
	transactionService *services.TransactionService,
	messageRepo repositories.MessageRepository,
	index string,
	queue string,
) *IndexingUseCase {
	return &IndexingUseCase{
		transactionService: transactionService,
		messageRepo:        messageRepo,
		index:              index,
		queue:              queue,
	}
}

// HandleIndexingMessage processes an indexing message
func (uc *IndexingUseCase) HandleIndexingMessage(ctx context.Context, data []byte, subject string) error {
	startTime := time.Now()

	// Create transaction from message data
	transaction, err := uc.createTransactionFromMessage(ctx, data, subject, false)
	if err != nil {
		log.Printf("Failed to create transaction: %v", err)
		return fmt.Errorf("failed to create transaction: %w", err)
	}

	// Process the transaction (includes enrichment)
	result, err := uc.transactionService.ProcessTransaction(ctx, transaction, uc.index)
	if err != nil {
		log.Printf("Failed to process transaction: %v", err)
		return fmt.Errorf("failed to process transaction: %w", err)
	}

	// Log the result
	duration := time.Since(startTime)
	if result.Success {
		log.Printf("Successfully processed transaction %s (docID: %s) in %v", result.MessageID, result.DocumentID, duration)
	} else {
		log.Printf("Failed to process transaction %s: %v", result.MessageID, result.Error)
		return result.Error
	}

	return nil
}

// HandleV1IndexingMessage processes a V1 indexing message
func (uc *IndexingUseCase) HandleV1IndexingMessage(ctx context.Context, data []byte, subject string) error {
	startTime := time.Now()

	// Create V1 transaction from message data
	transaction, err := uc.createTransactionFromMessage(ctx, data, subject, true)
	if err != nil {
		log.Printf("Failed to create V1 transaction: %v", err)
		return fmt.Errorf("failed to create V1 transaction: %w", err)
	}

	// Process the transaction (includes enrichment)
	result, err := uc.transactionService.ProcessTransaction(ctx, transaction, uc.index)
	if err != nil {
		log.Printf("Failed to process V1 transaction: %v", err)
		return fmt.Errorf("failed to process V1 transaction: %w", err)
	}

	// Log the result
	duration := time.Since(startTime)
	if result.Success {
		log.Printf("Successfully processed V1 transaction %s (docID: %s) in %v", result.MessageID, result.DocumentID, duration)
	} else {
		log.Printf("Failed to process V1 transaction %s: %v", result.MessageID, result.Error)
		return result.Error
	}

	return nil
}

// createTransactionFromMessage creates a transaction from NATS message data
func (uc *IndexingUseCase) createTransactionFromMessage(ctx context.Context, data []byte, subject string, isV1 bool) (*entities.LFXTransaction, error) {
	log.Printf("Creating transaction from message: subject=%s, isV1=%v, dataSize=%d", subject, isV1, len(data))

	var transaction entities.LFXTransaction
	if err := json.Unmarshal(data, &transaction); err != nil {
		log.Printf("Failed to unmarshal transaction data: %v", err)
		return nil, fmt.Errorf("failed to unmarshal transaction: %w", err)
	}

	// Extract object type from subject using simple string operations
	var objectType string
	var found bool
	if isV1 {
		// V1 subject format: "lfx.v1.index.{object_type}"
		objectType, found = strings.CutPrefix(subject, constants.FromV1Prefix)
		log.Printf("Parsing V1 subject: %s -> objectType=%s", subject, objectType)
	} else {
		// V2 subject format: "lfx.index.{object_type}"
		objectType, found = strings.CutPrefix(subject, constants.IndexPrefix)
		log.Printf("Parsing V2 subject: %s -> objectType=%s", subject, objectType)
	}

	if !found {
		log.Printf("Invalid subject format: %s (expected V1: %s* or V2: %s*)", subject, constants.FromV1Prefix, constants.IndexPrefix)
		return nil, fmt.Errorf("invalid subject format: %s", subject)
	}

	transaction.ObjectType = objectType
	transaction.IsV1 = isV1
	transaction.Timestamp = time.Now()

	log.Printf("Successfully created transaction: action=%s, objectType=%s, isV1=%v",
		transaction.Action, transaction.ObjectType, transaction.IsV1)

	return &transaction, nil
}

// StartIndexingSubscription starts the indexing subscription
func (uc *IndexingUseCase) StartIndexingSubscription(ctx context.Context, subject string) error {
	log.Printf("Starting indexing subscription for subject: %s with queue: %s", subject, uc.queue)
	return uc.messageRepo.QueueSubscribe(ctx, subject, uc.queue, &indexingHandler{useCase: uc})
}

// StartV1IndexingSubscription starts the V1 indexing subscription
func (uc *IndexingUseCase) StartV1IndexingSubscription(ctx context.Context, subject string) error {
	log.Printf("Starting V1 indexing subscription for subject: %s with queue: %s", subject, uc.queue)
	return uc.messageRepo.QueueSubscribe(ctx, subject, uc.queue, &v1IndexingHandler{useCase: uc})
}

// indexingHandler handles indexing messages
type indexingHandler struct {
	useCase *IndexingUseCase
}

// Handle implements the MessageHandler interface
func (h *indexingHandler) Handle(ctx context.Context, data []byte, subject string) error {
	return h.useCase.HandleIndexingMessage(ctx, data, subject)
}

// v1IndexingHandler handles V1 indexing messages
type v1IndexingHandler struct {
	useCase *IndexingUseCase
}

// Handle implements the MessageHandler interface
func (h *v1IndexingHandler) Handle(ctx context.Context, data []byte, subject string) error {
	return h.useCase.HandleV1IndexingMessage(ctx, data, subject)
}
