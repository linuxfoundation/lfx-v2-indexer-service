package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/logging"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// TransactionRepository implements the domain TransactionRepository interface
type TransactionRepository struct {
	client *opensearch.Client
	logger *slog.Logger
}

// NewTransactionRepository creates a new OpenSearch transaction repository
func NewTransactionRepository(client *opensearch.Client, logger *slog.Logger) *TransactionRepository {
	return &TransactionRepository{
		client: client,
		logger: logging.WithComponent(logger, "opensearch_repo"),
	}
}

// Index indexes a transaction body into OpenSearch
func (r *TransactionRepository) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	logger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"operation":   "index",
			"document_id": docID,
			"index":       index,
		},
	)

	logger.Debug("Indexing document started")

	req := opensearchapi.IndexRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    "true",
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logging.LogError(logger, "Failed to index document", err)
		return fmt.Errorf("failed to index document: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logging.LogError(logger, "Indexing failed", fmt.Errorf("status: %s", res.Status()))
		return fmt.Errorf("indexing failed: %s", res.Status())
	}

	logger.Info("Document indexed successfully")
	return nil
}

// Search searches for documents in OpenSearch
func (r *TransactionRepository) Search(ctx context.Context, index string, query map[string]any) ([]map[string]any, error) {
	queryBytes, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	req := opensearchapi.SearchRequest{
		Index: []string{index},
		Body:  bytes.NewReader(queryBytes),
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search failed: %s", res.Status())
	}

	var response struct {
		Hits struct {
			Hits []struct {
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	var results []map[string]any
	for _, hit := range response.Hits.Hits {
		results = append(results, hit.Source)
	}

	return results, nil
}

// SearchWithVersions searches for documents and includes version tracking information
func (r *TransactionRepository) SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]repositories.VersionedDocument, error) {
	queryBytes, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	req := opensearchapi.SearchRequest{
		Index: []string{index},
		Body:  bytes.NewReader(queryBytes),
		// Note: seq_no and primary_term are included by default in OpenSearch responses
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return nil, fmt.Errorf("failed to search with versions: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search with versions failed: %s", res.Status())
	}

	var response struct {
		Hits struct {
			Hits []struct {
				ID          string                 `json:"_id"`
				SeqNo       *int64                 `json:"_seq_no"`
				PrimaryTerm *int64                 `json:"_primary_term"`
				Source      map[string]interface{} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode search response with versions: %w", err)
	}

	var results []repositories.VersionedDocument
	for _, hit := range response.Hits.Hits {
		results = append(results, repositories.VersionedDocument{
			ID:          hit.ID,
			SeqNo:       hit.SeqNo,
			PrimaryTerm: hit.PrimaryTerm,
			Source:      hit.Source,
		})
	}

	return results, nil
}

// Update updates a document in OpenSearch
func (r *TransactionRepository) Update(ctx context.Context, index string, docID string, body io.Reader) error {
	logger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"operation":   "update",
			"document_id": docID,
			"index":       index,
		},
	)

	logger.Debug("Updating document started")

	req := opensearchapi.UpdateRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    "true",
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logging.LogError(logger, "Failed to update document", err)
		return fmt.Errorf("failed to update document: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logging.LogError(logger, "Update failed", fmt.Errorf("status: %s", res.Status()))
		return fmt.Errorf("update failed: %s", res.Status())
	}

	logger.Info("Document updated successfully")
	return nil
}

// UpdateWithOptimisticLock updates a document with optimistic concurrency control
func (r *TransactionRepository) UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *repositories.OptimisticUpdateParams) error {
	logger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"operation":   "update_optimistic",
			"document_id": docID,
			"index":       index,
		},
	)

	logger.Debug("Updating document with optimistic lock started")

	req := opensearchapi.UpdateRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    "true",
	}

	// Add optimistic concurrency parameters if provided
	if params != nil {
		if params.SeqNo != nil {
			seqNo := int(*params.SeqNo)
			req.IfSeqNo = &seqNo
		}
		if params.PrimaryTerm != nil {
			primaryTerm := int(*params.PrimaryTerm)
			req.IfPrimaryTerm = &primaryTerm
		}
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		// Check for version conflict in the error
		if strings.Contains(err.Error(), "version_conflict_engine_exception") {
			logger.Warn("Version conflict detected", "error", err.Error())
			return &repositories.VersionConflictError{
				DocumentID: docID,
				Err:        err,
			}
		}
		logging.LogError(logger, "Failed to update document with optimistic lock", err)
		return fmt.Errorf("failed to update document with optimistic lock: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		// Check for version conflict status code
		if res.StatusCode == 409 {
			logger.Warn("Version conflict detected", "status_code", res.StatusCode)
			return &repositories.VersionConflictError{
				DocumentID: docID,
				Err:        fmt.Errorf("version conflict: %s", res.Status()),
			}
		}
		logging.LogError(logger, "Update with optimistic lock failed", fmt.Errorf("status: %s", res.Status()))
		return fmt.Errorf("update with optimistic lock failed: %s", res.Status())
	}

	logger.Info("Document updated successfully with optimistic lock")
	return nil
}

// Delete deletes a document from OpenSearch
func (r *TransactionRepository) Delete(ctx context.Context, index string, docID string) error {
	logger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"operation":   "delete",
			"document_id": docID,
			"index":       index,
		},
	)

	logger.Debug("Deleting document started")

	req := opensearchapi.DeleteRequest{
		Index:      index,
		DocumentID: docID,
		Refresh:    "true",
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logging.LogError(logger, "Failed to delete document", err)
		return fmt.Errorf("failed to delete document: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logging.LogError(logger, "Delete failed", fmt.Errorf("status: %s", res.Status()))
		return fmt.Errorf("delete failed: %s", res.Status())
	}

	logger.Info("Document deleted successfully")
	return nil
}

// BulkIndex performs bulk indexing operations
func (r *TransactionRepository) BulkIndex(ctx context.Context, operations []repositories.BulkOperation) error {
	if len(operations) == 0 {
		return nil
	}

	logger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"operation": "bulk_index",
			"op_count":  len(operations),
		},
	)

	logger.Debug("Bulk indexing operations started")

	var bulkBody strings.Builder
	for _, op := range operations {
		// Write the action header
		switch op.Action {
		case "index":
			bulkBody.WriteString(fmt.Sprintf(`{"index":{"_index":"%s","_id":"%s"}}`, op.Index, op.DocID))
		case "update":
			bulkBody.WriteString(fmt.Sprintf(`{"update":{"_index":"%s","_id":"%s"}}`, op.Index, op.DocID))
		case "delete":
			bulkBody.WriteString(fmt.Sprintf(`{"delete":{"_index":"%s","_id":"%s"}}`, op.Index, op.DocID))
		default:
			return fmt.Errorf("unsupported bulk operation: %s", op.Action)
		}
		bulkBody.WriteString("\n")

		// Write the document body for index and update operations
		if op.Action != "delete" && op.Body != nil {
			bodyBytes, err := json.Marshal(op.Body)
			if err != nil {
				return fmt.Errorf("failed to marshal bulk operation body: %w", err)
			}
			bulkBody.Write(bodyBytes)
			bulkBody.WriteString("\n")
		}
	}

	req := opensearchapi.BulkRequest{
		Body:    strings.NewReader(bulkBody.String()),
		Refresh: "true",
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logging.LogError(logger, "Failed to execute bulk operations", err)
		return fmt.Errorf("failed to execute bulk operations: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logging.LogError(logger, "Bulk operations failed", fmt.Errorf("status: %s", res.Status()))
		return fmt.Errorf("bulk operations failed: %s", res.Status())
	}

	logger.Info("Bulk operations executed successfully")
	return nil
}

// HealthCheck checks the health of the OpenSearch connection
func (r *TransactionRepository) HealthCheck(ctx context.Context) error {
	req := opensearchapi.InfoRequest{}
	res, err := req.Do(ctx, r.client)
	if err != nil {
		return fmt.Errorf("failed to check OpenSearch health: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("OpenSearch health check failed: %s", res.Status())
	}

	return nil
}
