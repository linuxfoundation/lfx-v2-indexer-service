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
	"time"

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

// Helper functions for timing and metrics
func (r *StorageRepository) logOperationStart(logger *slog.Logger, operation string, metadata map[string]any) (*slog.Logger, time.Time) {
	start := time.Now()
	enhancedLogger := logging.WithFields(logger, map[string]any{
		"started_at": start,
		"operation":  operation,
	})
	if metadata != nil {
		enhancedLogger = logging.WithFields(enhancedLogger, metadata)
	}
	enhancedLogger.Info("Storage operation started")
	return enhancedLogger, start
}

func (r *StorageRepository) logOperationEnd(logger *slog.Logger, operation string, start time.Time, err error, metadata map[string]any) {
	duration := time.Since(start)
	fields := map[string]any{
		"duration_ms": duration.Milliseconds(),
		"operation":   operation,
	}
	for k, v := range metadata {
		fields[k] = v
	}

	if err != nil {
		fields["error"] = err.Error()
		fields["error_type"] = classifyError(err)
		logging.WithFields(logger, fields).Error("Storage operation failed")
	} else {
		logging.WithFields(logger, fields).Info("Storage operation completed successfully")
	}
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "connection") || strings.Contains(errStr, "timeout"):
		return "network"
	case strings.Contains(errStr, "409") || strings.Contains(errStr, "version_conflict"):
		return "version_conflict"
	case strings.Contains(errStr, "400"):
		return "bad_request"
	case strings.Contains(errStr, "401") || strings.Contains(errStr, "403"):
		return "authentication"
	case strings.Contains(errStr, "404"):
		return "not_found"
	case strings.Contains(errStr, "marshal") || strings.Contains(errStr, "unmarshal") || strings.Contains(errStr, "decode"):
		return "serialization"
	default:
		return "unknown"
	}
}

// Index indexes a transaction body into OpenSearch
func (r *StorageRepository) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	baseLogger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"document_id": docID,
			"index":       index,
		},
	)

	logger, start := r.logOperationStart(baseLogger, "index", nil)

	req := opensearchapi.IndexRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    constants.RefreshTrue,
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		r.logOperationEnd(logger, "index", start, err, map[string]any{
			"stage": "request_execution",
		})
		return fmt.Errorf("%s: %w", constants.ErrIndexDocument, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		apiErr := fmt.Errorf("status: %s", res.Status())
		r.logOperationEnd(logger, "index", start, apiErr, map[string]any{
			"stage":       "response_processing",
			"status_code": res.Status(),
		})
		return fmt.Errorf("%s: %s", constants.ErrIndexDocument, res.Status())
	}

	r.logOperationEnd(logger, "index", start, nil, map[string]any{
		"status_code": res.Status(),
	})
	return nil
}

// Search searches for documents in OpenSearch
func (r *StorageRepository) Search(ctx context.Context, index string, query map[string]any) ([]map[string]any, error) {
	baseLogger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"index": index,
		},
	)

	logger, start := r.logOperationStart(baseLogger, "search", nil)

	queryBytes, err := json.Marshal(query)
	if err != nil {
		r.logOperationEnd(logger, "search", start, err, map[string]any{
			"stage": "query_marshaling",
		})
		return nil, fmt.Errorf("%s: %w", constants.ErrMarshalQuery, err)
	}

	req := opensearchapi.SearchRequest{
		Index: []string{index},
		Body:  bytes.NewReader(queryBytes),
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		r.logOperationEnd(logger, "search", start, err, map[string]any{
			"stage": "request_execution",
		})
		return nil, fmt.Errorf("%s: %w", constants.ErrSearchFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		apiErr := fmt.Errorf("status: %s", res.Status())
		r.logOperationEnd(logger, "search", start, apiErr, map[string]any{
			"stage":       "response_processing",
			"status_code": res.Status(),
		})
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
		r.logOperationEnd(logger, "search", start, err, map[string]any{
			"stage": "response_decoding",
		})
		return nil, fmt.Errorf("%s: %w", constants.ErrDecodeResponse, err)
	}

	var results []map[string]any
	for _, hit := range response.Hits.Hits {
		results = append(results, hit.Source)
	}

	r.logOperationEnd(logger, "search", start, nil, map[string]any{
		"result_count": len(results),
		"total_hits":   response.Hits.Total.Value,
		"status_code":  res.Status(),
	})

	return results, nil
}

// SearchWithVersions searches for documents and includes version tracking information
func (r *StorageRepository) SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]contracts.VersionedDocument, error) {
	baseLogger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"index": index,
		},
	)

	logger, start := r.logOperationStart(baseLogger, "search_with_versions", nil)

	queryBytes, err := json.Marshal(query)
	if err != nil {
		r.logOperationEnd(logger, "search_with_versions", start, err, map[string]any{
			"stage": "query_marshaling",
		})
		return nil, fmt.Errorf("%s: %w", constants.ErrMarshalQuery, err)
	}

	req := opensearchapi.SearchRequest{
		Index: []string{index},
		Body:  bytes.NewReader(queryBytes),
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		r.logOperationEnd(logger, "search_with_versions", start, err, map[string]any{
			"stage": "request_execution",
		})
		return nil, fmt.Errorf("%s: %w", constants.ErrSearchFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		apiErr := fmt.Errorf("status: %s", res.Status())
		r.logOperationEnd(logger, "search_with_versions", start, apiErr, map[string]any{
			"stage":       "response_processing",
			"status_code": res.Status(),
		})
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
		r.logOperationEnd(logger, "search_with_versions", start, err, map[string]any{
			"stage": "response_decoding",
		})
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

	r.logOperationEnd(logger, "search_with_versions", start, nil, map[string]any{
		"result_count": len(results),
		"total_hits":   response.Hits.Total.Value,
		"status_code":  res.Status(),
	})

	return results, nil
}

// Update updates a document in OpenSearch
func (r *StorageRepository) Update(ctx context.Context, index string, docID string, body io.Reader) error {
	baseLogger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"document_id": docID,
			"index":       index,
		},
	)

	logger, start := r.logOperationStart(baseLogger, "update", nil)

	req := opensearchapi.UpdateRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    constants.RefreshTrue,
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		r.logOperationEnd(logger, "update", start, err, map[string]any{
			"stage": "request_execution",
		})
		return fmt.Errorf("%s: %w", constants.ErrUpdateDocument, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		apiErr := fmt.Errorf("status: %s", res.Status())
		r.logOperationEnd(logger, "update", start, apiErr, map[string]any{
			"stage":       "response_processing",
			"status_code": res.Status(),
		})
		return fmt.Errorf("%s: %s", constants.ErrUpdateDocument, res.Status())
	}

	r.logOperationEnd(logger, "update", start, nil, map[string]any{
		"status_code": res.Status(),
	})
	return nil
}

// Delete deletes a document from OpenSearch
func (r *StorageRepository) Delete(ctx context.Context, index string, docID string) error {
	baseLogger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"document_id": docID,
			"index":       index,
		},
	)

	logger, start := r.logOperationStart(baseLogger, "delete", nil)

	req := opensearchapi.DeleteRequest{
		Index:      index,
		DocumentID: docID,
		Refresh:    constants.RefreshTrue,
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		r.logOperationEnd(logger, "delete", start, err, map[string]any{
			"stage": "request_execution",
		})
		return fmt.Errorf("%s: %w", constants.ErrDeleteDocument, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		// 404 is not an error for delete operations
		if strings.Contains(res.Status(), "404") {
			r.logOperationEnd(logger, "delete", start, nil, map[string]any{
				"status_code": res.Status(),
				"result":      "document_not_found",
			})
			return nil
		}

		apiErr := fmt.Errorf("status: %s", res.Status())
		r.logOperationEnd(logger, "delete", start, apiErr, map[string]any{
			"stage":       "response_processing",
			"status_code": res.Status(),
		})
		return fmt.Errorf("%s: %s", constants.ErrDeleteDocument, res.Status())
	}

	r.logOperationEnd(logger, "delete", start, nil, map[string]any{
		"status_code": res.Status(),
		"result":      "document_deleted",
	})
	return nil
}

// BulkIndex performs bulk indexing operations
func (r *StorageRepository) BulkIndex(ctx context.Context, operations []contracts.BulkOperation) error {
	if len(operations) == 0 {
		return nil
	}

	baseLogger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"operation_count": len(operations),
		},
	)

	logger, start := r.logOperationStart(baseLogger, "bulk_index", nil)

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
			r.logOperationEnd(logger, "bulk_index", start, err, map[string]any{
				"stage": "action_marshaling",
			})
			return fmt.Errorf("%s: %w", constants.ErrMarshalBulkAction, err)
		}

		buf.Write(actionBytes)
		buf.WriteByte('\n')

		// Add the document body if it's not a delete operation
		if op.Action != "delete" && op.Body != nil {
			if _, err := buf.ReadFrom(op.Body); err != nil {
				r.logOperationEnd(logger, "bulk_index", start, err, map[string]any{
					"stage": "body_reading",
				})
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
		r.logOperationEnd(logger, "bulk_index", start, err, map[string]any{
			"stage":     "request_execution",
			"body_size": buf.Len(),
		})
		return fmt.Errorf("%s: %w", constants.ErrBulkOperation, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		apiErr := fmt.Errorf("status: %s", res.Status())
		r.logOperationEnd(logger, "bulk_index", start, apiErr, map[string]any{
			"stage":       "response_processing",
			"status_code": res.Status(),
			"body_size":   buf.Len(),
		})
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
		r.logOperationEnd(logger, "bulk_index", start, err, map[string]any{
			"stage": "response_decoding",
		})
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

		r.logOperationEnd(logger, "bulk_index", start, fmt.Errorf("bulk operation completed with %d errors", errorCount), map[string]any{
			"success_count": successCount,
			"error_count":   errorCount,
			"body_size":     buf.Len(),
		})
		return fmt.Errorf("bulk operation completed with %d errors", errorCount)
	}

	r.logOperationEnd(logger, "bulk_index", start, nil, map[string]any{
		"operations_processed": len(operations),
		"body_size":            buf.Len(),
		"status_code":          res.Status(),
	})
	return nil
}

// UpdateWithOptimisticLock updates a document with optimistic concurrency control
func (r *StorageRepository) UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *contracts.OptimisticUpdateParams) error {
	baseLogger := logging.WithFields(
		logging.FromContext(ctx, r.logger),
		map[string]any{
			"document_id": docID,
			"index":       index,
		},
	)

	var metadata map[string]any
	if params != nil {
		metadata = map[string]any{
			"seq_no":       params.SeqNo,
			"primary_term": params.PrimaryTerm,
		}
	}

	logger, start := r.logOperationStart(baseLogger, "update_with_lock", metadata)

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
		r.logOperationEnd(logger, "update_with_lock", start, err, map[string]any{
			"stage": "request_execution",
		})
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
			r.logOperationEnd(logger, "update_with_lock", start, versionErr, map[string]any{
				"stage":       "version_conflict",
				"status_code": res.Status(),
			})
			return versionErr
		}

		apiErr := fmt.Errorf("status: %s", res.Status())
		r.logOperationEnd(logger, "update_with_lock", start, apiErr, map[string]any{
			"stage":       "response_processing",
			"status_code": res.Status(),
		})
		return fmt.Errorf("%s: %s", constants.ErrOptimisticUpdate, res.Status())
	}

	r.logOperationEnd(logger, "update_with_lock", start, nil, map[string]any{
		"status_code": res.Status(),
	})
	return nil
}

// HealthCheck checks the health of the OpenSearch connection
func (r *StorageRepository) HealthCheck(ctx context.Context) error {
	baseLogger := logging.FromContext(ctx, r.logger)
	logger, start := r.logOperationStart(baseLogger, "health_check", nil)

	req := opensearchapi.InfoRequest{}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		r.logOperationEnd(logger, "health_check", start, err, map[string]any{
			"stage": "request_execution",
		})
		return fmt.Errorf("%s: %w", constants.ErrHealthCheck, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		apiErr := fmt.Errorf("status: %s", res.Status())
		r.logOperationEnd(logger, "health_check", start, apiErr, map[string]any{
			"stage":       "response_processing",
			"status_code": res.Status(),
		})
		return fmt.Errorf("%s: %s", constants.ErrHealthCheck, res.Status())
	}

	r.logOperationEnd(logger, "health_check", start, nil, map[string]any{
		"status_code": res.Status(),
	})
	return nil
}
