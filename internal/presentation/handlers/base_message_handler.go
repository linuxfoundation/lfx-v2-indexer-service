package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/logging"
)

// BaseMessageHandler contains shared functionality for NATS message handlers
// Following DRY principle to eliminate code duplication
type BaseMessageHandler struct{}

// RespondError handles error responses with enhanced logging and context
// Shared implementation used by all message handlers
func (h *BaseMessageHandler) RespondError(reply func([]byte) error, err error, msg string) {
	h.RespondErrorWithContext(context.Background(), reply, err, msg, "")
}

// RespondErrorWithContext handles error responses with full context and correlation
func (h *BaseMessageHandler) RespondErrorWithContext(ctx context.Context, reply func([]byte) error, err error, msg string, subject string) {
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
		"request_id", logging.GetRequestID(ctx),
		"timestamp", time.Now().UTC())

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

// RespondSuccess handles success responses with enhanced logging
// Shared implementation used by all message handlers
func (h *BaseMessageHandler) RespondSuccess(reply func([]byte) error, subject string) {
	h.RespondSuccessWithContext(context.Background(), reply, subject)
}

// RespondSuccessWithContext handles success responses with full context and timing
func (h *BaseMessageHandler) RespondSuccessWithContext(ctx context.Context, reply func([]byte) error, subject string) {
	startTime := time.Now()

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
				"request_id", logging.GetRequestID(ctx),
				"response_time", time.Since(startTime))
		} else {
			logger.Info("Success reply sent to NATS",
				"subject", subject,
				"request_id", logging.GetRequestID(ctx),
				"response_time", time.Since(startTime),
				"timestamp", time.Now().UTC())
		}
	} else {
		logger.Debug("No reply function available for success response",
			"subject", subject,
			"request_id", logging.GetRequestID(ctx))
	}
}
