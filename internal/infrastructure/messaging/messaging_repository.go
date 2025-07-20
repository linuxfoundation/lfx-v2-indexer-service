// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/auth"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
	"github.com/nats-io/nats.go"
)

// MessagingRepository implements the domain MessagingRepository interface
type MessagingRepository struct {
	conn           *nats.Conn
	authRepo       *auth.AuthRepository // Embedded for auth delegation
	logger         *slog.Logger
	subscriptions  []*nats.Subscription
	mu             sync.RWMutex
	drainTimeout   time.Duration
	isShuttingDown bool
}

// NewMessagingRepository creates a new NATS messaging repository with auth delegation
func NewMessagingRepository(conn *nats.Conn, authRepo *auth.AuthRepository, logger *slog.Logger, drainTimeout time.Duration) *MessagingRepository {
	msgLogger := logging.WithComponent(logger, constants.ComponentNATS)

	repo := &MessagingRepository{
		conn:           conn,
		authRepo:       authRepo,
		logger:         msgLogger,
		subscriptions:  make([]*nats.Subscription, 0),
		drainTimeout:   drainTimeout,
		isShuttingDown: false,
	}

	// Log initialization with connection diagnostics
	connInfo := repo.getConnectionInfo()
	msgLogger.Info("NATS messaging repository initialized",
		"drain_timeout", drainTimeout,
		"connection_status", connInfo["status"],
		"connected", connInfo["connected"],
		"auth_repo_configured", authRepo != nil)

	repo.logConnectionState("initialization")
	return repo
}

// =================
// NATS MESSAGE OPERATIONS (preserved from MessageRepository)
// =================

// Subscribe subscribes to NATS messages
func (r *MessagingRepository) Subscribe(ctx context.Context, subject string, handler contracts.MessageHandler) error {
	startTime := time.Now()
	subscriptionID := r.generateMessageID()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Info("Creating NATS subscription",
		"subscription_id", subscriptionID,
		"subject", subject,
		"current_subscriptions", len(r.subscriptions))

	r.logConnectionState("before_subscribe")

	// Create the NATS message handler with enhanced logging
	natsHandler := func(msg *nats.Msg) {
		msgStartTime := time.Now()
		msgID := r.generateMessageID()

		// Generate request_id at NATS entry point
		ctx, logger := logging.WithRequestID(context.Background(), r.logger)

		logger.Info("NATS message received",
			"message_id", msgID,
			"subscription_id", subscriptionID,
			"subject", msg.Subject,
			"size", len(msg.Data),
			"data_preview", fmt.Sprintf("%.50s...", string(msg.Data)))

		// Handle the message with performance tracking
		if err := handler.Handle(ctx, msg.Data, msg.Subject); err != nil {
			errorType := r.classifyNATSError(err)
			logger.Error("Message handler failed",
				"message_id", msgID,
				"subscription_id", subscriptionID,
				"error", err.Error(),
				"error_type", errorType,
				"processing_duration", time.Since(msgStartTime))
		} else {
			logger.Debug("Message handled successfully",
				"message_id", msgID,
				"subscription_id", subscriptionID,
				"processing_duration", time.Since(msgStartTime))
		}
	}

	// Subscribe to the subject
	sub, err := r.conn.Subscribe(subject, natsHandler)
	if err != nil {
		errorType := r.classifyNATSError(err)
		r.logger.Error("Failed to subscribe to NATS",
			"subscription_id", subscriptionID,
			"subject", subject,
			"error", err.Error(),
			"error_type", errorType,
			"duration", time.Since(startTime))
		return fmt.Errorf("failed to subscribe to subject %s: %w", subject, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS subscription created successfully",
		"subscription_id", subscriptionID,
		"subject", subject,
		"total_subscriptions", len(r.subscriptions),
		"setup_duration", time.Since(startTime))

	r.logConnectionState("after_subscribe")
	return nil
}

// QueueSubscribe subscribes to NATS messages with queue group for load balancing
func (r *MessagingRepository) QueueSubscribe(ctx context.Context, subject string, queue string, handler contracts.MessageHandler) error {
	startTime := time.Now()
	subscriptionID := r.generateMessageID()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Info("Creating NATS queue subscription",
		"subscription_id", subscriptionID,
		"subject", subject,
		"queue", queue,
		"current_subscriptions", len(r.subscriptions))

	r.logConnectionState("before_queue_subscribe")

	// Create the NATS message handler with enhanced logging
	natsHandler := func(msg *nats.Msg) {
		msgStartTime := time.Now()
		msgID := r.generateMessageID()

		// Generate request_id at NATS entry point
		ctx, logger := logging.WithRequestID(context.Background(), r.logger)

		logger.Info("NATS queue message received",
			"message_id", msgID,
			"subscription_id", subscriptionID,
			"subject", msg.Subject,
			"queue", queue,
			"size", len(msg.Data),
			"data_preview", fmt.Sprintf("%.50s...", string(msg.Data)))

		// Handle the message with performance tracking
		if err := handler.Handle(ctx, msg.Data, msg.Subject); err != nil {
			errorType := r.classifyNATSError(err)
			logger.Error("Queue message handler failed",
				"message_id", msgID,
				"subscription_id", subscriptionID,
				"queue", queue,
				"error", err.Error(),
				"error_type", errorType,
				"processing_duration", time.Since(msgStartTime))
		} else {
			logger.Debug("Queue message handled successfully",
				"message_id", msgID,
				"subscription_id", subscriptionID,
				"queue", queue,
				"processing_duration", time.Since(msgStartTime))
		}
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		errorType := r.classifyNATSError(err)
		r.logger.Error("Failed to queue subscribe to NATS",
			"subscription_id", subscriptionID,
			"subject", subject,
			"queue", queue,
			"error", err.Error(),
			"error_type", errorType,
			"duration", time.Since(startTime))
		return fmt.Errorf("failed to queue subscribe to subject %s with queue %s: %w", subject, queue, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS queue subscription created successfully",
		"subscription_id", subscriptionID,
		"subject", subject,
		"queue", queue,
		"total_subscriptions", len(r.subscriptions),
		"setup_duration", time.Since(startTime))

	r.logConnectionState("after_queue_subscribe")
	return nil
}

// QueueSubscribeWithReply subscribes to NATS messages with queue group and reply support
func (r *MessagingRepository) QueueSubscribeWithReply(ctx context.Context, subject string, queue string, handler contracts.MessageHandlerWithReply) error {
	startTime := time.Now()
	subscriptionID := r.generateMessageID()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Info("Creating NATS queue subscription with reply",
		"subscription_id", subscriptionID,
		"subject", subject,
		"queue", queue,
		"current_subscriptions", len(r.subscriptions))

	r.logConnectionState("before_queue_subscribe_reply")

	// Create the NATS message handler with reply support and enhanced logging
	natsHandler := func(msg *nats.Msg) {
		msgStartTime := time.Now()
		msgID := r.generateMessageID()

		// Generate request_id at NATS entry point
		ctx, logger := logging.WithRequestID(context.Background(), r.logger)

		logger.Info("NATS queue message with reply received",
			"message_id", msgID,
			"subscription_id", subscriptionID,
			"subject", msg.Subject,
			"queue", queue,
			"size", len(msg.Data),
			"has_reply", msg.Reply != "",
			"reply_subject", msg.Reply,
			"data_preview", fmt.Sprintf("%.50s...", string(msg.Data)))

		// Create reply function if message has reply subject
		var replyFunc func([]byte) error
		if msg.Reply != "" {
			replyFunc = func(data []byte) error {
				replyStartTime := time.Now()

				logger.Debug("Sending reply to NATS message",
					"message_id", msgID,
					"reply_subject", msg.Reply,
					"reply_size", len(data))

				err := msg.Respond(data)
				if err != nil {
					errorType := r.classifyNATSError(err)
					logger.Error("Failed to send reply",
						"message_id", msgID,
						"reply_subject", msg.Reply,
						"error", err.Error(),
						"error_type", errorType,
						"reply_duration", time.Since(replyStartTime))
				} else {
					logger.Debug("Reply sent successfully",
						"message_id", msgID,
						"reply_subject", msg.Reply,
						"reply_size", len(data),
						"reply_duration", time.Since(replyStartTime))
				}

				return err
			}
		}

		// Handle the message with reply support and performance tracking
		if err := handler.HandleWithReply(ctx, msg.Data, msg.Subject, replyFunc); err != nil {
			errorType := r.classifyNATSError(err)
			logger.Error("Queue message with reply handler failed",
				"message_id", msgID,
				"subscription_id", subscriptionID,
				"queue", queue,
				"has_reply", msg.Reply != "",
				"error", err.Error(),
				"error_type", errorType,
				"processing_duration", time.Since(msgStartTime))
		} else {
			logger.Debug("Queue message with reply handled successfully",
				"message_id", msgID,
				"subscription_id", subscriptionID,
				"queue", queue,
				"has_reply", msg.Reply != "",
				"processing_duration", time.Since(msgStartTime))
		}
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		errorType := r.classifyNATSError(err)
		r.logger.Error("Failed to queue subscribe with reply to NATS",
			"subscription_id", subscriptionID,
			"subject", subject,
			"queue", queue,
			"error", err.Error(),
			"error_type", errorType,
			"duration", time.Since(startTime))
		return fmt.Errorf("failed to queue subscribe with reply to subject %s with queue %s: %w", subject, queue, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS queue subscription with reply created successfully",
		"subscription_id", subscriptionID,
		"subject", subject,
		"queue", queue,
		"total_subscriptions", len(r.subscriptions),
		"setup_duration", time.Since(startTime))

	r.logConnectionState("after_queue_subscribe_reply")
	return nil
}

// Publish publishes a message to NATS
func (r *MessagingRepository) Publish(ctx context.Context, subject string, data []byte) error {
	startTime := time.Now()
	msgID := r.generateMessageID()

	logger := logging.FromContext(ctx, r.logger)

	logger.Info("Publishing message to NATS",
		"message_id", msgID,
		"subject", subject,
		"size", len(data),
		"data_preview", fmt.Sprintf("%.50s...", string(data)))

	r.logConnectionState("before_publish")

	// Check connection state before publishing
	if !r.IsConnected() {
		r.logger.Error("Cannot publish: NATS connection not available",
			"message_id", msgID,
			"subject", subject,
			"connection_status", r.getConnectionInfo()["status"])
		return fmt.Errorf("NATS connection not available for publishing to subject %s", subject)
	}

	if err := r.conn.Publish(subject, data); err != nil {
		errorType := r.classifyNATSError(err)
		logger.Error("Failed to publish message to NATS",
			"message_id", msgID,
			"subject", subject,
			"size", len(data),
			"error", err.Error(),
			"error_type", errorType,
			"duration", time.Since(startTime))
		return fmt.Errorf("failed to publish message to subject %s: %w", subject, err)
	}

	logger.Info("Message published to NATS successfully",
		"message_id", msgID,
		"subject", subject,
		"size", len(data),
		"publish_duration", time.Since(startTime))

	return nil
}

// DrainWithTimeout performs graceful NATS connection drain with timeout
func (r *MessagingRepository) DrainWithTimeout() error {
	startTime := time.Now()

	r.mu.Lock()
	r.isShuttingDown = true
	totalSubscriptions := len(r.subscriptions)
	r.mu.Unlock()

	r.logger.Info("Starting NATS graceful drain sequence",
		"timeout", r.drainTimeout,
		"subscription_count", totalSubscriptions,
		"connection_status", r.getConnectionInfo()["status"])

	r.logConnectionState("before_drain")

	if r.conn == nil {
		r.logger.Warn("NATS connection is nil, skipping drain")
		return nil
	}

	if r.conn.IsClosed() {
		r.logger.Info("NATS connection already closed, no drain needed")
		return nil
	}

	if r.conn.IsDraining() {
		r.logger.Info("NATS connection already draining, waiting for completion")
		return nil
	}

	// Log subscription health before drain
	r.logSubscriptionHealth()

	// Start drain process
	drainStart := time.Now()
	r.logger.Debug("Initiating NATS connection drain")

	if err := r.conn.Drain(); err != nil {
		errorType := r.classifyNATSError(err)
		r.logger.Error("Failed to start NATS drain",
			"error", err.Error(),
			"error_type", errorType,
			"drain_attempt_duration", time.Since(drainStart),
			"total_duration", time.Since(startTime))
		return fmt.Errorf("failed to drain NATS connection: %w", err)
	}

	// Wait for drain to complete and log progress
	drainCompleted := false
	progressTicker := time.NewTicker(1 * time.Second)
	defer progressTicker.Stop()

	go func() {
		for range progressTicker.C {
			if r.conn.IsClosed() {
				drainCompleted = true
				return
			}
			r.logger.Debug("NATS drain in progress",
				"elapsed", time.Since(drainStart),
				"status", r.conn.Status().String())
		}
	}()

	// Wait for drain completion or timeout
	select {
	case <-time.After(r.drainTimeout):
		r.logger.Warn("NATS drain timeout reached",
			"timeout", r.drainTimeout,
			"final_status", r.conn.Status().String())
	case <-func() <-chan struct{} {
		done := make(chan struct{})
		go func() {
			for !drainCompleted {
				time.Sleep(100 * time.Millisecond)
			}
			close(done)
		}()
		return done
	}():
		// Drain completed successfully
	}

	r.logger.Info("NATS drain completed successfully",
		"drain_duration", time.Since(drainStart),
		"total_duration", time.Since(startTime),
		"final_status", r.conn.Status().String(),
		"subscriptions_processed", totalSubscriptions)

	r.logConnectionState("after_drain")
	return nil
}

// Close closes the NATS connection and all subscriptions
func (r *MessagingRepository) Close() error {
	startTime := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	logger := r.logger
	totalSubscriptions := len(r.subscriptions)

	logger.Info("Initiating NATS repository close sequence",
		"total_subscriptions", totalSubscriptions,
		"is_shutting_down", r.isShuttingDown,
		"connection_status", r.getConnectionInfo()["status"])

	// First, attempt graceful drain if not already shutting down
	if !r.isShuttingDown {
		logger.Info("Close called without drain, attempting graceful drain first")
		r.mu.Unlock() // Unlock to call DrainWithTimeout
		if err := r.DrainWithTimeout(); err != nil {
			logger.Warn("Graceful drain failed, proceeding with immediate close",
				"error", err.Error(),
				"error_type", r.classifyNATSError(err))
		}
		r.mu.Lock() // Re-lock for the rest of the method
	}

	// Unsubscribe from all subscriptions with detailed tracking
	subscriptionErrors := 0
	for i, sub := range r.subscriptions {
		if sub == nil {
			logger.Warn("Null subscription found during cleanup", "index", i)
			continue
		}

		if !sub.IsValid() {
			logger.Debug("Invalid subscription found during cleanup", "index", i)
			continue
		}

		logger.Debug("Unsubscribing from subscription",
			"index", i,
			"subject", sub.Subject,
			"queue", sub.Queue)

		if err := sub.Unsubscribe(); err != nil {
			subscriptionErrors++
			logger.Error("Failed to unsubscribe from subscription",
				"index", i,
				"subject", sub.Subject,
				"queue", sub.Queue,
				"error", err.Error(),
				"error_type", r.classifyNATSError(err))
		} else {
			logger.Debug("Successfully unsubscribed from subscription",
				"index", i,
				"subject", sub.Subject,
				"queue", sub.Queue)
		}
	}

	// Clear subscriptions array
	r.subscriptions = nil

	logger.Info("Subscription cleanup completed",
		"total_subscriptions", totalSubscriptions,
		"subscription_errors", subscriptionErrors,
		"success_rate", fmt.Sprintf("%.2f%%", float64(totalSubscriptions-subscriptionErrors)/float64(totalSubscriptions)*100))

	// Close the connection if not already closed
	if r.conn != nil && !r.conn.IsClosed() {
		logger.Info("Closing NATS connection",
			"connection_status", r.conn.Status().String())

		r.conn.Close()

		logger.Info("NATS connection closed successfully")
	} else {
		logger.Info("NATS connection already closed or nil")
	}

	logger.Info("NATS repository close sequence completed",
		"total_duration", time.Since(startTime),
		"subscription_errors", subscriptionErrors,
		"final_connection_status", r.getConnectionInfo()["status"])

	return nil
}

// HealthCheck checks the health of the NATS connection
func (r *MessagingRepository) HealthCheck(ctx context.Context) error {
	startTime := time.Now()

	r.logger.Debug("NATS health check initiated")

	// Check basic connection state
	if r.conn == nil {
		r.logger.Error("NATS health check failed: connection is nil")
		return fmt.Errorf("%s: NATS connection is nil", constants.ErrHealthCheck)
	}

	connInfo := r.getConnectionInfo()

	r.logger.Debug("NATS connection diagnostics",
		"status", connInfo["status"],
		"connected", connInfo["connected"],
		"reconnecting", connInfo["reconnecting"],
		"closed", connInfo["closed"],
		"draining", connInfo["draining"],
		"subscriptions", connInfo["subscriptions"])

	if !r.conn.IsConnected() {
		r.logger.Error("NATS health check failed: connection not connected",
			"status", connInfo["status"],
			"reconnecting", connInfo["reconnecting"])
		return fmt.Errorf("%s: NATS connection is not connected", constants.ErrHealthCheck)
	}

	// Check if we can get server info
	if r.conn.Status() != nats.CONNECTED {
		r.logger.Error("NATS health check failed: connection status not connected",
			"status", r.conn.Status().String(),
			"expected", nats.CONNECTED.String())
		return fmt.Errorf("%s: NATS connection status is not connected: %s", constants.ErrHealthCheck, r.conn.Status())
	}

	// Check subscription health
	r.mu.RLock()
	totalSubs := len(r.subscriptions)
	activeSubs := 0
	for _, sub := range r.subscriptions {
		if sub != nil && sub.IsValid() {
			activeSubs++
		}
	}
	r.mu.RUnlock()

	healthRatio := float64(activeSubs) / float64(totalSubs) * 100
	if totalSubs > 0 && healthRatio < 90 {
		r.logger.Warn("NATS subscription health degraded",
			"total_subscriptions", totalSubs,
			"active_subscriptions", activeSubs,
			"health_ratio", fmt.Sprintf("%.2f%%", healthRatio))
	}

	// Check auth repository if configured
	if r.authRepo != nil {
		if err := r.authRepo.HealthCheck(ctx); err != nil {
			r.logger.Warn("NATS auth repository health check failed",
				"error", err.Error())
			// Don't fail the overall health check for auth issues
		}
	}

	r.logger.Info("NATS health check completed successfully",
		"status", "healthy",
		"connection_status", connInfo["status"],
		"subscriptions", totalSubs,
		"active_subscriptions", activeSubs,
		"health_ratio", fmt.Sprintf("%.2f%%", healthRatio),
		"check_duration", time.Since(startTime))

	return nil
}

// =================
// AUTHENTICATION OPERATIONS (delegated to AuthRepository)
// =================

// ValidateToken validates a JWT token and returns principal information
func (r *MessagingRepository) ValidateToken(ctx context.Context, token string) (*entities.Principal, error) {
	startTime := time.Now()

	r.logger.Debug("NATS delegating token validation to auth repository")

	if r.authRepo == nil {
		r.logger.Error("Token validation failed: auth repository not configured")
		return nil, fmt.Errorf(constants.ErrAuthRepoNotConfigured)
	}

	principal, err := r.authRepo.ValidateToken(ctx, token)
	if err != nil {
		r.logger.Error("Token validation delegation failed",
			"error", err.Error(),
			"duration", time.Since(startTime))
		return nil, err
	}

	r.logger.Debug("Token validation delegation completed successfully",
		"principal", principal.Principal,
		"duration", time.Since(startTime))

	return principal, nil
}

// ParsePrincipals parses principals from HTTP headers with delegation support
func (r *MessagingRepository) ParsePrincipals(ctx context.Context, headers map[string]string) ([]entities.Principal, error) {
	startTime := time.Now()

	r.logger.Debug("NATS delegating principal parsing to auth repository",
		"headers_count", len(headers))

	if r.authRepo == nil {
		r.logger.Error("Principal parsing failed: auth repository not configured")
		return nil, fmt.Errorf(constants.ErrAuthRepoNotConfigured)
	}

	principals, err := r.authRepo.ParsePrincipals(ctx, headers)
	if err != nil {
		r.logger.Error("Principal parsing delegation failed",
			"error", err.Error(),
			"duration", time.Since(startTime))
		return nil, err
	}

	r.logger.Debug("Principal parsing delegation completed successfully",
		"principals_count", len(principals),
		"duration", time.Since(startTime))

	return principals, nil
}

// =================
// UTILITY METHODS (preserved)
// =================

// GetConnection returns the underlying NATS connection
func (r *MessagingRepository) GetConnection() *nats.Conn {
	return r.conn
}

// GetSubscriptionCount returns the number of active subscriptions
func (r *MessagingRepository) GetSubscriptionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.subscriptions)
}

// IsConnected checks if the NATS connection is active
func (r *MessagingRepository) IsConnected() bool {
	return r.conn != nil && r.conn.IsConnected()
}

// Helper methods for message tracking and performance monitoring

// generateMessageID generates a unique identifier for message correlation
func (r *MessagingRepository) generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

// classifyNATSError categorizes NATS errors for better monitoring
func (r *MessagingRepository) classifyNATSError(err error) string {
	if err == nil {
		return "none"
	}

	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "connection closed"):
		return "connection_closed"
	case strings.Contains(errStr, "no responders"):
		return "no_responders"
	case strings.Contains(errStr, "timeout"):
		return "timeout"
	case strings.Contains(errStr, "invalid subject"):
		return "invalid_subject"
	case strings.Contains(errStr, "permission denied"):
		return "permission_denied"
	case strings.Contains(errStr, "slow consumer"):
		return "slow_consumer"
	case strings.Contains(errStr, "connection refused"):
		return "connection_refused"
	case strings.Contains(errStr, "reconnecting"):
		return "reconnecting"
	default:
		return "unknown_error"
	}
}

// getConnectionInfo returns connection diagnostic information
func (r *MessagingRepository) getConnectionInfo() map[string]interface{} {
	if r.conn == nil {
		return map[string]interface{}{
			"status":        "nil",
			"connected":     false,
			"servers":       []string{},
			"subscriptions": 0,
		}
	}

	info := map[string]interface{}{
		"status":        r.conn.Status().String(),
		"connected":     r.conn.IsConnected(),
		"reconnecting":  r.conn.IsReconnecting(),
		"closed":        r.conn.IsClosed(),
		"draining":      r.conn.IsDraining(),
		"subscriptions": len(r.subscriptions),
	}

	if r.conn.ConnectedUrl() != "" {
		info["connected_url"] = r.conn.ConnectedUrl()
	}

	return info
}

// logConnectionState logs current connection state with context
func (r *MessagingRepository) logConnectionState(context string) {
	connInfo := r.getConnectionInfo()

	r.logger.Debug("NATS connection state",
		"context", context,
		"status", connInfo["status"],
		"connected", connInfo["connected"],
		"reconnecting", connInfo["reconnecting"],
		"closed", connInfo["closed"],
		"draining", connInfo["draining"],
		"subscriptions", connInfo["subscriptions"])
}

// logSubscriptionHealth logs subscription health metrics
func (r *MessagingRepository) logSubscriptionHealth() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	totalSubs := len(r.subscriptions)
	activeSubs := 0
	invalidSubs := 0

	for _, sub := range r.subscriptions {
		if sub.IsValid() {
			activeSubs++
		} else {
			invalidSubs++
		}
	}

	r.logger.Info("Subscription health check",
		"total_subscriptions", totalSubs,
		"active_subscriptions", activeSubs,
		"invalid_subscriptions", invalidSubs,
		"health_ratio", fmt.Sprintf("%.2f%%", float64(activeSubs)/float64(totalSubs)*100))
}

// GetMetrics returns messaging repository metrics for monitoring
func (r *MessagingRepository) GetMetrics() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	connInfo := r.getConnectionInfo()

	// Calculate subscription health
	totalSubs := len(r.subscriptions)
	activeSubs := 0
	for _, sub := range r.subscriptions {
		if sub != nil && sub.IsValid() {
			activeSubs++
		}
	}

	healthRatio := float64(0)
	if totalSubs > 0 {
		healthRatio = float64(activeSubs) / float64(totalSubs) * 100
	}

	return map[string]interface{}{
		"connection_status":    connInfo["status"],
		"connected":            connInfo["connected"],
		"reconnecting":         connInfo["reconnecting"],
		"closed":               connInfo["closed"],
		"draining":             connInfo["draining"],
		"total_subscriptions":  totalSubs,
		"active_subscriptions": activeSubs,
		"subscription_health":  fmt.Sprintf("%.2f%%", healthRatio),
		"is_shutting_down":     r.isShuttingDown,
		"drain_timeout":        r.drainTimeout,
		"auth_repo_configured": r.authRepo != nil,
	}
}

// GetConnectionStatus returns detailed connection status for monitoring
func (r *MessagingRepository) GetConnectionStatus() map[string]interface{} {
	connInfo := r.getConnectionInfo()

	status := map[string]interface{}{
		"component":          "messaging_repository",
		"status":             "initialized",
		"connection_info":    connInfo,
		"subscription_count": len(r.subscriptions),
		"is_shutting_down":   r.isShuttingDown,
	}

	if r.conn != nil && r.conn.IsConnected() {
		status["status"] = "connected"
		if r.conn.ConnectedUrl() != "" {
			status["connected_url"] = r.conn.ConnectedUrl()
		}
	} else if r.conn != nil && r.conn.IsClosed() {
		status["status"] = "closed"
	} else if r.conn != nil && r.conn.IsReconnecting() {
		status["status"] = "reconnecting"
	}

	return status
}
