package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/constants"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/logging"
)

// IndexingUseCase orchestrates the indexing workflow
type IndexingUseCase struct {
	transactionService *services.TransactionService
	messageRepo        repositories.MessageRepository
	logger             *slog.Logger
	index              string
	queue              string
}

// NewIndexingUseCase creates a new indexing use case
func NewIndexingUseCase(
	transactionService *services.TransactionService,
	messageRepo repositories.MessageRepository,
	logger *slog.Logger,
	index string,
	queue string,
) *IndexingUseCase {
	return &IndexingUseCase{
		transactionService: transactionService,
		messageRepo:        messageRepo,
		logger:             logging.WithComponent(logger, "indexing_usecase"),
		index:              index,
		queue:              queue,
	}
}

// HandleIndexingMessage processes an indexing message
func (uc *IndexingUseCase) HandleIndexingMessage(ctx context.Context, data []byte, subject string) error {
	// Use request-aware logger with component
	logger := logging.FromContext(ctx, uc.logger)

	startTime := time.Now()
	logger.Info("Processing indexing message started",
		"subject", subject,
		"data_size", len(data))

	// Create transaction from message data
	transaction, err := uc.createTransactionFromMessage(ctx, data, subject, false)
	if err != nil {
		logging.LogError(logger, "Failed to create transaction", err)
		return fmt.Errorf("failed to create transaction: %w", err)
	}

	// Process the transaction (includes enrichment)
	result, err := uc.transactionService.ProcessTransaction(ctx, transaction, uc.index)
	if err != nil {
		logging.LogError(logger, "Failed to process transaction", err)
		return fmt.Errorf("failed to process transaction: %w", err)
	}

	// Log the result
	duration := time.Since(startTime)
	if result.Success {
		logger.Info("Successfully processed transaction",
			"message_id", result.MessageID,
			"document_id", result.DocumentID,
			"duration", duration)
	} else {
		logging.LogError(logger, "Failed to process transaction", result.Error)
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
		logging.LogError(uc.logger, "Failed to create V1 transaction", err)
		return fmt.Errorf("failed to create V1 transaction: %w", err)
	}

	// Process the transaction (includes enrichment)
	result, err := uc.transactionService.ProcessTransaction(ctx, transaction, uc.index)
	if err != nil {
		logging.LogError(uc.logger, "Failed to process V1 transaction", err)
		return fmt.Errorf("failed to process V1 transaction: %w", err)
	}

	// Log the result
	duration := time.Since(startTime)
	if result.Success {
		uc.logger.Info("Successfully processed V1 transaction",
			"message_id", result.MessageID,
			"document_id", result.DocumentID,
			"duration", duration)
	} else {
		logging.LogError(uc.logger, "Failed to process V1 transaction", result.Error)
		return result.Error
	}

	return nil
}

// createTransactionFromMessage creates a transaction from NATS message data
func (uc *IndexingUseCase) createTransactionFromMessage(ctx context.Context, data []byte, subject string, isV1 bool) (*entities.LFXTransaction, error) {
	logger := logging.FromContext(ctx, uc.logger)

	logger.Info("Creating transaction from message",
		"subject", subject,
		"is_v1", isV1,
		"data_size", len(data))

	var transaction entities.LFXTransaction
	if err := json.Unmarshal(data, &transaction); err != nil {
		logging.LogError(logger, "Failed to unmarshal transaction data", err)
		return nil, fmt.Errorf("failed to unmarshal transaction: %w", err)
	}

	// Extract object type from subject using simple string operations
	var objectType string
	var found bool
	if isV1 {
		// V1 subject format: "lfx.v1.index.{object_type}"
		objectType, found = strings.CutPrefix(subject, constants.FromV1Prefix)
		logger.Info("Parsing V1 subject",
			"subject", subject,
			"object_type", objectType)
	} else {
		// V2 subject format: "lfx.index.{object_type}"
		objectType, found = strings.CutPrefix(subject, constants.IndexPrefix)
		logger.Info("Parsing V2 subject",
			"subject", subject,
			"object_type", objectType)
	}

	if !found {
		logging.LogError(logger, "Invalid subject format", fmt.Errorf("invalid subject format: %s", subject))
		return nil, fmt.Errorf("invalid subject format: %s", subject)
	}

	transaction.ObjectType = objectType
	transaction.IsV1 = isV1
	transaction.Timestamp = time.Now()

	logger.Info("Successfully created transaction",
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"is_v1", transaction.IsV1)

	return &transaction, nil
}

// StartIndexingSubscription starts the indexing subscription
func (uc *IndexingUseCase) StartIndexingSubscription(ctx context.Context, subject string) error {
	logger := logging.FromContext(ctx, uc.logger)
	logger.Info("Starting indexing subscription",
		"subject", subject,
		"queue", uc.queue)
	return uc.messageRepo.QueueSubscribe(ctx, subject, uc.queue, &indexingHandler{useCase: uc})
}

// StartV1IndexingSubscription starts the V1 indexing subscription
func (uc *IndexingUseCase) StartV1IndexingSubscription(ctx context.Context, subject string) error {
	logger := logging.FromContext(ctx, uc.logger)
	logger.Info("Starting V1 indexing subscription",
		"subject", subject,
		"queue", uc.queue)
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
