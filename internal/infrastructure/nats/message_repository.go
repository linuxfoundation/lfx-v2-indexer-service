package nats

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/logging"
	"github.com/nats-io/nats.go"
)

// MessageRepository implements the domain MessageRepository interface
type MessageRepository struct {
	conn           *nats.Conn
	logger         *slog.Logger
	subscriptions  []*nats.Subscription
	mu             sync.RWMutex
	drainTimeout   time.Duration
	isShuttingDown bool
}

// NewMessageRepository creates a new NATS message repository
func NewMessageRepository(conn *nats.Conn, logger *slog.Logger, drainTimeout time.Duration) *MessageRepository {
	return &MessageRepository{
		conn:           conn,
		logger:         logging.WithComponent(logger, "nats_repo"),
		subscriptions:  make([]*nats.Subscription, 0),
		drainTimeout:   drainTimeout,
		isShuttingDown: false,
	}
}

// Subscribe subscribes to NATS messages
func (r *MessageRepository) Subscribe(ctx context.Context, subject string, handler repositories.MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Create the NATS message handler
	natsHandler := func(msg *nats.Msg) {
		// Generate request_id at NATS entry point
		ctx, logger := logging.WithRequestID(context.Background(), r.logger)

		logger.Info("NATS message received",
			"subject", msg.Subject,
			"size", len(msg.Data))

		if err := handler.Handle(ctx, msg.Data, msg.Subject); err != nil {
			logging.LogError(logger, "Message handler failed", err)
		}
	}

	// Subscribe to the subject
	sub, err := r.conn.Subscribe(subject, natsHandler)
	if err != nil {
		r.logger.Error("Failed to subscribe to NATS",
			"subject", subject,
			"error", err.Error())
		return fmt.Errorf("failed to subscribe to subject %s: %w", subject, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS subscription created", "subject", subject)
	return nil
}

// QueueSubscribe subscribes to NATS messages with queue group for load balancing
func (r *MessageRepository) QueueSubscribe(ctx context.Context, subject string, queue string, handler repositories.MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Create the NATS message handler
	natsHandler := func(msg *nats.Msg) {
		// Generate request_id at NATS entry point
		ctx, logger := logging.WithRequestID(context.Background(), r.logger)

		logger.Info("NATS queue message received",
			"subject", msg.Subject,
			"queue", queue,
			"size", len(msg.Data))

		if err := handler.Handle(ctx, msg.Data, msg.Subject); err != nil {
			logging.LogError(logger, "Queue message handler failed", err, "queue", queue)
		}
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		r.logger.Error("Failed to queue subscribe to NATS",
			"subject", subject,
			"queue", queue,
			"error", err.Error())
		return fmt.Errorf("failed to queue subscribe to subject %s with queue %s: %w", subject, queue, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS queue subscription created", "subject", subject, "queue", queue)
	return nil
}

// QueueSubscribeWithReply subscribes to NATS messages with queue group and reply support
func (r *MessageRepository) QueueSubscribeWithReply(ctx context.Context, subject string, queue string, handler repositories.MessageHandlerWithReply) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Create the NATS message handler with reply support
	natsHandler := func(msg *nats.Msg) {
		// Generate request_id at NATS entry point
		ctx, logger := logging.WithRequestID(context.Background(), r.logger)

		logger.Info("NATS queue message with reply received",
			"subject", msg.Subject,
			"queue", queue,
			"size", len(msg.Data),
			"has_reply", msg.Reply != "")

		// Create reply function if message has reply subject
		var replyFunc func([]byte) error
		if msg.Reply != "" {
			replyFunc = func(data []byte) error {
				return msg.Respond(data)
			}
		}

		// Handle the message with reply support
		if err := handler.HandleWithReply(ctx, msg.Data, msg.Subject, replyFunc); err != nil {
			logging.LogError(logger, "Queue message with reply handler failed", err, "queue", queue)
		}
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		r.logger.Error("Failed to queue subscribe with reply to NATS",
			"subject", subject,
			"queue", queue,
			"error", err.Error())
		return fmt.Errorf("failed to queue subscribe with reply to subject %s with queue %s: %w", subject, queue, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS queue subscription with reply created", "subject", subject, "queue", queue)
	return nil
}

// Publish publishes a message to NATS
func (r *MessageRepository) Publish(ctx context.Context, subject string, data []byte) error {
	logger := logging.FromContext(ctx, r.logger)

	if err := r.conn.Publish(subject, data); err != nil {
		logging.LogError(logger, "Failed to publish message to NATS", err, "subject", subject)
		return fmt.Errorf("failed to publish message to subject %s: %w", subject, err)
	}

	logger.Info("Message published to NATS", "subject", subject, "size", len(data))
	return nil
}

// DrainWithTimeout performs graceful NATS connection drain with timeout
func (r *MessageRepository) DrainWithTimeout() error {
	r.mu.Lock()
	r.isShuttingDown = true
	r.mu.Unlock()

	r.logger.Info("Starting NATS graceful drain sequence",
		"timeout", r.drainTimeout,
		"subscription_count", len(r.subscriptions))

	if r.conn == nil {
		r.logger.Warn("NATS connection is nil, skipping drain")
		return nil
	}

	if r.conn.IsClosed() {
		r.logger.Info("NATS connection already closed")
		return nil
	}

	if r.conn.IsDraining() {
		r.logger.Info("NATS connection already draining")
		return nil
	}

	// Start drain process
	drainStart := time.Now()
	if err := r.conn.Drain(); err != nil {
		r.logger.Error("Failed to start NATS drain", "error", err.Error())
		return fmt.Errorf("failed to drain NATS connection: %w", err)
	}

	r.logger.Info("NATS drain completed successfully",
		"duration", time.Since(drainStart),
		"final_status", r.conn.Status())

	return nil
}

// Close closes the NATS connection and all subscriptions
func (r *MessageRepository) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	logger := r.logger

	// First, attempt graceful drain if not already shutting down
	if !r.isShuttingDown {
		logger.Info("Close called without drain, attempting graceful drain first")
		r.mu.Unlock() // Unlock to call DrainWithTimeout
		if err := r.DrainWithTimeout(); err != nil {
			logger.Warn("Graceful drain failed, proceeding with immediate close", "error", err.Error())
		}
		r.mu.Lock() // Re-lock for the rest of the method
	}

	// Unsubscribe from all subscriptions
	for _, sub := range r.subscriptions {
		if err := sub.Unsubscribe(); err != nil {
			logging.LogError(logger, "Failed to unsubscribe from subscription", err)
		}
	}
	r.subscriptions = nil

	// Close the connection if not already closed
	if r.conn != nil && !r.conn.IsClosed() {
		r.conn.Close()
		logger.Info("NATS connection closed")
	}

	return nil
}

// HealthCheck checks the health of the NATS connection
func (r *MessageRepository) HealthCheck(ctx context.Context) error {
	if r.conn == nil {
		return fmt.Errorf("NATS connection is nil")
	}

	if !r.conn.IsConnected() {
		return fmt.Errorf("NATS connection is not connected")
	}

	// Check if we can get server info
	if r.conn.Status() != nats.CONNECTED {
		return fmt.Errorf("NATS connection status is not connected: %s", r.conn.Status())
	}

	return nil
}

// GetConnection returns the underlying NATS connection
func (r *MessageRepository) GetConnection() *nats.Conn {
	return r.conn
}

// GetSubscriptionCount returns the number of active subscriptions
func (r *MessageRepository) GetSubscriptionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.subscriptions)
}

// IsConnected checks if the NATS connection is active
func (r *MessageRepository) IsConnected() bool {
	return r.conn != nil && r.conn.IsConnected()
}
