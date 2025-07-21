// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/linuxfoundation/lfx-indexer-service/internal/application"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
)

// IndexingMessageHandler handles both V2 and V1 indexing messages from NATS
// Consolidated to eliminate code duplication and over-engineering
// Following clean architecture: only handles presentation concerns (NATS protocol)
type IndexingMessageHandler struct {
	messageProcessor *application.MessageProcessor
}

// NewIndexingMessageHandler creates a new unified indexing message handler
func NewIndexingMessageHandler(messageProcessor *application.MessageProcessor) *IndexingMessageHandler {
	return &IndexingMessageHandler{
		messageProcessor: messageProcessor,
	}
}

// Handle processes indexing messages (legacy interface for backwards compatibility)
func (h *IndexingMessageHandler) Handle(ctx context.Context, data []byte, subject string) error {
	// Determine message version and delegate to appropriate processor method
	if strings.HasPrefix(subject, constants.FromV1Prefix) {
		return h.messageProcessor.ProcessV1IndexingMessage(ctx, data, subject)
	}
	return h.messageProcessor.ProcessIndexingMessage(ctx, data, subject)
}

// HandleWithReply processes indexing messages with NATS reply support
func (h *IndexingMessageHandler) HandleWithReply(ctx context.Context, data []byte, subject string, reply func([]byte) error) error {
	// Determine message version and process accordingly
	var err error
	if strings.HasPrefix(subject, constants.FromV1Prefix) {
		// Process V1 message
		err = h.messageProcessor.ProcessV1IndexingMessage(ctx, data, subject)
	} else {
		// Process V2 message
		err = h.messageProcessor.ProcessIndexingMessage(ctx, data, subject)
	}

	if err != nil {
		// Handle error response using integrated error handling with context
		h.respondErrorWithContext(ctx, reply, err, "error processing indexing message", subject)
		return err
	}

	// Send success response using integrated success handling with context
	h.respondSuccessWithContext(ctx, reply, subject)
	return nil
}

// respondErrorWithContext handles error responses with full context and correlation
func (h *IndexingMessageHandler) respondErrorWithContext(ctx context.Context, reply func([]byte) error, err error, msg string, subject string) {
	// Create base logger or use context logger if available
	var logger *slog.Logger
	if requestID := logging.GetRequestID(ctx); requestID != "" {
		logger = logging.FromContext(ctx, slog.Default())
	} else {
		logger = slog.Default()
	}

	// Enhanced error logging with context
	logger.Error("NATS message handler error",
		"error", err.Error(),
		"message", msg,
		"subject", subject,
		"has_reply", reply != nil,
		"request_id", logging.GetRequestID(ctx))

	// Send error response to NATS (presentation layer concern)
	if reply != nil {
		errorMsg := fmt.Sprintf("ERROR: %s", msg)
		if replyErr := reply([]byte(errorMsg)); replyErr != nil {
			logger.Error("Failed to send error reply to NATS",
				"original_error", err.Error(),
				"reply_error", replyErr.Error(),
				"subject", subject,
				"request_id", logging.GetRequestID(ctx))
		} else {
			logger.Debug("Error reply sent successfully",
				"subject", subject,
				"error_message", msg,
				"request_id", logging.GetRequestID(ctx))
		}
	} else {
		logger.Warn("No reply function available for error response",
			"subject", subject,
			"error", err.Error(),
			"request_id", logging.GetRequestID(ctx))
	}
}

// respondSuccessWithContext handles success responses
func (h *IndexingMessageHandler) respondSuccessWithContext(ctx context.Context, reply func([]byte) error, subject string) {
	// Create base logger or use context logger if available
	var logger *slog.Logger
	if requestID := logging.GetRequestID(ctx); requestID != "" {
		logger = logging.FromContext(ctx, slog.Default())
	} else {
		logger = slog.Default()
	}

	if reply != nil {
		if replyErr := reply([]byte("OK")); replyErr != nil {
			logger.Error("Failed to send success reply to NATS",
				"subject", subject,
				"reply_error", replyErr.Error(),
				"request_id", logging.GetRequestID(ctx))
		} else {
			logger.Info("Success reply sent to NATS",
				"subject", subject,
				"request_id", logging.GetRequestID(ctx))
		}
	} else {
		logger.Debug("No reply function available for success response",
			"subject", subject,
			"request_id", logging.GetRequestID(ctx))
	}
}
