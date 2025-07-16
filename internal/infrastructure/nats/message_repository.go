package nats

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/nats-io/nats.go"
)

// MessageRepository implements the domain MessageRepository interface
type MessageRepository struct {
	conn          *nats.Conn
	subscriptions []*nats.Subscription
	mu            sync.RWMutex
}

// NewMessageRepository creates a new NATS message repository
func NewMessageRepository(conn *nats.Conn) *MessageRepository {
	return &MessageRepository{
		conn:          conn,
		subscriptions: make([]*nats.Subscription, 0),
	}
}

// Subscribe subscribes to NATS messages
func (r *MessageRepository) Subscribe(ctx context.Context, subject string, handler repositories.MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Create the NATS message handler
	natsHandler := func(msg *nats.Msg) {
		if err := handler.Handle(ctx, msg.Data, msg.Subject); err != nil {
			log.Printf("Error handling message from subject %s: %v", msg.Subject, err)
		}
	}

	// Subscribe to the subject
	sub, err := r.conn.Subscribe(subject, natsHandler)
	if err != nil {
		return fmt.Errorf("failed to subscribe to subject %s: %w", subject, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	log.Printf("Subscribed to NATS subject: %s", subject)
	return nil
}

// QueueSubscribe subscribes to NATS messages with queue group for load balancing
func (r *MessageRepository) QueueSubscribe(ctx context.Context, subject string, queue string, handler repositories.MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Create the NATS message handler
	natsHandler := func(msg *nats.Msg) {
		if err := handler.Handle(ctx, msg.Data, msg.Subject); err != nil {
			log.Printf("Error handling message from subject %s (queue %s): %v", msg.Subject, queue, err)
		}
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		return fmt.Errorf("failed to queue subscribe to subject %s with queue %s: %w", subject, queue, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	log.Printf("Queue subscribed to NATS subject: %s (queue: %s)", subject, queue)
	return nil
}

// QueueSubscribeWithReply subscribes to NATS messages with queue group and reply support
func (r *MessageRepository) QueueSubscribeWithReply(ctx context.Context, subject string, queue string, handler repositories.MessageHandlerWithReply) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Create the NATS message handler with reply support
	natsHandler := func(msg *nats.Msg) {
		// Create reply function if message has reply subject
		var replyFunc func([]byte) error
		if msg.Reply != "" {
			replyFunc = func(data []byte) error {
				return msg.Respond(data)
			}
		}

		// Handle the message with reply support
		if err := handler.HandleWithReply(ctx, msg.Data, msg.Subject, replyFunc); err != nil {
			log.Printf("Error handling message with reply from subject %s (queue %s): %v", msg.Subject, queue, err)
		}
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		return fmt.Errorf("failed to queue subscribe with reply to subject %s with queue %s: %w", subject, queue, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	log.Printf("Queue subscribed with reply to NATS subject: %s (queue: %s)", subject, queue)
	return nil
}

// Publish publishes a message to NATS
func (r *MessageRepository) Publish(ctx context.Context, subject string, data []byte) error {
	if err := r.conn.Publish(subject, data); err != nil {
		return fmt.Errorf("failed to publish message to subject %s: %w", subject, err)
	}

	log.Printf("Published message to NATS subject: %s", subject)
	return nil
}

// Close closes the NATS connection and all subscriptions
func (r *MessageRepository) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Unsubscribe from all subscriptions
	for _, sub := range r.subscriptions {
		if err := sub.Unsubscribe(); err != nil {
			log.Printf("Error unsubscribing from subscription: %v", err)
		}
	}
	r.subscriptions = nil

	// Close the connection
	if r.conn != nil {
		r.conn.Close()
		log.Printf("NATS connection closed")
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
