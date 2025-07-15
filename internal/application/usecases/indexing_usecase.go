package usecases

import (
	"context"
	"fmt"
	"log"
	"time"

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

	// Parse the transaction
	transaction, err := uc.transactionService.ParseTransaction(ctx, data, subject)
	if err != nil {
		log.Printf("Failed to parse transaction: %v", err)
		return fmt.Errorf("failed to parse transaction: %w", err)
	}

	// Process the transaction
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

	// Parse the V1 transaction
	transaction, err := uc.transactionService.ParseV1Transaction(ctx, data, subject)
	if err != nil {
		log.Printf("Failed to parse V1 transaction: %v", err)
		return fmt.Errorf("failed to parse V1 transaction: %w", err)
	}

	// Process the transaction
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
