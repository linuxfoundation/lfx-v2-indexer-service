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
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/janitor"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
)

// MessageProcessor orchestrates the complete message processing workflow
// Handles both V2 and V1 message processing use cases
type MessageProcessor struct {
	// Dependencies
	indexerService *services.IndexerService      // Domain service for business logic
	messagingRepo  contracts.MessagingRepository // Infrastructure for NATS operations
	janitorService *janitor.JanitorService       // Infrastructure for cleanup operations
	logger         *slog.Logger

	// Configuration
	index string
	queue string
}

// NewMessageProcessor creates a new message processor
func NewMessageProcessor(
	indexerService *services.IndexerService,
	messagingRepo contracts.MessagingRepository,
	janitorService *janitor.JanitorService,
	logger *slog.Logger,
) *MessageProcessor {
	return &MessageProcessor{
		indexerService: indexerService,
		messagingRepo:  messagingRepo,
		janitorService: janitorService,
		logger:         logging.WithComponent(logger, "message_processor"),
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
	startTime := time.Now()

	// Log message reception with metadata
	logger.Info("Processing indexing message",
		"message_id", messageID,
		"subject", subject,
		"message_size_bytes", len(data),
		"message_type", "V2",
		"queue", mp.queue,
		"index", mp.index)

	// Create transaction from message data
	transactionStart := time.Now()
	transaction, err := mp.createTransaction(ctx, data, subject)
	if err != nil {
		mp.logMessageError(logger, messageID, "transaction_creation", err, map[string]any{
			"subject":            subject,
			"message_size_bytes": len(data),
			"message_type":       "V2",
			"creation_duration":  time.Since(transactionStart),
		})
		logging.LogError(logger, constants.LogFailedCreateTransaction, err,
			"message_id", messageID,
			"subject", subject,
			"stage", "transaction_creation")
		return fmt.Errorf("%s: %w", constants.ErrCreateTransaction, err)
	}

	// Log transaction creation success with timing
	mp.logMessageMetrics(logger, messageID, "transaction_creation", time.Since(transactionStart), map[string]any{
		"transaction_id": transaction.ObjectType + "_" + transaction.Action + "_" + strconv.FormatInt(transaction.Timestamp.UnixNano(), 10),
		"action":         transaction.Action,
		"object_type":    transaction.ObjectType,
		"subject":        subject,
	})

	// Process the transaction
	processingStart := time.Now()
	result, err := mp.indexerService.ProcessTransaction(ctx, transaction, mp.index)
	if err != nil {
		mp.logMessageError(logger, messageID, "transaction_processing", err, map[string]any{
			"action":              transaction.Action,
			"object_type":         transaction.ObjectType,
			"subject":             subject,
			"processing_duration": time.Since(processingStart),
			"total_duration":      time.Since(startTime),
		})
		logging.LogError(logger, "Failed to process transaction", err,
			"message_id", messageID,
			"action", transaction.Action,
			"object_type", transaction.ObjectType,
			"subject", subject,
			"stage", "transaction_processing")
		return fmt.Errorf("failed to process transaction: %w", err)
	}

	// Log transaction processing success with timing
	mp.logMessageMetrics(logger, messageID, "transaction_processing", time.Since(processingStart), map[string]any{
		"document_id":        result.DocumentID,
		"index_success":      result.IndexSuccess,
		"processing_success": result.Success,
	})

	// Trigger janitor check for the processed object
	janitorStart := time.Now()
	if result.Success && result.DocumentID != "" {
		mp.janitorService.CheckItem(result.DocumentID)
		logger.Debug("Janitor check triggered",
			"message_id", messageID,
			"document_id", result.DocumentID,
			"janitor_duration", time.Since(janitorStart))
	} else {
		logger.Warn("Janitor check skipped",
			"message_id", messageID,
			"result_success", result.Success,
			"document_id", result.DocumentID,
			"reason", "processing_failed_or_no_document_id")
	}

	// Log final success with comprehensive metrics
	totalDuration := time.Since(startTime)
	logger.Info("Indexing message processed successfully",
		"message_id", messageID,
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"document_id", result.DocumentID,
		"subject", subject,
		"total_duration", totalDuration,
		"total_duration_ms", totalDuration.Milliseconds(),
		"processing_duration", result.Duration,
		"processing_duration_ms", result.Duration.Milliseconds(),
		"index_success", result.IndexSuccess,
		"message_size_bytes", len(data))

	return nil
}

// ProcessV1IndexingMessage handles the complete V1 indexing message workflow
func (mp *MessageProcessor) ProcessV1IndexingMessage(ctx context.Context, data []byte, subject string) error {
	logger := logging.FromContext(ctx, mp.logger)
	messageID := mp.generateMessageID()
	startTime := time.Now()

	// Log message reception with metadata
	logger.Info("Processing V1 indexing message",
		"message_id", messageID,
		"subject", subject,
		"message_size_bytes", len(data),
		"message_type", "V1",
		"queue", mp.queue,
		"index", mp.index)

	// Create V1 transaction from message data
	transactionStart := time.Now()
	transaction, err := mp.createV1Transaction(ctx, data, subject)
	if err != nil {
		mp.logMessageError(logger, messageID, "v1_transaction_creation", err, map[string]any{
			"subject":            subject,
			"message_size_bytes": len(data),
			"message_type":       "V1",
			"creation_duration":  time.Since(transactionStart),
		})
		logging.LogError(logger, constants.LogFailedCreateV1Transaction, err,
			"message_id", messageID,
			"subject", subject,
			"stage", "v1_transaction_creation")
		return fmt.Errorf("%s: %w", constants.ErrCreateV1Transaction, err)
	}

	// Log transaction creation success with timing
	mp.logMessageMetrics(logger, messageID, "v1_transaction_creation", time.Since(transactionStart), map[string]any{
		"transaction_id": transaction.ObjectType + "_" + transaction.Action + "_" + strconv.FormatInt(transaction.Timestamp.UnixNano(), 10),
		"action":         transaction.Action,
		"object_type":    transaction.ObjectType,
		"subject":        subject,
		"has_v1_data":    transaction.V1Data != nil,
		"v1_data_fields": len(transaction.V1Data),
	})

	// Process the transaction (same processing path)
	processingStart := time.Now()
	result, err := mp.indexerService.ProcessTransaction(ctx, transaction, mp.index)
	if err != nil {
		mp.logMessageError(logger, messageID, "v1_transaction_processing", err, map[string]any{
			"action":              transaction.Action,
			"object_type":         transaction.ObjectType,
			"subject":             subject,
			"processing_duration": time.Since(processingStart),
			"total_duration":      time.Since(startTime),
			"has_v1_data":         transaction.V1Data != nil,
		})
		logging.LogError(logger, "Failed to process V1 transaction", err,
			"message_id", messageID,
			"action", transaction.Action,
			"object_type", transaction.ObjectType,
			"subject", subject,
			"stage", "v1_transaction_processing")
		return fmt.Errorf("failed to process V1 transaction: %w", err)
	}

	// Log transaction processing success with timing
	mp.logMessageMetrics(logger, messageID, "v1_transaction_processing", time.Since(processingStart), map[string]any{
		"document_id":        result.DocumentID,
		"index_success":      result.IndexSuccess,
		"processing_success": result.Success,
	})

	// Trigger janitor check for the processed object
	janitorStart := time.Now()
	if result.Success && result.DocumentID != "" {
		mp.janitorService.CheckItem(result.DocumentID)
		logger.Debug("Janitor check triggered for V1 message",
			"message_id", messageID,
			"document_id", result.DocumentID,
			"janitor_duration", time.Since(janitorStart))
	} else {
		logger.Warn("Janitor check skipped for V1 message",
			"message_id", messageID,
			"result_success", result.Success,
			"document_id", result.DocumentID,
			"reason", "processing_failed_or_no_document_id")
	}

	// Log final success with comprehensive metrics
	totalDuration := time.Since(startTime)
	logger.Info("V1 indexing message processed successfully",
		"message_id", messageID,
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"document_id", result.DocumentID,
		"subject", subject,
		"total_duration", totalDuration,
		"total_duration_ms", totalDuration.Milliseconds(),
		"processing_duration", result.Duration,
		"processing_duration_ms", result.Duration.Milliseconds(),
		"index_success", result.IndexSuccess,
		"message_size_bytes", len(data),
		"has_v1_data", transaction.V1Data != nil,
		"v1_data_fields", len(transaction.V1Data))

	return nil
}

// =================
// TRANSACTION CREATION METHODS (simplified using service)
// =================

// createTransaction creates an LFXTransaction from V2 message data
func (mp *MessageProcessor) createTransaction(ctx context.Context, data []byte, subject string) (*entities.LFXTransaction, error) {
	logger := logging.FromContext(ctx, mp.logger)
	startTime := time.Now()

	logger.Debug("Creating V2 transaction from message data",
		"subject", subject,
		"message_size_bytes", len(data),
		"message_type", "V2")

	// Parse message data
	var messageData map[string]any
	parseStart := time.Now()
	if err := json.Unmarshal(data, &messageData); err != nil {
		logger.Error("Failed to unmarshal V2 message data",
			"subject", subject,
			"message_size_bytes", len(data),
			"parse_duration", time.Since(parseStart),
			"error", err.Error(),
			"data_preview", string(data[:min(len(data), 200)]))
		return nil, fmt.Errorf("failed to unmarshal message data: %w", err)
	}

	logger.Debug("V2 message data parsed successfully",
		"subject", subject,
		"parse_duration", time.Since(parseStart),
		"message_fields", len(messageData),
		"has_action", messageData["action"] != nil,
		"has_data", messageData["data"] != nil,
		"has_headers", messageData["headers"] != nil)

	// Extract object type from subject (V2: lfx.index.{object_type})
	objectType, found := strings.CutPrefix(subject, constants.IndexPrefix)
	if !found {
		logger.Error("Invalid V2 subject format",
			"subject", subject,
			"expected_prefix", constants.IndexPrefix,
			"subject_validation_duration", time.Since(startTime))
		return nil, fmt.Errorf("invalid V2 subject format: %s", subject)
	}

	logger.Debug("V2 object type extracted from subject",
		"subject", subject,
		"object_type", objectType,
		"subject_validation_duration", time.Since(startTime))

	// Use service method for transaction creation
	serviceStart := time.Now()
	transaction, err := mp.indexerService.CreateTransactionFromMessage(messageData, objectType, false)
	if err != nil {
		logger.Error("Failed to create V2 transaction via service",
			"subject", subject,
			"object_type", objectType,
			"service_duration", time.Since(serviceStart),
			"total_duration", time.Since(startTime),
			"error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrCreateV2Transaction, err)
	}

	logger.Debug("V2 transaction created successfully",
		"subject", subject,
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"service_duration", time.Since(serviceStart),
		"total_creation_duration", time.Since(startTime),
		"transaction_timestamp", transaction.Timestamp,
		"header_count", len(transaction.Headers))

	return transaction, nil
}

// createV1Transaction creates an LFXTransaction from V1 message data
func (mp *MessageProcessor) createV1Transaction(ctx context.Context, data []byte, subject string) (*entities.LFXTransaction, error) {
	logger := logging.FromContext(ctx, mp.logger)
	startTime := time.Now()

	logger.Debug("Creating V1 transaction from message data",
		"subject", subject,
		"message_size_bytes", len(data),
		"message_type", "V1")

	// Parse message data
	var messageData map[string]any
	parseStart := time.Now()
	if err := json.Unmarshal(data, &messageData); err != nil {
		logger.Error("Failed to unmarshal V1 message data",
			"subject", subject,
			"message_size_bytes", len(data),
			"parse_duration", time.Since(parseStart),
			"error", err.Error(),
			"data_preview", string(data[:min(len(data), 200)]))
		return nil, fmt.Errorf("failed to unmarshal V1 message data: %w", err)
	}

	logger.Debug("V1 message data parsed successfully",
		"subject", subject,
		"parse_duration", time.Since(parseStart),
		"message_fields", len(messageData),
		"has_action", messageData["action"] != nil,
		"has_data", messageData["data"] != nil,
		"has_headers", messageData["headers"] != nil,
		"has_v1_data", messageData["v1_data"] != nil)

	// Extract object type from subject (V1: lfx.v1.index.{object_type})
	objectType, found := strings.CutPrefix(subject, constants.FromV1Prefix)
	if !found {
		logger.Error("Invalid V1 subject format",
			"subject", subject,
			"expected_prefix", constants.FromV1Prefix,
			"subject_validation_duration", time.Since(startTime))
		return nil, fmt.Errorf("invalid V1 subject format: %s", subject)
	}

	logger.Debug("V1 object type extracted from subject",
		"subject", subject,
		"object_type", objectType,
		"subject_validation_duration", time.Since(startTime))

	// Use service method for transaction creation
	serviceStart := time.Now()
	transaction, err := mp.indexerService.CreateTransactionFromMessage(messageData, objectType, true)
	if err != nil {
		logger.Error("Failed to create V1 transaction via service",
			"subject", subject,
			"object_type", objectType,
			"service_duration", time.Since(serviceStart),
			"total_duration", time.Since(startTime),
			"error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrCreateV1Transaction, err)
	}

	logger.Debug("V1 transaction created successfully",
		"subject", subject,
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"service_duration", time.Since(serviceStart),
		"total_creation_duration", time.Since(startTime),
		"transaction_timestamp", transaction.Timestamp,
		"header_count", len(transaction.Headers),
		"has_v1_data", transaction.V1Data != nil,
		"v1_data_fields", len(transaction.V1Data))

	return transaction, nil
}

// =================
// SUBSCRIPTION METHODS (streamlined from MessageProcessor)
// =================

// StartSubscriptions starts all NATS subscriptions (consolidated)
func (mp *MessageProcessor) StartSubscriptions(ctx context.Context) error {
	logger := logging.FromContext(ctx, mp.logger)
	startTime := time.Now()

	logger.Info("Starting NATS subscriptions for indexing workflow",
		"queue", mp.queue,
		"index", mp.index,
		"v2_subjects", constants.AllSubjects,
		"v1_subjects", constants.AllV1Subjects)

	// Subscribe to V2 indexing messages
	v2Start := time.Now()
	if err := mp.subscribeTo(ctx, constants.AllSubjects, "V2 indexing messages"); err != nil {
		logger.Error("Failed to subscribe to V2 messages",
			"subjects", constants.AllSubjects,
			"queue", mp.queue,
			"v2_subscription_duration", time.Since(v2Start),
			"total_duration", time.Since(startTime),
			"error", err.Error())
		return fmt.Errorf("failed to subscribe to V2 messages: %w", err)
	}

	logger.Info("V2 subscription successful",
		"subjects", constants.AllSubjects,
		"queue", mp.queue,
		"v2_subscription_duration", time.Since(v2Start))

	// Subscribe to V1 indexing messages
	v1Start := time.Now()
	if err := mp.subscribeTo(ctx, constants.AllV1Subjects, "V1 indexing messages"); err != nil {
		logger.Error("Failed to subscribe to V1 messages",
			"subjects", constants.AllV1Subjects,
			"queue", mp.queue,
			"v1_subscription_duration", time.Since(v1Start),
			"total_duration", time.Since(startTime),
			"error", err.Error())
		return fmt.Errorf("failed to subscribe to V1 messages: %w", err)
	}

	logger.Info("V1 subscription successful",
		"subjects", constants.AllV1Subjects,
		"queue", mp.queue,
		"v1_subscription_duration", time.Since(v1Start))

	totalDuration := time.Since(startTime)
	logger.Info("All NATS subscriptions started successfully",
		"total_duration", totalDuration,
		"total_duration_ms", totalDuration.Milliseconds(),
		"v2_subjects", constants.AllSubjects,
		"v1_subjects", constants.AllV1Subjects,
		"queue", mp.queue)

	return nil
}

// subscribeTo subscribes to a NATS subject with the message handler
func (mp *MessageProcessor) subscribeTo(ctx context.Context, subject, description string) error {
	logger := logging.FromContext(ctx, mp.logger)
	startTime := time.Now()

	logger.Info("Subscribing to NATS subject",
		"subject", subject,
		"queue", mp.queue,
		"description", description)

	// Create handler that routes to appropriate processing method
	handler := &indexingHandler{useCase: mp}

	// Attempt subscription with detailed error handling
	subscriptionStart := time.Now()
	if err := mp.messagingRepo.QueueSubscribeWithReply(ctx, subject, mp.queue, handler); err != nil {
		subscriptionDuration := time.Since(subscriptionStart)
		logger.Error("Failed to subscribe to NATS subject",
			"subject", subject,
			"queue", mp.queue,
			"description", description,
			"subscription_duration", subscriptionDuration,
			"total_duration", time.Since(startTime),
			"error", err.Error(),
			"error_type", fmt.Sprintf("%T", err))

		logging.LogError(logger, "Failed to subscribe to NATS subject", err,
			"subject", subject,
			"queue", mp.queue,
			"description", description,
			"subscription_duration", subscriptionDuration)

		return fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}

	subscriptionDuration := time.Since(subscriptionStart)
	logger.Info("Successfully subscribed to NATS subject",
		"subject", subject,
		"queue", mp.queue,
		"description", description,
		"subscription_duration", subscriptionDuration,
		"subscription_duration_ms", subscriptionDuration.Milliseconds(),
		"total_duration", time.Since(startTime),
		"handler_type", fmt.Sprintf("%T", handler))

	return nil
}

// indexingHandler handles NATS messages and routes them to the appropriate message processor method
type indexingHandler struct {
	useCase *MessageProcessor
}

// HandleWithReply processes messages with reply support
func (h *indexingHandler) HandleWithReply(ctx context.Context, data []byte, subject string, reply func([]byte) error) error {
	startTime := time.Now()
	messageID := h.useCase.generateMessageID()
	logger := logging.FromContext(ctx, h.useCase.logger)

	logger.Debug("Handling message with reply",
		"message_id", messageID,
		"subject", subject,
		"message_size_bytes", len(data),
		"has_reply_func", reply != nil)

	// Route to appropriate processing method based on subject
	var err error
	var messageType string

	processingStart := time.Now()
	if strings.HasPrefix(subject, constants.FromV1Prefix) {
		messageType = "V1"
		err = h.useCase.ProcessV1IndexingMessage(ctx, data, subject)
	} else {
		messageType = "V2"
		err = h.useCase.ProcessIndexingMessage(ctx, data, subject)
	}
	processingDuration := time.Since(processingStart)

	// Log processing results
	if err != nil {
		logger.Error("Message processing failed in handler",
			"message_id", messageID,
			"subject", subject,
			"message_type", messageType,
			"processing_duration", processingDuration,
			"total_duration", time.Since(startTime),
			"error", err.Error(),
			"has_reply_func", reply != nil)
	} else {
		logger.Debug("Message processing succeeded in handler",
			"message_id", messageID,
			"subject", subject,
			"message_type", messageType,
			"processing_duration", processingDuration,
			"total_duration", time.Since(startTime))
	}

	// Handle reply if function provided
	if reply != nil {
		replyStart := time.Now()
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
					"reply_error", replyErr.Error(),
					"reply_duration", time.Since(replyStart))
			} else {
				logger.Debug("Error reply sent successfully",
					"message_id", messageID,
					"subject", subject,
					"message_type", messageType,
					"reply_duration", time.Since(replyStart))
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
					"reply_error", replyErr.Error(),
					"reply_duration", time.Since(replyStart))
			} else {
				logger.Debug("Success reply sent successfully",
					"message_id", messageID,
					"subject", subject,
					"message_type", messageType,
					"reply_duration", time.Since(replyStart))
			}
		}
	} else {
		logger.Debug("No reply function provided",
			"message_id", messageID,
			"subject", subject,
			"message_type", messageType)
	}

	// Log final handler metrics
	totalDuration := time.Since(startTime)
	logger.Info("Message handler completed",
		"message_id", messageID,
		"subject", subject,
		"message_type", messageType,
		"success", err == nil,
		"total_duration", totalDuration,
		"total_duration_ms", totalDuration.Milliseconds(),
		"processing_duration", processingDuration,
		"processing_duration_ms", processingDuration.Milliseconds())

	return err
}

// =================
// HELPER METHODS FOR ENHANCED LOGGING
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

// logMessageMetrics logs detailed message processing metrics
func (mp *MessageProcessor) logMessageMetrics(logger *slog.Logger, messageID string, stage string, duration time.Duration, metadata map[string]any) {
	logData := []any{
		"message_id", messageID,
		"stage", stage,
		"duration", duration,
		"duration_ms", duration.Milliseconds(),
	}

	// Add metadata fields
	for k, v := range metadata {
		logData = append(logData, k, v)
	}

	logger.Info("Message processing metrics", logData...)
}

// logMessageError logs enhanced error information with context
func (mp *MessageProcessor) logMessageError(logger *slog.Logger, messageID string, stage string, err error, metadata map[string]any) {
	logData := []any{
		"message_id", messageID,
		"stage", stage,
		"error", err.Error(),
		"error_type", fmt.Sprintf("%T", err),
	}

	// Add metadata fields
	for k, v := range metadata {
		logData = append(logData, k, v)
	}

	logging.LogError(logger, "Message processing error", err, logData...)
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
