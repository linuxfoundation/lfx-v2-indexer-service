// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package main provides a data migration script to add access_check_query and history_check_query
// fields to existing documents in the OpenSearch index.
//
// These fields combine the existing access control object and relation fields into query format:
// - access_check_query = access_check_object + "#" + access_check_relation
// - history_check_query = history_check_object + "#" + history_check_relation
//
// Usage:
//
//	go run scripts/data-migration/add-access-query-fields/main.go
//
// Environment variables:
//   - OPENSEARCH_URL: OpenSearch cluster URL (default: http://localhost:9200)
//   - OPENSEARCH_INDEX: Target index name (default: resources)
//   - BATCH_SIZE: Number of documents to process per batch (default: 100)
//   - DRY_RUN: If true, only log what would be updated without making changes (default: false)
//   - SCROLL_TIMEOUT: Scroll context timeout (default: 5m)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/env"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Config holds the migration configuration
type Config struct {
	OpenSearchURL string
	IndexName     string
	BatchSize     int
	DryRun        bool
	ScrollTimeout time.Duration
}

// Document represents a document from OpenSearch with access control fields
type Document struct {
	ID     string         `json:"_id"`
	Source DocumentSource `json:"_source"`
}

// DocumentSource represents the source fields of a document
type DocumentSource struct {
	AccessCheckObject    string `json:"access_check_object,omitempty"`
	AccessCheckRelation  string `json:"access_check_relation,omitempty"`
	HistoryCheckObject   string `json:"history_check_object,omitempty"`
	HistoryCheckRelation string `json:"history_check_relation,omitempty"`
	AccessCheckQuery     string `json:"access_check_query,omitempty"`
	HistoryCheckQuery    string `json:"history_check_query,omitempty"`
}

// SearchResponse represents the OpenSearch search response
type SearchResponse struct {
	ScrollID string `json:"_scroll_id,omitempty"`
	Hits     struct {
		Total struct {
			Value int `json:"value"`
		} `json:"total"`
		Hits []Document `json:"hits"`
	} `json:"hits"`
}

// BulkResponse represents the OpenSearch bulk response
type BulkResponse struct {
	Took   int                      `json:"took"`
	Errors bool                     `json:"errors"`
	Items  []map[string]interface{} `json:"items"`
}

// Stats tracks migration statistics
type Stats struct {
	TotalDocuments     int
	ProcessedDocuments int
	UpdatedDocuments   int
	SkippedDocuments   int
	ErroredDocuments   int
	StartTime          time.Time
}

// JoinFgaQuery constructs an FGA query string from object and relation
// This matches the logic in internal/domain/contracts/fga.go
func JoinFgaQuery(object, relation string) string {
	return fmt.Sprintf("%s#%s", object, relation)
}

// loadConfig loads configuration from environment variables with defaults
func loadConfig() *Config {
	config := &Config{
		OpenSearchURL: env.GetString("OPENSEARCH_URL", "http://localhost:9200"),
		IndexName:     env.GetString("OPENSEARCH_INDEX", "resources"),
		BatchSize:     env.GetInt("BATCH_SIZE", 100),
		DryRun:        env.GetBool("DRY_RUN", false),
		ScrollTimeout: env.GetDuration("SCROLL_TIMEOUT", 5*time.Minute),
	}

	log.Println("=== Migration Configuration ===")
	log.Printf("  OpenSearch URL: %s", config.OpenSearchURL)
	log.Printf("  Index Name: %s", config.IndexName)
	log.Printf("  Batch Size: %d", config.BatchSize)
	log.Printf("  Dry Run: %t", config.DryRun)
	log.Printf("  Scroll Timeout: %v", config.ScrollTimeout)
	log.Println("==============================")

	return config
}


// createOpenSearchClient creates and configures OpenSearch client
func createOpenSearchClient(config *Config) (*opensearch.Client, error) {
	cfg := opensearch.Config{
		Addresses: []string{config.OpenSearchURL},
		// Add any authentication configuration here if needed
	}

	client, err := opensearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenSearch client: %w", err)
	}

	// Test connection
	info, err := client.Info()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to OpenSearch: %w", err)
	}
	defer info.Body.Close()

	log.Println("✓ Connected to OpenSearch successfully")
	return client, nil
}

// needsUpdate checks if a document needs the new query fields added
func needsUpdate(doc DocumentSource) (needsAccessQuery, needsHistoryQuery bool) {
	// Check if access query is needed
	if doc.AccessCheckQuery == "" && doc.AccessCheckObject != "" && doc.AccessCheckRelation != "" {
		needsAccessQuery = true
	}

	// Check if history query is needed
	if doc.HistoryCheckQuery == "" && doc.HistoryCheckObject != "" && doc.HistoryCheckRelation != "" {
		needsHistoryQuery = true
	}

	return needsAccessQuery, needsHistoryQuery
}

// buildQueryFields constructs the new query fields from existing access control fields
func buildQueryFields(doc DocumentSource) (accessQuery, historyQuery string) {
	// Build access check query if both components are present and non-empty
	if doc.AccessCheckObject != "" && doc.AccessCheckRelation != "" {
		accessQuery = JoinFgaQuery(doc.AccessCheckObject, doc.AccessCheckRelation)
	}

	// Build history check query if both components are present and non-empty
	if doc.HistoryCheckObject != "" && doc.HistoryCheckRelation != "" {
		historyQuery = JoinFgaQuery(doc.HistoryCheckObject, doc.HistoryCheckRelation)
	}

	return accessQuery, historyQuery
}

// searchDocuments initiates a scroll search for documents that need migration
func searchDocuments(ctx context.Context, client *opensearch.Client, config *Config) (*SearchResponse, error) {
	// Search for documents that have access control fields but are missing query fields
	searchBody := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"bool": map[string]interface{}{
							"must": []map[string]interface{}{
								{"exists": map[string]interface{}{"field": "access_check_object"}},
								{"exists": map[string]interface{}{"field": "access_check_relation"}},
							},
							"must_not": []map[string]interface{}{
								{"exists": map[string]interface{}{"field": "access_check_query"}},
							},
						},
					},
					{
						"bool": map[string]interface{}{
							"must": []map[string]interface{}{
								{"exists": map[string]interface{}{"field": "history_check_object"}},
								{"exists": map[string]interface{}{"field": "history_check_relation"}},
							},
							"must_not": []map[string]interface{}{
								{"exists": map[string]interface{}{"field": "history_check_query"}},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
		"size": config.BatchSize,
		"_source": []string{
			"access_check_object",
			"access_check_relation",
			"history_check_object",
			"history_check_relation",
			"access_check_query",
			"history_check_query",
		},
	}

	searchBodyJSON, err := json.Marshal(searchBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search body: %w", err)
	}

	req := opensearchapi.SearchRequest{
		Index:  []string{config.IndexName},
		Body:   strings.NewReader(string(searchBodyJSON)),
		Scroll: config.ScrollTimeout,
	}

	res, err := req.Do(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search request failed: %s", res.String())
	}

	var searchResponse SearchResponse
	if err := json.NewDecoder(res.Body).Decode(&searchResponse); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %w", err)
	}

	return &searchResponse, nil
}

// scrollDocuments continues scrolling through search results
func scrollDocuments(ctx context.Context, client *opensearch.Client, scrollID string, scrollTimeout time.Duration) (*SearchResponse, error) {
	scrollBody := map[string]interface{}{
		"scroll_id": scrollID,
		"scroll":    fmt.Sprintf("%dm", int(scrollTimeout.Minutes())),
	}

	scrollBodyJSON, err := json.Marshal(scrollBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal scroll body: %w", err)
	}

	req := opensearchapi.ScrollRequest{
		Body: strings.NewReader(string(scrollBodyJSON)),
	}

	res, err := req.Do(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to execute scroll: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("scroll request failed: %s", res.String())
	}

	var searchResponse SearchResponse
	if err := json.NewDecoder(res.Body).Decode(&searchResponse); err != nil {
		return nil, fmt.Errorf("failed to parse scroll response: %w", err)
	}

	return &searchResponse, nil
}

// processBatch processes a batch of documents and updates them
func processBatch(ctx context.Context, client *opensearch.Client, config *Config, documents []Document, stats *Stats) error {
	if len(documents) == 0 {
		return nil
	}

	var bulkBody strings.Builder
	updateCount := 0

	for _, doc := range documents {
		stats.ProcessedDocuments++

		needsAccessQuery, needsHistoryQuery := needsUpdate(doc.Source)
		if !needsAccessQuery && !needsHistoryQuery {
			stats.SkippedDocuments++
			continue
		}

		accessQuery, historyQuery := buildQueryFields(doc.Source)

		// Build update document
		updateDoc := make(map[string]interface{})
		if needsAccessQuery && accessQuery != "" {
			updateDoc["access_check_query"] = accessQuery
		}
		if needsHistoryQuery && historyQuery != "" {
			updateDoc["history_check_query"] = historyQuery
		}

		// Skip if no updates needed (both queries empty)
		if len(updateDoc) == 0 {
			stats.SkippedDocuments++
			continue
		}

		if config.DryRun {
			log.Printf("[DRY RUN] Would update document %s:", doc.ID)
			if accessQuery != "" {
				log.Printf("  - access_check_query: %s", accessQuery)
			}
			if historyQuery != "" {
				log.Printf("  - history_check_query: %s", historyQuery)
			}
			stats.UpdatedDocuments++
			continue
		}

		// Add to bulk body
		bulkBody.WriteString(fmt.Sprintf(`{"update":{"_index":"%s","_id":"%s"}}`, config.IndexName, doc.ID))
		bulkBody.WriteString("\n")

		updateJSON, _ := json.Marshal(map[string]interface{}{"doc": updateDoc})
		bulkBody.Write(updateJSON)
		bulkBody.WriteString("\n")

		updateCount++
	}

	// Execute bulk update if not in dry run and there are updates
	if !config.DryRun && updateCount > 0 {
		req := opensearchapi.BulkRequest{
			Body: strings.NewReader(bulkBody.String()),
		}

		res, err := req.Do(ctx, client)
		if err != nil {
			stats.ErroredDocuments += updateCount
			return fmt.Errorf("failed to execute bulk update: %w", err)
		}
		defer res.Body.Close()

		if res.IsError() {
			stats.ErroredDocuments += updateCount
			return fmt.Errorf("bulk update failed: %s", res.String())
		}

		// Parse response to check for errors
		var bulkResponse BulkResponse
		if err := json.NewDecoder(res.Body).Decode(&bulkResponse); err != nil {
			return fmt.Errorf("failed to parse bulk response: %w", err)
		}

		if bulkResponse.Errors {
			// Count individual errors
			errorCount := 0
			for _, item := range bulkResponse.Items {
				if update, ok := item["update"].(map[string]interface{}); ok {
					if _, hasError := update["error"]; hasError {
						errorCount++
					}
				}
			}
			stats.ErroredDocuments += errorCount
			stats.UpdatedDocuments += (updateCount - errorCount)
			return fmt.Errorf("bulk update had %d errors out of %d updates", errorCount, updateCount)
		}

		stats.UpdatedDocuments += updateCount
	}

	return nil
}

// clearScroll clears the scroll context
func clearScroll(ctx context.Context, client *opensearch.Client, scrollID string) {
	if scrollID == "" {
		return
	}

	req := opensearchapi.ClearScrollRequest{
		ScrollID: []string{scrollID},
	}

	if res, err := req.Do(ctx, client); err == nil {
		res.Body.Close()
	}
}

// printStats prints the migration statistics
func printStats(stats *Stats) {
	duration := time.Since(stats.StartTime)

	log.Println("\n=== Migration Statistics ===")
	log.Printf("Total Documents Found: %d", stats.TotalDocuments)
	log.Printf("Documents Processed: %d", stats.ProcessedDocuments)
	log.Printf("Documents Updated: %d", stats.UpdatedDocuments)
	log.Printf("Documents Skipped: %d", stats.SkippedDocuments)
	log.Printf("Documents with Errors: %d", stats.ErroredDocuments)
	log.Printf("Duration: %v", duration)

	if stats.ProcessedDocuments > 0 {
		rate := float64(stats.ProcessedDocuments) / duration.Seconds()
		log.Printf("Processing Rate: %.2f docs/sec", rate)
	}
	log.Println("============================")
}

func main() {
	log.Println("Starting access query fields migration...")

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("\n⚠ Received interrupt signal, shutting down gracefully...")
		cancel()
	}()

	// Load configuration
	config := loadConfig()

	// Initialize statistics
	stats := &Stats{
		StartTime: time.Now(),
	}

	// Create OpenSearch client
	client, err := createOpenSearchClient(config)
	if err != nil {
		log.Fatalf("Failed to create OpenSearch client: %v", err)
	}

	// Initial search
	log.Println("Searching for documents that need migration...")
	searchResponse, err := searchDocuments(ctx, client, config)
	if err != nil {
		log.Fatalf("Failed to search documents: %v", err)
	}

	stats.TotalDocuments = searchResponse.Hits.Total.Value
	log.Printf("Found %d documents that may need migration", stats.TotalDocuments)

	if stats.TotalDocuments == 0 {
		log.Println("✓ No documents need migration. Exiting.")
		return
	}

	scrollID := searchResponse.ScrollID
	defer clearScroll(context.Background(), client, scrollID)

	batchNumber := 0
	for {
		// Check for cancellation
		select {
		case <-ctx.Done():
			log.Println("Migration cancelled by user")
			printStats(stats)
			return
		default:
		}

		// Process current batch
		if len(searchResponse.Hits.Hits) > 0 {
			batchNumber++
			log.Printf("\nProcessing batch %d (%d documents)...", batchNumber, len(searchResponse.Hits.Hits))

			if err := processBatch(ctx, client, config, searchResponse.Hits.Hits, stats); err != nil {
				log.Printf("Warning: Error processing batch %d: %v", batchNumber, err)
			}

			// Progress update
			percentComplete := float64(stats.ProcessedDocuments) * 100 / float64(stats.TotalDocuments)
			log.Printf("Progress: %d/%d documents (%.1f%%)", stats.ProcessedDocuments, stats.TotalDocuments, percentComplete)
		}

		// Check if we have more documents to process
		if len(searchResponse.Hits.Hits) < config.BatchSize {
			break
		}

		// Get next batch
		searchResponse, err = scrollDocuments(ctx, client, scrollID, config.ScrollTimeout)
		if err != nil {
			log.Printf("Warning: Failed to scroll documents: %v", err)
			break
		}

		// Update scroll ID for cleanup
		if searchResponse.ScrollID != "" {
			scrollID = searchResponse.ScrollID
		}

		// Break if no more documents
		if len(searchResponse.Hits.Hits) == 0 {
			break
		}
	}

	// Print final statistics
	printStats(stats)

	if config.DryRun {
		log.Println("\n✓ DRY RUN COMPLETE: No actual changes were made")
		log.Println("  Run without DRY_RUN=true to apply changes")
	} else {
		log.Println("\n✓ Migration completed successfully!")
	}
}
