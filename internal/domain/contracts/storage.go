package contracts

import (
	"context"
	"fmt"
	"io"
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

// BulkOperation represents a bulk operation for indexing
type BulkOperation struct {
	Index  string
	DocID  string
	Action string // "index", "update", "delete"
	Body   io.Reader
}

// StorageRepository defines the interface for OpenSearch data access operations
type StorageRepository interface {
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
