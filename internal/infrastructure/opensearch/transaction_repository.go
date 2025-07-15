package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// TransactionRepository implements the domain TransactionRepository interface
type TransactionRepository struct {
	client *opensearch.Client
}

// NewTransactionRepository creates a new OpenSearch transaction repository
func NewTransactionRepository(client *opensearch.Client) *TransactionRepository {
	return &TransactionRepository{
		client: client,
	}
}

// Index indexes a transaction body into OpenSearch
func (r *TransactionRepository) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	req := opensearchapi.IndexRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    "true",
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return fmt.Errorf("failed to index document: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("indexing failed: %s", res.Status())
	}

	log.Printf("Indexed document %s in index %s", docID, index)
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
		Body:  strings.NewReader(string(queryBytes)),
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

// Update updates a document in OpenSearch
func (r *TransactionRepository) Update(ctx context.Context, index string, docID string, body io.Reader) error {
	req := opensearchapi.UpdateRequest{
		Index:      index,
		DocumentID: docID,
		Body:       body,
		Refresh:    "true",
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return fmt.Errorf("failed to update document: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("update failed: %s", res.Status())
	}

	log.Printf("Updated document %s in index %s", docID, index)
	return nil
}

// Delete deletes a document from OpenSearch
func (r *TransactionRepository) Delete(ctx context.Context, index string, docID string) error {
	req := opensearchapi.DeleteRequest{
		Index:      index,
		DocumentID: docID,
		Refresh:    "true",
	}

	res, err := req.Do(ctx, r.client)
	if err != nil {
		return fmt.Errorf("failed to delete document: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("delete failed: %s", res.Status())
	}

	log.Printf("Deleted document %s from index %s", docID, index)
	return nil
}

// BulkIndex performs bulk indexing operations
func (r *TransactionRepository) BulkIndex(ctx context.Context, operations []repositories.BulkOperation) error {
	if len(operations) == 0 {
		return nil
	}

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
		}
		bulkBody.WriteString("\n")

		// Write the document body (not needed for delete operations)
		if op.Action != "delete" && op.Body != nil {
			bodyBytes, err := io.ReadAll(op.Body)
			if err != nil {
				return fmt.Errorf("failed to read operation body: %w", err)
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
		return fmt.Errorf("failed to execute bulk operations: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("bulk operations failed: %s", res.Status())
	}

	log.Printf("Executed %d bulk operations", len(operations))
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
