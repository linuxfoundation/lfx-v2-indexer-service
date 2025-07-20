// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

// Error messages (centralized from scattered string literals)
const (
	ErrInvalidAction     = "invalid transaction action"
	ErrInvalidObjectType = "unsupported object type"
	ErrInvalidSubject    = "invalid NATS subject format"
	ErrParseTransaction  = "failed to parse transaction data"
	ErrEnrichTransaction = "failed to enrich transaction"
	ErrIndexDocument     = "failed to index document"
	ErrHealthCheck       = "health check failed"
	ErrVersionConflict   = "version conflict detected"
	ErrJanitorFailed     = "janitor processing failed"
	ErrShutdownTimeout   = "shutdown timeout exceeded"
	ErrProcessing        = "processing failed"

	// Transaction processing errors
	ErrCreateTransaction   = "failed to create transaction"
	ErrCreateV1Transaction = "failed to create V1 transaction"
	ErrCreateV2Transaction = "failed to create V2 transaction"
	ErrProcessTransaction  = "failed to process transaction"

	// Data enrichment errors
	ErrMappingUID        = "error mapping `uid` to `object_id`"
	ErrMappingPublic     = "error mapping `public` attribute"
	ErrEnrichmentFailed  = "transaction enrichment failed"
	ErrMissingObjectID   = "missing or invalid object ID"
	ErrMissingPublicFlag = "missing or invalid public flag"

	// Storage specific errors
	ErrMarshalQuery       = "failed to marshal query"
	ErrSearchFailed       = "failed to search"
	ErrDecodeResponse     = "failed to decode search response"
	ErrUpdateDocument     = "failed to update document"
	ErrDeleteDocument     = "failed to delete document"
	ErrMarshalBulkAction  = "failed to marshal bulk action"
	ErrReadBulkBody       = "failed to read bulk operation body"
	ErrBulkOperation      = "failed to perform bulk operation"
	ErrDecodeBulkResponse = "failed to decode bulk response"
	ErrOptimisticUpdate   = "failed to perform optimistic update"

	// Log messages
	LogFailedIndexDocument       = "Failed to index document"
	LogFailedUpdateDocument      = "Failed to update document"
	LogFailedDeleteDocument      = "Failed to delete document"
	LogFailedBulkOperation       = "Failed to perform bulk operation"
	LogFailedOptimisticUpdate    = "Failed to perform optimistic update"
	LogFailedCreateTransaction   = "Failed to create transaction from message"
	LogFailedCreateV1Transaction = "Failed to create V1 transaction from message"
)

// Error contexts (for structured error logging)
const (
	ContextValidation = "validation"
	ContextProcessing = "processing"
	ContextStorage    = "storage"
	ContextMessaging  = "messaging"
	ContextAuth       = "authentication"
	ContextJanitor    = "janitor"
)
