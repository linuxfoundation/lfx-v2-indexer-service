// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// StorageRepository implements the domain StorageRepository interface
type StorageRepository struct {
	client *opensearch.Client
	logger *slog.Logger
}

// NewStorageRepository creates a new OpenSearch storage repository
func NewStorageRepository(client *opensearch.Client, logger *slog.Logger) *StorageRepository {
	return &StorageRepository{
		client: client,
		logger: logging.WithComponent(logger, constants.ComponentOpenSearch),
	}
}

// Index indexes a transaction body into OpenSearch
func (r *StorageRepository) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	logger := logging.FromContext(ctx, r.logger)
	logger.Debug("Indexing document", "document_id", docID, "index", index)

	req := opensearchapi.IndexRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    constants.RefreshTrue,
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logger.Error("Failed to index document", "error", err.Error())
		return fmt.Errorf("%s: %w", constants.ErrIndexDocument, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logger.Error("Index request failed", "status", res.Status())
		return fmt.Errorf("%s: %s", constants.ErrIndexDocument, res.Status())
	}

	logger.Debug("Document indexed successfully", "status", res.Status())
	return nil
}

// Search searches for documents in OpenSearch
func (r *StorageRepository) Search(ctx context.Context, index string, query map[string]any) ([]map[string]any, error) {
	logger := logging.FromContext(ctx, r.logger)
	logger.Debug("Searching documents", "index", index)

	queryBytes, err := json.Marshal(query)
	if err != nil {
		logger.Error("Failed to marshal query", "error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrMarshalQuery, err)
	}

	req := opensearchapi.SearchRequest{
		Index: []string{index},
		Body:  bytes.NewReader(queryBytes),
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logger.Error("Search request failed", "error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrSearchFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logger.Error("Search response error", "status", res.Status())
		return nil, fmt.Errorf("%s: %s", constants.ErrSearchFailed, res.Status())
	}

	var response struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		logger.Error("Failed to decode response", "error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrDecodeResponse, err)
	}

	var results []map[string]any
	for _, hit := range response.Hits.Hits {
		results = append(results, hit.Source)
	}

	logger.Debug("Search completed successfully", "result_count", len(results), "total_hits", response.Hits.Total.Value)
	return results, nil
}

// SearchWithVersions searches for documents and includes version tracking information
func (r *StorageRepository) SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]contracts.VersionedDocument, error) {
	logger := logging.FromContext(ctx, r.logger)
	logger.Debug("Searching documents with versions", "index", index)

	queryBytes, err := json.Marshal(query)
	if err != nil {
		logger.Error("Failed to marshal query", "error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrMarshalQuery, err)
	}

	req := opensearchapi.SearchRequest{
		Index: []string{index},
		Body:  bytes.NewReader(queryBytes),
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logger.Error("Search request failed", "error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrSearchFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logger.Error("Search response error", "status", res.Status())
		return nil, fmt.Errorf("%s: %s", constants.ErrSearchFailed, res.Status())
	}

	var response struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID          string                 `json:"_id"`
				SeqNo       *int64                 `json:"_seq_no"`
				PrimaryTerm *int64                 `json:"_primary_term"`
				Source      map[string]interface{} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		logger.Error("Failed to decode response", "error", err.Error())
		return nil, fmt.Errorf("%s: %w", constants.ErrDecodeResponse, err)
	}

	var results []contracts.VersionedDocument
	for _, hit := range response.Hits.Hits {
		results = append(results, contracts.VersionedDocument{
			ID:          hit.ID,
			SeqNo:       hit.SeqNo,
			PrimaryTerm: hit.PrimaryTerm,
			Source:      hit.Source,
		})
	}

	logger.Debug("Search with versions completed successfully", "result_count", len(results), "total_hits", response.Hits.Total.Value)
	return results, nil
}

// Update updates a document in OpenSearch
func (r *StorageRepository) Update(ctx context.Context, index string, docID string, body io.Reader) error {
	logger := logging.FromContext(ctx, r.logger)
	logger.Debug("Updating document", "document_id", docID, "index", index)

	req := opensearchapi.UpdateRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    constants.RefreshTrue,
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logger.Error("Failed to update document", "error", err.Error())
		return fmt.Errorf("%s: %w", constants.ErrUpdateDocument, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logger.Error("Update request failed", "status", res.Status())
		return fmt.Errorf("%s: %s", constants.ErrUpdateDocument, res.Status())
	}

	logger.Debug("Document updated successfully", "status", res.Status())
	return nil
}

// Delete deletes a document from OpenSearch
func (r *StorageRepository) Delete(ctx context.Context, index string, docID string) error {
	logger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"document_id": docID,
			"index":       index,
		},
	)

	logger.Debug("Deleting document from OpenSearch")

	req := opensearchapi.DeleteRequest{
		Index:      index,
		DocumentID: docID,
		Refresh:    constants.RefreshTrue,
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logger.Error("Failed to execute delete request", "error", err.Error())
		return fmt.Errorf("%s: %w", constants.ErrDeleteDocument, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		// 404 is not an error for delete operations
		if strings.Contains(res.Status(), "404") {
			logger.Debug("Document not found for deletion", "status", res.Status())
			return nil
		}

		logger.Error("Delete request failed", "status", res.Status())
		return fmt.Errorf("%s: %s", constants.ErrDeleteDocument, res.Status())
	}

	logger.Debug("Document deleted successfully", "status", res.Status())
	return nil
}

// BulkIndex performs bulk indexing operations
func (r *StorageRepository) BulkIndex(ctx context.Context, operations []contracts.BulkOperation) error {
	if len(operations) == 0 {
		return nil
	}

	logger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"operation_count": len(operations),
		},
	)

	logger.Debug("Starting bulk index operation")

	var buf bytes.Buffer
	for _, op := range operations {
		// Create the action metadata
		action := map[string]any{
			op.Action: map[string]any{
				"_index": op.Index,
				"_id":    op.DocID,
			},
		}

		actionBytes, err := json.Marshal(action)
		if err != nil {
			logger.Error("Failed to marshal bulk action", "error", err.Error())
			return fmt.Errorf("%s: %w", constants.ErrMarshalBulkAction, err)
		}

		buf.Write(actionBytes)
		buf.WriteByte('\n')

		// Add the document body if it's not a delete operation
		if op.Action != "delete" && op.Body != nil {
			if _, err := buf.ReadFrom(op.Body); err != nil {
				logger.Error("Failed to read bulk body", "error", err.Error())
				return fmt.Errorf("%s: %w", constants.ErrReadBulkBody, err)
			}
			buf.WriteByte('\n')
		}
	}

	req := opensearchapi.BulkRequest{
		Body:    &buf,
		Refresh: constants.RefreshTrue,
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logger.Error("Failed to execute bulk request", "error", err.Error(), "body_size", buf.Len())
		return fmt.Errorf("%s: %w", constants.ErrBulkOperation, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logger.Error("Bulk request failed", "status", res.Status(), "body_size", buf.Len())
		return fmt.Errorf("%s: %s", constants.ErrBulkOperation, res.Status())
	}

	// Parse the response to check for individual operation errors
	var bulkResponse struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int    `json:"status"`
			Error  string `json:"error,omitempty"`
		} `json:"items"`
	}

	if err := json.NewDecoder(res.Body).Decode(&bulkResponse); err != nil {
		logger.Error("Failed to decode bulk response", "error", err.Error())
		return fmt.Errorf("%s: %w", constants.ErrDecodeBulkResponse, err)
	}

	if bulkResponse.Errors {
		var successCount, errorCount int
		for _, item := range bulkResponse.Items {
			for _, op := range item {
				if op.Status >= 400 {
					errorCount++
					logger.Warn("Bulk operation item failed",
						"status", op.Status,
						"error", op.Error)
				} else {
					successCount++
				}
			}
		}

		logger.Error("Bulk operation completed with errors", "error_count", errorCount, "success_count", successCount)
		return fmt.Errorf("bulk operation completed with %d errors", errorCount)
	}

	logger.Debug("Bulk index operation completed successfully",
		"operations_processed", len(operations),
		"body_size", buf.Len(),
		"status", res.Status())
	return nil
}

// UpdateWithOptimisticLock updates a document with optimistic concurrency control
func (r *StorageRepository) UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *contracts.OptimisticUpdateParams) error {
	logger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"document_id": docID,
			"index":       index,
		},
	)

	if params != nil {
		logger = logging.WithFields(logger, map[string]any{
			"seq_no":       params.SeqNo,
			"primary_term": params.PrimaryTerm,
		})
	}

	logger.Debug("Updating document with optimistic lock")

	req := opensearchapi.UpdateRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    constants.RefreshTrue,
	}

	// Add optimistic concurrency control parameters if provided
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
		logger.Error("Failed to execute update with lock request", "error", err.Error())
		return fmt.Errorf("%s: %w", constants.ErrOptimisticUpdate, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		// Check for version conflict (409)
		if strings.Contains(res.Status(), "409") {
			versionErr := &contracts.VersionConflictError{
				DocumentID:  docID,
				CurrentSeq:  0, // Would need to be extracted from response
				ExpectedSeq: 0, // Would need to be extracted from params
				Err:         fmt.Errorf("version conflict: %s", res.Status()),
			}
			logger.Warn("Version conflict during update", "status", res.Status())
			return versionErr
		}

		logger.Error("Update with lock request failed", "status", res.Status())
		return fmt.Errorf("%s: %s", constants.ErrOptimisticUpdate, res.Status())
	}

	logger.Debug("Update with optimistic lock completed successfully", "status", res.Status())
	return nil
}

// HealthCheck checks the health of the OpenSearch connection
func (r *StorageRepository) HealthCheck(ctx context.Context) error {
	logger := logging.FromContext(ctx, r.logger)
	logger.Debug("Checking OpenSearch health")

	req := opensearchapi.InfoRequest{}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		logger.Error("Failed to execute health check request", "error", err.Error())
		return fmt.Errorf("%s: %w", constants.ErrHealthCheck, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		logger.Error("Health check request failed", "status", res.Status())
		return fmt.Errorf("%s: %s", constants.ErrHealthCheck, res.Status())
	}

	logger.Debug("Health check completed successfully", "status", res.Status())
	return nil
}
