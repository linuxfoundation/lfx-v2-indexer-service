// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package messaging provides NATS-based messaging infrastructure for the indexer service.
package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
	"github.com/nats-io/nats.go"
)

// MessagingRepository implements the MessagingRepository interface for NATS operations
type MessagingRepository struct {
	conn           *nats.Conn
	authRepo       contracts.AuthRepository
	logger         *slog.Logger
	subscriptions  []*nats.Subscription
	mu             sync.RWMutex
	drainTimeout   time.Duration
	isShuttingDown bool
}

// NewMessagingRepository creates a new NATS messaging repository with auth delegation
func NewMessagingRepository(conn *nats.Conn, authRepo contracts.AuthRepository, logger *slog.Logger, drainTimeout time.Duration) *MessagingRepository {
	msgLogger := logging.WithComponent(logger, constants.ComponentNATS)

	repo := &MessagingRepository{
		conn:           conn,
		authRepo:       authRepo,
		logger:         msgLogger,
		subscriptions:  make([]*nats.Subscription, 0),
		drainTimeout:   drainTimeout,
		isShuttingDown: false,
	}

	// Log initialization
	msgLogger.Info("NATS messaging repository initialized", "drain_timeout", drainTimeout, "auth_repo_configured", authRepo != nil)

	return repo
}

// =================
// NATS MESSAGE OPERATIONS (preserved from MessageRepository)
// =================

// Subscribe subscribes to NATS messages
func (r *MessagingRepository) Subscribe(ctx context.Context, subject string, handler contracts.MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.InfoContext(ctx, "Creating NATS subscription", "subject", subject)

	// Create the NATS message handler
	natsHandler := func(msg *nats.Msg) {
		// Generate request_id at NATS entry point
		ctx, logger := logging.WithRequestID(context.Background(), r.logger)

		logger.Debug("NATS message received", "subject", msg.Subject, "size", len(msg.Data))

		// Handle the message
		if err := handler.Handle(ctx, msg.Data, msg.Subject); err != nil {
			logger.Error("Message handler failed", "error", err.Error())
		}
	}

	// Subscribe to the subject
	sub, err := r.conn.Subscribe(subject, natsHandler)
	if err != nil {
		r.logger.Error("Failed to subscribe to NATS", "subject", subject, "error", err.Error())
		return fmt.Errorf("failed to subscribe to subject %s: %w", subject, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS subscription created successfully", "subject", subject)
	return nil
}

// QueueSubscribe subscribes to NATS messages with queue group for load balancing
func (r *MessagingRepository) QueueSubscribe(ctx context.Context, subject string, queue string, handler contracts.MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.InfoContext(ctx, "Creating NATS queue subscription", "subject", subject, "queue", queue)

	// Create the NATS message handler
	natsHandler := func(msg *nats.Msg) {
		// Generate request_id at NATS entry point
		ctx, logger := logging.WithRequestID(context.Background(), r.logger)

		logger.Debug("NATS queue message received", "subject", msg.Subject, "queue", queue, "size", len(msg.Data))

		// Handle the message
		if err := handler.Handle(ctx, msg.Data, msg.Subject); err != nil {
			logger.Error("Queue message handler failed", "queue", queue, "error", err.Error())
		}
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		r.logger.Error("Failed to queue subscribe to NATS", "subject", subject, "queue", queue, "error", err.Error())
		return fmt.Errorf("failed to queue subscribe to subject %s with queue %s: %w", subject, queue, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS queue subscription created successfully", "subject", subject, "queue", queue)
	return nil
}

// QueueSubscribeWithReply subscribes to NATS messages with queue group and reply support
func (r *MessagingRepository) QueueSubscribeWithReply(ctx context.Context, subject string, queue string, handler contracts.MessageHandlerWithReply) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.InfoContext(ctx, "Creating NATS queue subscription with reply", "subject", subject, "queue", queue)

	// Create the NATS message handler with reply support
	natsHandler := func(msg *nats.Msg) {
		// Generate request_id at NATS entry point
		ctx, logger := logging.WithRequestID(context.Background(), r.logger)

		logger.Debug("NATS queue message with reply received", "subject", msg.Subject, "queue", queue, "has_reply", msg.Reply != "")

		// Create reply function if message has reply subject
		var replyFunc func([]byte) error
		if msg.Reply != "" {
			replyFunc = func(data []byte) error {
				logger.Debug("Sending reply to NATS message", "reply_subject", msg.Reply, "reply_size", len(data))

				err := msg.Respond(data)
				if err != nil {
					logger.Error("Failed to send reply", "reply_subject", msg.Reply, "error", err.Error())
				}
				return err
			}
		}

		// Handle the message with reply support
		if err := handler.HandleWithReply(ctx, msg.Data, msg.Subject, replyFunc); err != nil {
			logger.Error("Queue message with reply handler failed", "queue", queue, "error", err.Error())
		}
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		r.logger.Error("Failed to queue subscribe with reply to NATS", "subject", subject, "queue", queue, "error", err.Error())
		return fmt.Errorf("failed to queue subscribe with reply to subject %s with queue %s: %w", subject, queue, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS queue subscription with reply created successfully", "subject", subject, "queue", queue)
	return nil
}

// Publish publishes a message to NATS
func (r *MessagingRepository) Publish(ctx context.Context, subject string, data []byte) error {
	logger := logging.FromContext(ctx, r.logger)

	logger.Debug("Publishing message to NATS", "subject", subject, "size", len(data))

	// Check connection state before publishing
	if !r.IsConnected() {
		r.logger.Error("Cannot publish: NATS connection not available", "subject", subject)
		return fmt.Errorf("NATS connection not available for publishing to subject %s", subject)
	}

	if err := r.conn.Publish(subject, data); err != nil {
		logger.Error("Failed to publish message to NATS", "subject", subject, "error", err.Error())
		return fmt.Errorf("failed to publish message to subject %s: %w", subject, err)
	}

	logger.Debug("Message published to NATS successfully", "subject", subject, "size", len(data))
	return nil
}

// DrainWithTimeout performs graceful NATS connection drain with timeout
func (r *MessagingRepository) DrainWithTimeout() error {
	r.mu.Lock()
	r.isShuttingDown = true
	totalSubscriptions := len(r.subscriptions)
	r.mu.Unlock()

	r.logger.Info("Starting NATS graceful drain sequence", "timeout", r.drainTimeout, "subscriptions", totalSubscriptions)

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

	// Start drain process
	r.logger.Debug("Initiating NATS connection drain")

	if err := r.conn.Drain(); err != nil {
		r.logger.Error("Failed to start NATS drain", "error", err.Error())
		return fmt.Errorf("failed to drain NATS connection: %w", err)
	}

	// Wait for drain to complete
	select {
	case <-time.After(r.drainTimeout):
		r.logger.Warn("NATS drain timeout reached", "timeout", r.drainTimeout)
	case <-func() <-chan struct{} {
		done := make(chan struct{})
		go func() {
			for !r.conn.IsClosed() {
				time.Sleep(100 * time.Millisecond)
			}
			close(done)
		}()
		return done
	}():
		// Drain completed successfully
	}

	r.logger.Info("NATS drain completed successfully", "subscriptions_processed", totalSubscriptions)
	return nil
}

// Close closes the NATS connection and all subscriptions
func (r *MessagingRepository) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	logger := r.logger
	totalSubscriptions := len(r.subscriptions)

	logger.Info("Initiating NATS repository close sequence", "total_subscriptions", totalSubscriptions)

	// First, attempt graceful drain if not already shutting down
	if !r.isShuttingDown {
		logger.Info("Close called without drain, attempting graceful drain first")
		r.mu.Unlock() // Unlock to call DrainWithTimeout
		if err := r.DrainWithTimeout(); err != nil {
			logger.Warn("Graceful drain failed, proceeding with immediate close", "error", err.Error())
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
			logger.Error("Failed to unsubscribe from subscription", "index", i, "subject", sub.Subject, "error", err.Error())
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

	logger.Info("NATS repository close sequence completed", "subscription_errors", subscriptionErrors)
	return nil
}

// HealthCheck checks the health of the NATS connection
func (r *MessagingRepository) HealthCheck(ctx context.Context) error {
	r.logger.Debug("NATS health check initiated")

	// Check basic connection state
	if r.conn == nil {
		r.logger.Error("NATS health check failed: connection is nil")
		return fmt.Errorf("%s: NATS connection is nil", constants.ErrHealthCheck)
	}

	r.logger.Debug("NATS connection diagnostics", "status", r.conn.Status().String(), "connected", r.conn.IsConnected())

	if !r.conn.IsConnected() {
		r.logger.Error("NATS health check failed: connection not connected", "status", r.conn.Status().String())
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

	r.logger.Info("NATS health check completed successfully", "status", "healthy", "subscriptions", totalSubs)

	return nil
}

// =================
// AUTHENTICATION OPERATIONS (delegated to AuthRepository)
// =================

// ValidateToken validates a JWT token and returns principal information
func (r *MessagingRepository) ValidateToken(ctx context.Context, token string) (*contracts.Principal, error) {
	r.logger.Debug("NATS delegating token validation to auth repository")

	if r.authRepo == nil {
		r.logger.Error("Token validation failed: auth repository not configured")
		return nil, fmt.Errorf(constants.ErrAuthRepoNotConfigured)
	}

	principal, err := r.authRepo.ValidateToken(ctx, token)
	if err != nil {
		r.logger.Error("Token validation delegation failed", "error", err.Error())
		return nil, err
	}

	r.logger.Debug("Token validation delegation completed successfully", "principal", principal.Principal)
	return principal, nil
}

// ParsePrincipals parses principals from HTTP headers with delegation support
func (r *MessagingRepository) ParsePrincipals(ctx context.Context, headers map[string]string) ([]contracts.Principal, error) {
	r.logger.Debug("NATS delegating principal parsing to auth repository", "headers_count", len(headers))

	if r.authRepo == nil {
		r.logger.Error("Principal parsing failed: auth repository not configured")
		return nil, fmt.Errorf(constants.ErrAuthRepoNotConfigured)
	}

	principals, err := r.authRepo.ParsePrincipals(ctx, headers)
	if err != nil {
		r.logger.Error("Principal parsing delegation failed", "error", err.Error())
		return nil, err
	}

	r.logger.Debug("Principal parsing delegation completed successfully", "principals_count", len(principals))
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

// GetMetrics returns messaging repository metrics for monitoring
func (r *MessagingRepository) GetMetrics() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

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

	connectionStatus := "unknown"
	connected := false
	if r.conn != nil {
		connectionStatus = r.conn.Status().String()
		connected = r.conn.IsConnected()
	}

	return map[string]interface{}{
		"connection_status":    connectionStatus,
		"connected":            connected,
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
	status := map[string]interface{}{
		"component":          "messaging_repository",
		"status":             "initialized",
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
