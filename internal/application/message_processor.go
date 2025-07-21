// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
)

// MessageProcessor orchestrates the complete message processing workflow
// Handles both V2 and V1 message processing use cases
type MessageProcessor struct {
	// Dependencies
	indexerService    *services.IndexerService         // Domain service for business logic
	messagingRepo     contracts.MessagingRepository    // Infrastructure for NATS operations
	cleanupRepository contracts.CleanupRepository      // Infrastructure for cleanup operations
	logger            *slog.Logger

	// Configuration
	index string
	queue string
}

// NewMessageProcessor creates a new message processor
func NewMessageProcessor(
	indexerService *services.IndexerService,
	messagingRepo contracts.MessagingRepository,
	cleanupRepository contracts.CleanupRepository,
	logger *slog.Logger,
) *MessageProcessor {
	return &MessageProcessor{
		indexerService:    indexerService,
		messagingRepo:     messagingRepo,
		cleanupRepository: cleanupRepository,
		logger:            logging.WithComponent(logger, "message_processor"),
		index:          constants.DefaultIndex,
		queue:          constants.DefaultQueue,
	}
}

// =================
// MAIN USE CASE METHODS (workflow orchestration)
// =================

// ProcessIndexingMessage handles the complete V2 indexing message workflow
func (mp *MessageProcessor) ProcessIndexingMessage(ctx context.Context, data []byte, subject string) error {
	logger := logging.FromContext(ctx, mp.logger)
	messageID := mp.generateMessageID()

	logger.Info("Processing indexing message",
		"message_id", messageID,
		"subject", subject,
		"message_type", "V2")

	// Create transaction from message data
	transaction, err := mp.createTransaction(ctx, data, subject)
	if err != nil {
		logging.LogError(logger, constants.LogFailedCreateTransaction, err,
			"message_id", messageID,
			"subject", subject)
		return fmt.Errorf("%s: %w", constants.ErrCreateTransaction, err)
	}

	// Process the transaction
	result, err := mp.indexerService.ProcessTransaction(ctx, transaction, mp.index)
	if err != nil {
		logging.LogError(logger, "Failed to process transaction", err,
			"message_id", messageID,
			"action", transaction.Action,
			"object_type", transaction.ObjectType,
			"subject", subject)
		return fmt.Errorf("failed to process transaction: %w", err)
	}

	// Trigger cleanup check for the processed object
	if result.Success && result.DocumentID != "" {
		mp.cleanupRepository.CheckItem(result.DocumentID)
	} else {
		logger.Warn("Cleanup check skipped - processing failed or no document ID",
			"message_id", messageID,
			"result_success", result.Success,
			"document_id", result.DocumentID)
	}

	logger.Info("Indexing message processed successfully",
		"message_id", messageID,
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"document_id", result.DocumentID,
		"subject", subject,
		"index_success", result.IndexSuccess)

	return nil
}

// ProcessV1IndexingMessage handles the complete V1 indexing message workflow
func (mp *MessageProcessor) ProcessV1IndexingMessage(ctx context.Context, data []byte, subject string) error {
	logger := logging.FromContext(ctx, mp.logger)
	messageID := mp.generateMessageID()

	logger.Info("Processing V1 indexing message",
		"message_id", messageID,
		"subject", subject,
		"message_type", "V1")

	// Create V1 transaction from message data
	transaction, err := mp.createV1Transaction(ctx, data, subject)
	if err != nil {
		logging.LogError(logger, constants.LogFailedCreateV1Transaction, err,
			"message_id", messageID,
			"subject", subject)
		return fmt.Errorf("%s: %w", constants.ErrCreateV1Transaction, err)
	}

	// Process the transaction (same processing path)
	result, err := mp.indexerService.ProcessTransaction(ctx, transaction, mp.index)
	if err != nil {
		logging.LogError(logger, "Failed to process V1 transaction", err,
			"message_id", messageID,
			"action", transaction.Action,
			"object_type", transaction.ObjectType,
			"subject", subject)
		return fmt.Errorf("failed to process V1 transaction: %w", err)
	}

	// Trigger cleanup check for the processed object
	if result.Success && result.DocumentID != "" {
		mp.cleanupRepository.CheckItem(result.DocumentID)
	} else {
		logger.Warn("Cleanup check skipped - processing failed or no document ID",
			"message_id", messageID,
			"result_success", result.Success,
			"document_id", result.DocumentID)
	}

	logger.Info("V1 indexing message processed successfully",
		"message_id", messageID,
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"document_id", result.DocumentID,
		"subject", subject,
		"index_success", result.IndexSuccess,
		"has_v1_data", transaction.V1Data != nil)

	return nil
}

// =================
// TRANSACTION CREATION METHODS (simplified using service)
// =================

// createTransaction creates an LFXTransaction from V2 message data
func (mp *MessageProcessor) createTransaction(ctx context.Context, data []byte, subject string) (*contracts.LFXTransaction, error) {
	logger := logging.FromContext(ctx, mp.logger)

	// Parse message data
	var messageData map[string]any
	if err := json.Unmarshal(data, &messageData); err != nil {
		logger.Error("Failed to unmarshal V2 message data",
			"subject", subject,
			"error", err.Error())
		return nil, fmt.Errorf("failed to unmarshal message data: %w", err)
	}

	// Extract object type from subject (V2: lfx.index.{object_type})
	objectType, found := strings.CutPrefix(subject, constants.IndexPrefix)
	if !found {
		logger.Error("Invalid V2 subject format",
			"subject", subject,
			"expected_prefix", constants.IndexPrefix)
		return nil, fmt.Errorf("invalid V2 subject format: %s", subject)
	}

	// Use service method for transaction creation
	transaction, err := mp.indexerService.CreateTransactionFromMessage(messageData, objectType, false)
	if err != nil {
		logger.Error("Failed to create V2 transaction via service",
			"subject", subject,
			"object_type", objectType,
			"error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrCreateV2Transaction, err)
	}

	return transaction, nil
}

// createV1Transaction creates an LFXTransaction from V1 message data
func (mp *MessageProcessor) createV1Transaction(ctx context.Context, data []byte, subject string) (*contracts.LFXTransaction, error) {
	logger := logging.FromContext(ctx, mp.logger)

	// Parse message data
	var messageData map[string]any
	if err := json.Unmarshal(data, &messageData); err != nil {
		logger.Error("Failed to unmarshal V1 message data",
			"subject", subject,
			"error", err.Error())
		return nil, fmt.Errorf("failed to unmarshal V1 message data: %w", err)
	}

	// Extract object type from subject (V1: lfx.v1.index.{object_type})
	objectType, found := strings.CutPrefix(subject, constants.FromV1Prefix)
	if !found {
		logger.Error("Invalid V1 subject format",
			"subject", subject,
			"expected_prefix", constants.FromV1Prefix)
		return nil, fmt.Errorf("invalid V1 subject format: %s", subject)
	}

	// Use service method for transaction creation
	transaction, err := mp.indexerService.CreateTransactionFromMessage(messageData, objectType, true)
	if err != nil {
		logger.Error("Failed to create V1 transaction via service",
			"subject", subject,
			"object_type", objectType,
			"error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrCreateV1Transaction, err)
	}

	return transaction, nil
}

// =================
// SUBSCRIPTION METHODS (streamlined from MessageProcessor)
// =================

// StartSubscriptions starts all NATS subscriptions (consolidated)
func (mp *MessageProcessor) StartSubscriptions(ctx context.Context) error {
	logger := logging.FromContext(ctx, mp.logger)

	logger.Info("Starting NATS subscriptions for indexing workflow",
		"queue", mp.queue,
		"index", mp.index)

	// Subscribe to V2 indexing messages
	if err := mp.subscribeTo(ctx, constants.AllSubjects, "V2 indexing messages"); err != nil {
		logger.Error("Failed to subscribe to V2 messages",
			"subjects", constants.AllSubjects,
			"queue", mp.queue,
			"error", err.Error())
		return fmt.Errorf("failed to subscribe to V2 messages: %w", err)
	}

	logger.Info("V2 subscription successful",
		"subjects", constants.AllSubjects,
		"queue", mp.queue)

	// Subscribe to V1 indexing messages
	if err := mp.subscribeTo(ctx, constants.AllV1Subjects, "V1 indexing messages"); err != nil {
		logger.Error("Failed to subscribe to V1 messages",
			"subjects", constants.AllV1Subjects,
			"queue", mp.queue,
			"error", err.Error())
		return fmt.Errorf("failed to subscribe to V1 messages: %w", err)
	}

	logger.Info("V1 subscription successful",
		"subjects", constants.AllV1Subjects,
		"queue", mp.queue)

	logger.Info("All NATS subscriptions started successfully",
		"v2_subjects", constants.AllSubjects,
		"v1_subjects", constants.AllV1Subjects,
		"queue", mp.queue)

	return nil
}

// subscribeTo subscribes to a NATS subject with the message handler
func (mp *MessageProcessor) subscribeTo(ctx context.Context, subject, description string) error {
	logger := logging.FromContext(ctx, mp.logger)

	logger.Info("Subscribing to NATS subject",
		"subject", subject,
		"queue", mp.queue,
		"description", description)

	// Create handler that routes to appropriate processing method
	handler := &indexingHandler{useCase: mp}

	// Attempt subscription
	if err := mp.messagingRepo.QueueSubscribeWithReply(ctx, subject, mp.queue, handler); err != nil {
		logger.Error("Failed to subscribe to NATS subject",
			"subject", subject,
			"queue", mp.queue,
			"description", description,
			"error", err.Error())

		logging.LogError(logger, "Failed to subscribe to NATS subject", err,
			"subject", subject,
			"queue", mp.queue,
			"description", description)

		return fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}

	logger.Info("Successfully subscribed to NATS subject",
		"subject", subject,
		"queue", mp.queue,
		"description", description)

	return nil
}

// indexingHandler handles NATS messages and routes them to the appropriate message processor method
type indexingHandler struct {
	useCase *MessageProcessor
}

// HandleWithReply processes messages with reply support
func (h *indexingHandler) HandleWithReply(ctx context.Context, data []byte, subject string, reply func([]byte) error) error {
	messageID := h.useCase.generateMessageID()
	logger := logging.FromContext(ctx, h.useCase.logger)

	// Route to appropriate processing method based on subject
	var err error
	var messageType string

	if strings.HasPrefix(subject, constants.FromV1Prefix) {
		messageType = "V1"
		err = h.useCase.ProcessV1IndexingMessage(ctx, data, subject)
	} else {
		messageType = "V2"
		err = h.useCase.ProcessIndexingMessage(ctx, data, subject)
	}

	// Log processing results
	if err != nil {
		logger.Error("Message processing failed in handler",
			"message_id", messageID,
			"subject", subject,
			"message_type", messageType,
			"error", err.Error())
	}

	// Handle reply if function provided
	if reply != nil {
		var replyErr error

		if err != nil {
			// Send error reply
			errorMsg := fmt.Sprintf("ERROR: %s", err.Error())
			replyErr = reply([]byte(errorMsg))
			if replyErr != nil {
				logger.Error("Failed to send error reply",
					"message_id", messageID,
					"subject", subject,
					"message_type", messageType,
					"original_error", err.Error(),
					"reply_error", replyErr.Error())
			}
		} else {
			// Send success reply
			successMsg := "OK"
			replyErr = reply([]byte(successMsg))
			if replyErr != nil {
				logger.Error("Failed to send success reply",
					"message_id", messageID,
					"subject", subject,
					"message_type", messageType,
					"reply_error", replyErr.Error())
			}
		}
	}

	logger.Info("Message handler completed",
		"message_id", messageID,
		"subject", subject,
		"message_type", messageType,
		"success", err == nil)

	return err
}

// =================
// HELPER METHODS
// =================

// generateMessageID generates a unique message ID for tracking
func (mp *MessageProcessor) generateMessageID() string {
	timestamp := strconv.FormatInt(time.Now().UnixNano(), 10)

	// Generate a short random suffix
	randBytes := make([]byte, 4)
	if _, err := rand.Read(randBytes); err != nil {
		// Fallback to timestamp only if random generation fails
		return fmt.Sprintf("msg_%s", timestamp)
	}

	return fmt.Sprintf("msg_%s_%s", timestamp, hex.EncodeToString(randBytes))
}
