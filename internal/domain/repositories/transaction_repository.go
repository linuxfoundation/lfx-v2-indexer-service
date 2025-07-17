package repositories

import (
	"context"
	"fmt"
	"io"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
)

// OptimisticUpdateParams contains parameters for optimistic concurrency control
type OptimisticUpdateParams struct {
	SeqNo       *int64
	PrimaryTerm *int64
}

// VersionedDocument represents a document with version tracking information
type VersionedDocument struct {
	ID          string                 `json:"_id"`
	SeqNo       *int64                 `json:"_seq_no"`
	PrimaryTerm *int64                 `json:"_primary_term"`
	Source      map[string]interface{} `json:"_source"`
}

// VersionConflictError represents a version conflict during update operations
type VersionConflictError struct {
	DocumentID  string
	CurrentSeq  int64
	ExpectedSeq int64
	Err         error
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("version conflict for document %s: current_seq=%d, expected_seq=%d: %v",
		e.DocumentID, e.CurrentSeq, e.ExpectedSeq, e.Err)
}

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

	// UpdateWithOptimisticLock updates a document with optimistic concurrency control
	UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *OptimisticUpdateParams) error

	// SearchWithVersions searches for documents and includes version tracking information
	SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]VersionedDocument, error)
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

	// QueueSubscribeWithReply subscribes to NATS messages with queue group and reply support
	QueueSubscribeWithReply(ctx context.Context, subject string, queue string, handler MessageHandlerWithReply) error

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

// MessageHandlerWithReply defines the interface for handling messages with reply support
type MessageHandlerWithReply interface {
	HandleWithReply(ctx context.Context, data []byte, subject string, reply func([]byte) error) error
}

// AuthRepository handles authentication and principal parsing
type AuthRepository interface {
	// ValidateToken validates a JWT token and returns principal information
	ValidateToken(ctx context.Context, token string) (*entities.Principal, error)

	// ParsePrincipals parses principals from HTTP headers with delegation support
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
