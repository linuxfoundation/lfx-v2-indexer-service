package repositories

import (
	"context"
	"io"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
)

// TransactionRepository defines the interface for transaction data access
type TransactionRepository interface {
	// Index indexes a transaction body into OpenSearch
	Index(ctx context.Context, index string, docID string, body io.Reader) error

	// Search searches for documents in OpenSearch
	Search(ctx context.Context, index string, query map[string]any) ([]map[string]any, error)

	// Update updates a document in OpenSearch
	Update(ctx context.Context, index string, docID string, body io.Reader) error

	// Delete deletes a document from OpenSearch
	Delete(ctx context.Context, index string, docID string) error

	// BulkIndex performs bulk indexing operations
	BulkIndex(ctx context.Context, operations []BulkOperation) error

	// HealthCheck checks the health of the OpenSearch connection
	HealthCheck(ctx context.Context) error
}

// BulkOperation represents a bulk operation for indexing
type BulkOperation struct {
	Index  string
	DocID  string
	Action string // "index", "update", "delete"
	Body   io.Reader
}

// MessageRepository defines the interface for message operations
type MessageRepository interface {
	// Subscribe subscribes to NATS messages
	Subscribe(ctx context.Context, subject string, handler MessageHandler) error

	// QueueSubscribe subscribes to NATS messages with queue group for load balancing
	QueueSubscribe(ctx context.Context, subject string, queue string, handler MessageHandler) error

	// Publish publishes a message to NATS
	Publish(ctx context.Context, subject string, data []byte) error

	// Close closes the NATS connection
	Close() error

	// HealthCheck checks the health of the NATS connection
	HealthCheck(ctx context.Context) error
}

// MessageHandler defines the interface for handling messages
type MessageHandler interface {
	Handle(ctx context.Context, data []byte, subject string) error
}

// AuthRepository defines the interface for authentication operations
type AuthRepository interface {
	// ValidateToken validates a JWT token
	ValidateToken(ctx context.Context, token string) (*entities.Principal, error)

	// ParsePrincipals parses principals from headers
	ParsePrincipals(ctx context.Context, headers map[string]string) ([]entities.Principal, error)

	// HealthCheck checks the health of the auth service
	HealthCheck(ctx context.Context) error
}

// ConfigRepository defines the interface for configuration operations
type ConfigRepository interface {
	// GetConfig returns the current configuration
	GetConfig() interface{}

	// ValidateConfig validates the configuration
	ValidateConfig() error

	// ReloadConfig reloads the configuration
	ReloadConfig() error
}
