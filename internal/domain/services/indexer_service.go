// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package services provides domain services for the LFX indexer application.
package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// IndexerService handles transaction processing and health checking
type IndexerService struct {
	// Core dependencies
	storageRepo   contracts.StorageRepository
	docIndexer    contracts.DocumentIndexer // defaults to storageRepo; see SetDocIndexer
	messagingRepo contracts.MessagingRepository
	logger        *slog.Logger

	// Configuration
	timeout time.Duration

	// Health check caching
	mu            sync.RWMutex
	cacheDuration time.Duration
	lastReadiness *cachedResult
	lastLiveness  *cachedResult
	lastHealth    *cachedResult
}

// HealthStatus represents the overall health status of the service
type HealthStatus struct {
	Status     string           `json:"status"` // "healthy", "degraded", "unhealthy"
	Timestamp  time.Time        `json:"timestamp"`
	Duration   time.Duration    `json:"duration"`
	Checks     map[string]Check `json:"checks"`
	ErrorCount int              `json:"error_count,omitempty"`
}

// Check represents the health status of an individual component
type Check struct {
	Status    string        `json:"status"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
}

// cachedResult holds a cached health status with timestamp
type cachedResult struct {
	status    *HealthStatus
	timestamp time.Time
}

// NewIndexerService creates a new indexer service
func NewIndexerService(
	storageRepo contracts.StorageRepository,
	messagingRepo contracts.MessagingRepository,
	logger *slog.Logger,
) *IndexerService {
	return &IndexerService{
		storageRepo:   storageRepo,
		docIndexer:    storageRepo,
		messagingRepo: messagingRepo,
		logger:        logging.WithComponent(logger, constants.Component),
		timeout:       constants.HealthCheckTimeout,
		cacheDuration: constants.CacheDuration,
	}
}

// SetDocIndexer overrides the single-document indexer used by ProcessTransaction,
// e.g. to install a batching wrapper around storageRepo. Defaults to storageRepo
// itself if never called. Not safe to call concurrently with ProcessTransaction —
// intended for one-time wiring during service startup, before traffic begins.
func (s *IndexerService) SetDocIndexer(docIndexer contracts.DocumentIndexer) {
	s.docIndexer = docIndexer
}

// =================
// TRANSACTION ACTION HELPERS
// =================

// isCreateAction returns true for a completed create operation.
func (s *IndexerService) isCreateAction(transaction *contracts.LFXTransaction) bool {
	return transaction.Action == constants.ActionCreated
}

// isUpdateAction returns true for a completed update operation.
func (s *IndexerService) isUpdateAction(transaction *contracts.LFXTransaction) bool {
	return transaction.Action == constants.ActionUpdated
}

// isDeleteAction returns true for a completed delete operation.
func (s *IndexerService) isDeleteAction(transaction *contracts.LFXTransaction) bool {
	return transaction.Action == constants.ActionDeleted
}

// =================
// TRANSACTION CREATION METHODS
// =================

// CreateTransactionFromMessage creates a transaction from message data
func (s *IndexerService) CreateTransactionFromMessage(messageData map[string]any, objectType string) (*contracts.LFXTransaction, error) {
	logger := s.logger
	if err := validateEnvelopeAdmission(messageData); err != nil {
		return nil, err
	}

	// Extract action as string first, then convert to MessageAction
	actionStr, ok := messageData["action"].(string)
	if !ok || actionStr == "" {
		logging.LogError(logger, "Failed to create transaction: missing action",
			fmt.Errorf("missing or invalid action in message data"),
			"object_type", objectType)
		return nil, fmt.Errorf("missing or invalid action in message data")
	}
	action := constants.MessageAction(actionStr)

	logger.Debug("Creating transaction from message",
		"action", action,
		"object_type", objectType)

	// Create transaction directly without constructors
	transaction := &contracts.LFXTransaction{
		Action:     action,
		ObjectType: objectType,
		Data:       messageData["data"],
		Headers:    make(map[string]string),
		Timestamp:  time.Now(),
	}

	// Parse indexing_config if present (for resource indexing)
	if rawIndexingConfig, present := messageData["indexing_config"]; present && rawIndexingConfig != nil {
		indexingConfigData, ok := rawIndexingConfig.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("indexing_config must be an object")
		}
		indexingConfig, err := s.parseIndexingConfig(indexingConfigData, objectType)
		if err != nil {
			return nil, fmt.Errorf("failed to parse indexing_config: %w", err)
		}
		transaction.IndexingConfig = indexingConfig
	}

	// Convert headers
	headerCount := 0
	if headers, ok := messageData["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if str, ok := v.(string); ok {
				transaction.Headers[k] = str
				headerCount++
			}
		}
	}

	if tags, ok := messageData["tags"].([]any); ok {
		// Only add tags that are strings. Ignore other types instead of failing.
		for _, tag := range tags {
			if tagStr, ok := tag.(string); ok {
				transaction.Tags = append(transaction.Tags, tagStr)
			}
		}
	}

	logger.Debug("Transaction created successfully",
		"transaction_id", s.generateTransactionID(transaction),
		"action", action,
		"object_type", objectType,
		"tags", transaction.Tags,
		"header_count", headerCount)

	return transaction, nil
}

// ValidateTransactionData validates transaction data
func (s *IndexerService) ValidateTransactionData(transaction *contracts.LFXTransaction) error {
	logger := s.logger
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Validating transaction data",
		"transaction_id", transactionID,
		"action", transaction.Action,
		"object_type", transaction.ObjectType)

	if transaction.Data == nil {
		err := fmt.Errorf("data field is required")
		logging.LogError(logger, "Transaction data validation failed: missing data", err,
			"transaction_id", transactionID,
			"validation_step", "data_presence",
			"action", transaction.Action,
			"object_type", transaction.ObjectType)
		return err
	}

	// Try to decode the data
	if err := s.decodeTransactionData(transaction); err != nil {
		logging.LogError(logger, "Transaction data validation failed: failed to decode data", err,
			"transaction_id", transactionID,
			"validation_step", "data_decode",
			"action", transaction.Action,
			"object_type", transaction.ObjectType)
		return err
	}

	switch {
	case s.isCreateAction(transaction) || s.isUpdateAction(transaction):
		if _, ok := transaction.Data.(map[string]any); !ok {
			err := fmt.Errorf("data must be an object for %s actions", transaction.Action)
			logging.LogError(logger, "Transaction data validation failed: invalid data type", err,
				"transaction_id", transactionID,
				"validation_step", "data_type",
				"action", transaction.Action,
				"object_type", transaction.ObjectType,
				"transaction_data", transaction.Data,
				"expected_type", "object")
			return err
		}
		logger.Debug("Transaction data validation passed",
			"transaction_id", transactionID,
			"data_type", "object")
	case s.isDeleteAction(transaction):
		if _, ok := transaction.Data.(string); !ok {
			err := fmt.Errorf("data must be a string (object ID) for %s actions", transaction.Action)
			logging.LogError(logger, "Transaction data validation failed: invalid data type", err,
				"transaction_id", transactionID,
				"validation_step", "data_type",
				"action", transaction.Action,
				"object_type", transaction.ObjectType,
				"expected_type", "string")
			return err
		}
		logger.Debug("Transaction data validation passed",
			"transaction_id", transactionID,
			"data_type", "string")
	}

	logger.Debug("Transaction data validation completed successfully",
		"transaction_id", transactionID)
	return nil
}

// decodeTransactionData decodes the transaction data
func (s *IndexerService) decodeTransactionData(transaction *contracts.LFXTransaction) error {
	logger := s.logger
	transactionID := s.generateTransactionID(transaction)

	// Decode the data if it is base64 encoded
	if data, ok := transaction.Data.(string); ok {
		decodedData, err := base64.StdEncoding.DecodeString(data)
		if err == nil {
			// If there is no error, then we can unmarshal the data.
			// Otherwise, it means the data wasn't base64 encoded and therefore
			// we can just use the data as is.
			logger.Debug("Transaction data is base64 encoded, decoding",
				"transaction_id", transactionID,
				"action", transaction.Action,
				"object_type", transaction.ObjectType)
			if s.isCreateAction(transaction) || s.isUpdateAction(transaction) {
				var data map[string]any
				if err := json.Unmarshal(decodedData, &data); err != nil {
					logging.LogError(logger, "Failed to unmarshal JSON", err,
						"validation_step", "json_unmarshal",
						"transaction_data", transaction.Data,
						"transaction_id", transactionID)
					return err
				}
				transaction.Data = data
			}
		}
	}
	return nil
}

// ValidateTransactionHeaders validates transaction headers
func (s *IndexerService) ValidateTransactionHeaders(transaction *contracts.LFXTransaction) error {
	logger := s.logger
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Validating transaction headers",
		"transaction_id", transactionID,
		"header_count", len(transaction.Headers))

	if transaction.Headers == nil {
		err := fmt.Errorf("headers field is required")
		logging.LogError(logger, "Transaction header validation failed: missing headers", err,
			"transaction_id", transactionID,
			"validation_step", "headers_presence")
		return err
	}

	if err := s.validateV2Headers(transaction, transactionID); err != nil {
		return err
	}

	logger.Debug("Transaction header validation completed successfully",
		"transaction_id", transactionID)
	return nil
}

// validateV2Headers validates headers for V2 transactions
func (s *IndexerService) validateV2Headers(transaction *contracts.LFXTransaction, transactionID string) error {
	logger := s.logger
	// V2 transactions require authorization header
	if auth, exists := transaction.Headers[constants.AuthorizationHeader]; !exists || auth == "" {
		err := fmt.Errorf("authorization header is required for V2 transactions")
		logging.LogError(logger, "V2 header validation failed: missing authorization", err,
			"transaction_id", transactionID,
			"validation_step", "authorization_header",
			"has_auth_header", exists)
		return err
	}

	logger.Debug("V2 authorization header validated",
		"transaction_id", transactionID)
	return nil
}

// ValidateObjectType validates that the transaction has a non-empty object_type.
// The indexer is data-agnostic — any non-empty object_type is accepted.
func (s *IndexerService) ValidateObjectType(transaction *contracts.LFXTransaction) error {
	if transaction.ObjectType == "" {
		err := fmt.Errorf("object_type is required")
		logging.LogError(s.logger, "Object type validation failed: empty object_type", err,
			"validation_step", "object_type_check")
		return err
	}
	// Reject characters that are invalid in a NATS subject token — dots, wildcards,
	// and whitespace would corrupt the outbound event subject lfx.{object_type}.{action}
	// and could collide with the service's own inbound subscriptions.
	for _, r := range transaction.ObjectType {
		if r == '.' || r == '*' || r == '>' || r <= ' ' {
			err := fmt.Errorf("object_type %q contains invalid character for a NATS subject token", transaction.ObjectType)
			logging.LogError(s.logger, "Object type validation failed: invalid character", err,
				"validation_step", "object_type_check",
				"object_type", transaction.ObjectType)
			return err
		}
	}
	// Reject "index" — publishing lfx.index.{action} would match the service's own
	// inbound subscription lfx.index.> and create a self-triggering processing loop.
	if transaction.ObjectType == "index" {
		err := fmt.Errorf("object_type %q is reserved and cannot be used", transaction.ObjectType)
		logging.LogError(s.logger, "Object type validation failed: reserved value", err,
			"validation_step", "object_type_check",
			"object_type", transaction.ObjectType)
		return err
	}
	return nil
}

// ValidateTransactionAction accepts only completed platform V2 actions.
func (s *IndexerService) ValidateTransactionAction(transaction *contracts.LFXTransaction) error {
	logger := s.logger
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Validating transaction action",
		"transaction_id", transactionID,
		"action", transaction.Action)

	if err := s.validateV2Action(transaction, transactionID); err != nil {
		return err
	}

	logger.Debug("Transaction action validation completed successfully",
		"transaction_id", transactionID,
		"action", transaction.Action)
	return nil
}

// validateV2Action validates actions for V2 transactions
func (s *IndexerService) validateV2Action(transaction *contracts.LFXTransaction, transactionID string) error {
	logger := s.logger
	// V2 transactions are sent by regular LFX services
	// These use past-tense actions to indicate completed operations
	// Subject pattern: lfx.index.{object_type}

	logger.Debug("Validating V2 action",
		"transaction_id", transactionID,
		"action", transaction.Action)

	switch transaction.Action {
	case constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted:
		logger.Debug("V2 action validation passed",
			"transaction_id", transactionID,
			"action", transaction.Action)
		return nil
	default:
		err := fmt.Errorf("invalid V2 transaction action: %s", transaction.Action)
		logging.LogError(logger, "V2 action validation failed", err,
			"transaction_id", transactionID,
			"validation_step", "action_check",
			"action", transaction.Action,
			"valid_actions", []constants.MessageAction{constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted})
		return err
	}
}

// ExtractObjectID extracts object ID from transaction
func (s *IndexerService) ExtractObjectID(transaction *contracts.LFXTransaction) (string, error) {
	if s.isDeleteAction(transaction) {
		return transaction.ParsedObjectID, nil
	}

	if transaction.ParsedData == nil {
		return "", fmt.Errorf("parsed data is nil")
	}

	if id, ok := transaction.ParsedData["id"].(string); ok && id != "" {
		return id, nil
	}

	return "", fmt.Errorf("missing or invalid 'id' field in data")
}

// =================
// TRANSACTION PROCESSING METHODS
// =================

// EnrichTransaction enriches a transaction with additional data and validation
func (s *IndexerService) EnrichTransaction(ctx context.Context, transaction *contracts.LFXTransaction) error {
	logger := logging.FromContext(ctx, s.logger)
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Starting transaction enrichment",
		"transaction_id", transactionID,
		"action", transaction.Action,
		"object_type", transaction.ObjectType)

	// Validate transaction action
	if err := s.ValidateTransactionAction(transaction); err != nil {
		return fmt.Errorf("%s: %w", constants.ErrInvalidAction, err)
	}
	logger.Debug("Action validation completed", "transaction_id", transactionID)

	if err := s.ValidateObjectType(transaction); err != nil {
		return err
	}
	logger.Debug("Object type validation completed", "transaction_id", transactionID)

	// Validate transaction data
	if err := s.ValidateTransactionData(transaction); err != nil {
		return fmt.Errorf("invalid transaction data: %w", err)
	}
	logger.Debug("Data validation completed", "transaction_id", transactionID)

	// Validate transaction headers
	if err := s.ValidateTransactionHeaders(transaction); err != nil {
		return fmt.Errorf("invalid transaction headers: %w", err)
	}
	logger.Debug("Header validation completed", "transaction_id", transactionID)

	// Parse data based on action
	if err := s.parseTransactionData(transaction); err != nil {
		return fmt.Errorf("%s: %w", constants.ErrParseTransaction, err)
	}
	logger.Debug("Data parsing completed", "transaction_id", transactionID)

	// For create/update actions, indexing_config is required before authentication.
	canonicalAction := transaction.Action
	if canonicalAction == constants.ActionCreated || canonicalAction == constants.ActionUpdated {
		if transaction.IndexingConfig == nil {
			return fmt.Errorf("indexing_config is required for object_type %q", transaction.ObjectType)
		}
	}

	// Authenticate the publisher without collecting current-document attribution.
	// Delegated actor evidence belongs to the separate audited workflow.
	if _, err := s.messagingRepo.ValidateToken(ctx, transaction.Headers[constants.AuthorizationHeader]); err != nil {
		return fmt.Errorf("failed to authenticate transaction: %w", err)
	}

	logger.Debug("Transaction enrichment completed successfully",
		"transaction_id", transactionID)

	return nil
}

// isValidObjectID returns true if id is a non-empty string containing only
// printable, non-space ASCII characters (U+0021–U+007E).
// Go's range loop replaces invalid UTF-8 bytes with U+FFFD (> 0x7E), so
// invalid UTF-8 is caught by the same check without a separate utf8 call.
func isValidObjectID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r < 0x21 || r > 0x7E {
			return false
		}
	}
	return true
}

// GenerateTransactionBody creates a transaction body for indexing
func (s *IndexerService) GenerateTransactionBody(ctx context.Context, transaction *contracts.LFXTransaction) (*contracts.TransactionBody, error) {
	logger := logging.FromContext(ctx, s.logger)
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Generating transaction body",
		"transaction_id", transactionID,
		"object_type", transaction.ObjectType,
		"action", transaction.Action)

	body := &contracts.TransactionBody{
		ObjectType: transaction.ObjectType,
		Tags:       transaction.Tags,
	}

	// Set latest flag
	latest := true
	body.Latest = &latest

	// Set fields based on action
	canonicalAction := transaction.Action
	logger.Debug("Using canonical action",
		"transaction_id", transactionID,
		"original_action", transaction.Action,
		"canonical_action", canonicalAction)

	switch canonicalAction {
	case constants.ActionCreated:
		body.CreatedAt = &transaction.Timestamp
		body.UpdatedAt = body.CreatedAt // For search sorting
		logger.Debug("Created action body prepared",
			"transaction_id", transactionID)
	case constants.ActionUpdated:
		body.UpdatedAt = &transaction.Timestamp
		logger.Debug("Updated action body prepared",
			"transaction_id", transactionID)
	case constants.ActionDeleted:
		body.DeletedAt = &transaction.Timestamp
		if !isValidObjectID(transaction.ParsedObjectID) {
			err := fmt.Errorf("%s: %q", constants.ErrInvalidObjectID, transaction.ParsedObjectID)
			logging.LogError(logger, constants.ErrInvalidObjectID, err,
				"transaction_id", transactionID,
				"object_type", transaction.ObjectType,
				"action", canonicalAction)
			return nil, err
		}
		body.ObjectID = transaction.ParsedObjectID
		body.ObjectRef = transaction.ObjectType + ":" + transaction.ParsedObjectID
		if config := transaction.IndexingConfig; config != nil {
			if config.ObjectID != body.ObjectID {
				// Optional context must not leave the deleted resource's live data indexed.
				logger.Warn("Ignoring delete indexing_config with mismatched object_id",
					"transaction_id", transactionID,
					"object_type", transaction.ObjectType,
					"validation_step", "delete_config_identity")
			} else {
				// Retain publisher-supplied history context without making the tombstone
				// public or granting ordinary access. Do not copy search data from config.
				body.ParentRefs = config.ParentRefs
				body.HistoryCheckObject = config.HistoryCheckObject
				body.HistoryCheckRelation = config.HistoryCheckRelation
				body.HistoryCheckQuery = contracts.JoinFgaQuery(config.HistoryCheckObject, config.HistoryCheckRelation)
			}
		}

		logger.Debug("Deleted action body prepared",
			"transaction_id", transactionID,
			"object_id", body.ObjectID,
			"object_ref", body.ObjectRef)

		// Deletes have no resource data to enrich; legacy UID-only deletes remain supported.
		logger.Debug("Transaction body generation completed (delete action)", "transaction_id", transactionID)
		return body, nil
	default:
		err := fmt.Errorf("unsupported action: %s", canonicalAction)
		logging.LogError(logger, "Unsupported canonical action", err,
			"transaction_id", transactionID,
			"canonical_action", canonicalAction,
			"original_action", transaction.Action)
		return nil, err
	}

	// Enrich data for create/update actions
	if err := s.enrichTransactionData(body, transaction); err != nil {
		logging.LogError(logger, "Failed to enrich transaction data during body generation", err,
			"transaction_id", transactionID)
		return nil, fmt.Errorf("failed to enrich transaction data: %w", err)
	}
	logger.Debug("Transaction data enrichment completed", "transaction_id", transactionID)

	// Validate object ID before writing to the index
	if !isValidObjectID(body.ObjectID) {
		err := fmt.Errorf("%s: %q", constants.ErrInvalidObjectID, body.ObjectID)
		logging.LogError(logger, constants.ErrInvalidObjectID, err,
			"transaction_id", transactionID,
			"object_type", transaction.ObjectType,
			"action", canonicalAction)
		return nil, err
	}

	// Set object reference
	body.ObjectRef = transaction.ObjectType + ":" + body.ObjectID

	logger.Debug("Transaction body generation completed",
		"transaction_id", transactionID,
		"object_ref", body.ObjectRef)

	return body, nil
}

// ProcessTransaction processes a complete transaction
func (s *IndexerService) ProcessTransaction(ctx context.Context, transaction *contracts.LFXTransaction, index string) (*contracts.ProcessingResult, error) {
	logger := logging.FromContext(ctx, s.logger)
	transactionID := s.generateTransactionID(transaction)

	result := &contracts.ProcessingResult{
		ProcessedAt: time.Now(),
		MessageID:   s.generateMessageID(transaction),
	}

	logger.Debug("Processing transaction",
		"transaction_id", transactionID,
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"index", index)

	// Enrich the transaction
	if err := s.EnrichTransaction(ctx, transaction); err != nil {
		result.Error = err
		result.Success = false
		return result, err
	}
	logger.Debug("Transaction enrichment completed", "transaction_id", transactionID)

	// Generate transaction body
	body, err := s.GenerateTransactionBody(ctx, transaction)
	if err != nil {
		result.Error = err
		result.Success = false
		return result, err
	}
	logger.Debug("Transaction body generated",
		"transaction_id", transactionID,
		"object_id", body.ObjectID,
		"object_ref", body.ObjectRef)

	// Convert body to JSON for indexing
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		err = fmt.Errorf("failed to marshal body: %w", err)
		result.Error = err
		result.Success = false
		return result, err
	}
	logger.Debug("Transaction body marshaled",
		"transaction_id", transactionID,
		"body_size_bytes", len(bodyBytes))

	// Index the transaction (batched with other concurrent single-document
	// writes when docIndexer is a BatchIndexer; see SetDocIndexer)
	err = s.docIndexer.Index(ctx, index, body.ObjectRef, bytes.NewReader(bodyBytes))
	if err != nil {
		result.Error = err
		result.Success = false
		result.IndexSuccess = false
		return result, err
	}
	logger.Debug("Transaction indexed successfully",
		"transaction_id", transactionID,
		"object_ref", body.ObjectRef)

	// Success
	result.Success = true
	result.IndexSuccess = true
	result.DocumentID = body.ObjectRef

	logger.Debug("Transaction processing completed successfully",
		"transaction_id", transactionID,
		"object_ref", body.ObjectRef)

	// Publish post-indexing event. Non-blocking — failure is logged but does not
	// affect the result of the indexing operation itself.
	s.publishIndexingEvent(ctx, body, transaction.Action)

	return result, nil
}

// publishIndexingEvent publishes a domain event to NATS after a successful OpenSearch write.
// Failures are non-blocking: the error is logged and the caller continues normally.
// Subject format: lfx.{object_type}.{action} (e.g., "lfx.project.created").
func (s *IndexerService) publishIndexingEvent(ctx context.Context, body *contracts.TransactionBody, canonicalAction constants.MessageAction) {
	logger := logging.FromContext(ctx, s.logger)

	event := &contracts.IndexingEvent{
		DocumentID: body.ObjectRef,
		ObjectID:   body.ObjectID,
		ObjectType: body.ObjectType,
		Action:     canonicalAction,
		Body:       body,
		Timestamp:  time.Now().UTC(),
	}

	eventBytes, err := json.Marshal(event)
	if err != nil {
		logging.LogError(logger, "Failed to marshal indexing event — skipping publish", err,
			"document_id", body.ObjectRef,
			"action", canonicalAction,
			"object_type", body.ObjectType)
		return
	}

	subject := constants.BuildEventSubject(body.ObjectType, canonicalAction)

	if err := s.messagingRepo.Publish(ctx, subject, eventBytes); err != nil {
		logging.LogError(logger, "Failed to publish indexing event — index write already succeeded", err,
			"subject", subject,
			"document_id", body.ObjectRef,
			"action", canonicalAction,
			"object_type", body.ObjectType)
		return
	}

	logger.Debug("Indexing event published",
		"subject", subject,
		"document_id", body.ObjectRef,
		"action", canonicalAction,
		"object_type", body.ObjectType)
}

// =================
// HEALTH CHECK METHODS
// =================

// CheckReadiness performs readiness checks with simplified caching
func (s *IndexerService) CheckReadiness(ctx context.Context) *HealthStatus {
	logger := s.logger

	// Simple inline cache check
	s.mu.RLock()
	if s.lastReadiness != nil && time.Since(s.lastReadiness.timestamp) < s.cacheDuration {
		cached := s.lastReadiness.status
		s.mu.RUnlock()
		logger.Debug("Health readiness check served from cache")
		return cached
	}
	s.mu.RUnlock()

	logger.Debug("Health readiness check started")

	// Perform actual health check
	status := s.performHealthCheck(ctx)

	// Simple inline cache update
	s.mu.Lock()
	s.lastReadiness = &cachedResult{status: status, timestamp: time.Now()}
	s.mu.Unlock()

	logger.Debug("Health readiness check completed",
		"status", status.Status,
		"error_count", status.ErrorCount,
		"duration", status.Duration)

	return status
}

// CheckLiveness performs liveness checks with simplified caching
func (s *IndexerService) CheckLiveness(ctx context.Context) *HealthStatus {
	s.logger.DebugContext(ctx, "Liveness check initiated")

	// Simple inline cache check
	s.mu.RLock()
	if s.lastLiveness != nil && time.Since(s.lastLiveness.timestamp) < s.cacheDuration {
		cached := s.lastLiveness.status
		s.mu.RUnlock()
		s.logger.DebugContext(ctx, "Liveness check completed using cache", "status", cached.Status)
		return cached
	}
	s.mu.RUnlock()

	// Liveness is simpler - just check if the service itself is responsive
	status := &HealthStatus{
		Status:    constants.StatusHealthy,
		Timestamp: time.Now(),
		Duration:  0,
		Checks: map[string]Check{
			constants.ComponentService: {
				Status:    constants.StatusHealthy,
				Timestamp: time.Now(),
				Duration:  0,
			},
		},
	}

	// Simple inline cache update
	s.mu.Lock()
	s.lastLiveness = &cachedResult{status: status, timestamp: time.Now()}
	s.mu.Unlock()

	return status
}

// CheckHealth performs comprehensive health checks with simplified caching
func (s *IndexerService) CheckHealth(ctx context.Context) *HealthStatus {
	// Simple inline cache check
	s.mu.RLock()
	if s.lastHealth != nil && time.Since(s.lastHealth.timestamp) < s.cacheDuration {
		cached := s.lastHealth.status
		s.mu.RUnlock()
		return cached
	}
	s.mu.RUnlock()

	// Health check can be more lenient than readiness
	status := s.performHealthCheck(ctx)

	// For general health, we might accept degraded state as "healthy"
	if status.Status == constants.StatusDegraded {
		status.Status = constants.StatusHealthy // Accept degraded for general health
	}

	// Simple inline cache update
	s.mu.Lock()
	s.lastHealth = &cachedResult{status: status, timestamp: time.Now()}
	s.mu.Unlock()

	return status
}

// =================
// HELPER METHODS
// =================

// performHealthCheck does the actual health checking logic
func (s *IndexerService) performHealthCheck(ctx context.Context) *HealthStatus {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	status := &HealthStatus{
		Timestamp: time.Now(),
		Checks:    make(map[string]Check),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// Check storage (OpenSearch)
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		err := s.storageRepo.HealthCheck(ctx)
		check := Check{
			Duration:  time.Since(start),
			Timestamp: start,
		}
		if err != nil {
			check.Status = constants.StatusUnhealthy
			check.Error = err.Error()
			mu.Lock()
			status.ErrorCount++
			mu.Unlock()
		} else {
			check.Status = constants.StatusHealthy
		}
		mu.Lock()
		status.Checks[constants.ComponentOpenSearch] = check
		mu.Unlock()
	}()

	// Check messaging (NATS + Auth)
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		err := s.messagingRepo.HealthCheck(ctx)
		check := Check{
			Duration:  time.Since(start),
			Timestamp: start,
		}
		if err != nil {
			check.Status = constants.StatusUnhealthy
			check.Error = err.Error()
			mu.Lock()
			status.ErrorCount++
			mu.Unlock()
		} else {
			check.Status = constants.StatusHealthy
		}
		mu.Lock()
		status.Checks[constants.ComponentNATS] = check
		mu.Unlock()
	}()

	wg.Wait()
	status.Duration = time.Since(status.Timestamp)

	// Determine overall status
	switch status.ErrorCount {
	case 0:
		status.Status = constants.StatusHealthy
	case 2: // Both dependencies failed
		status.Status = constants.StatusUnhealthy
	default:
		status.Status = constants.StatusDegraded
	}

	return status
}

// parseTransactionData parses the transaction data based on action
func (s *IndexerService) parseTransactionData(transaction *contracts.LFXTransaction) error {
	switch {
	case s.isCreateAction(transaction) || s.isUpdateAction(transaction):
		parsedData, ok := transaction.Data.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid data for action %s: expected object", transaction.Action)
		}
		transaction.ParsedData = parsedData
	case s.isDeleteAction(transaction):
		objectID, ok := transaction.Data.(string)
		if !ok {
			return fmt.Errorf("invalid data for action %s: expected string", transaction.Action)
		}
		transaction.ParsedObjectID = objectID
	default:
		return fmt.Errorf("unsupported action: %s", transaction.Action)
	}

	return nil
}

// enrichTransactionData builds the transaction body from the publisher-supplied indexing_config.
// All create/update messages must include an indexing_config; the indexer is data-agnostic
// and does not perform resource-specific enrichment.
func (s *IndexerService) enrichTransactionData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	logger := s.logger
	transactionID := s.generateTransactionID(transaction)

	if transaction.IndexingConfig == nil {
		err := fmt.Errorf("indexing_config is required for object_type %q", transaction.ObjectType)
		logging.LogError(logger, "Enrichment failed: missing indexing_config", err,
			"transaction_id", transactionID,
			"object_type", transaction.ObjectType)
		return err
	}

	transactionData, ok := transaction.Data.(map[string]any)
	if !ok {
		logging.LogError(logger, "Failed to extract data from transaction", fmt.Errorf("data is not a map[string]any"),
			"transaction_id", transactionID)
		return fmt.Errorf("data is not a map[string]any")
	}

	newBody, err := s.buildTransactionBodyFromIndexingConfig(transactionData, transaction.IndexingConfig, transaction)
	if err != nil {
		logging.LogError(logger, "Failed to build transaction body from indexing config", err,
			"transaction_id", transactionID)
		return err
	}
	*body = *newBody

	logger.Debug("Transaction body built from indexing_config",
		"transaction_id", transactionID,
		"object_type", transaction.ObjectType)

	return nil
}

// generateMessageID generates a unique message ID for tracking
func (s *IndexerService) generateMessageID(transaction *contracts.LFXTransaction) string {
	timestamp := strconv.FormatInt(transaction.Timestamp.UnixNano(), 10)
	return fmt.Sprintf("%s-%s-%s", transaction.ObjectType, transaction.Action, timestamp)
}

// generateTransactionID generates a unique transaction ID for tracking
func (s *IndexerService) generateTransactionID(transaction *contracts.LFXTransaction) string {
	timestamp := strconv.FormatInt(transaction.Timestamp.UnixNano(), 10)
	return fmt.Sprintf("txn_%s_%s_%s", transaction.ObjectType, transaction.Action, timestamp)
}

// ClearCache clears all cached health statuses (useful for testing)
func (s *IndexerService) ClearCache() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastReadiness = nil
	s.lastLiveness = nil
	s.lastHealth = nil
}

// buildTransactionBodyFromIndexingConfig builds a TransactionBody from indexing_config for create/update actions
func (s *IndexerService) buildTransactionBodyFromIndexingConfig(
	data map[string]any,
	config *types.IndexingConfig,
	transaction *contracts.LFXTransaction,
) (*contracts.TransactionBody, error) {
	slog.Debug("Building transaction body from indexing config", "config", config)

	body := &contracts.TransactionBody{
		ObjectType: transaction.ObjectType,
		Data:       data,
		ObjectID:   config.ObjectID,
		ObjectRef:  fmt.Sprintf("%s:%s", transaction.ObjectType, config.ObjectID),
	}

	// Set latest flag (server-side field)
	latest := true
	body.Latest = &latest

	// Set public flag if provided
	if config.Public != nil {
		body.Public = *config.Public
	}

	// Set FGA fields (required)
	body.AccessCheckObject = config.AccessCheckObject
	body.AccessCheckRelation = config.AccessCheckRelation
	body.HistoryCheckObject = config.HistoryCheckObject
	body.HistoryCheckRelation = config.HistoryCheckRelation

	// Build FGA query strings
	body.AccessCheckQuery = contracts.JoinFgaQuery(config.AccessCheckObject, config.AccessCheckRelation)
	body.HistoryCheckQuery = contracts.JoinFgaQuery(config.HistoryCheckObject, config.HistoryCheckRelation)

	// Set optional search fields
	body.SortName = config.SortName
	body.NameAndAliases = config.NameAndAliases
	body.ParentRefs = config.ParentRefs
	// Producers may set the same tags in both the top-level message and
	// indexing_config, so de-duplicate to avoid doubled tags in the index.
	body.Tags = dedupeStrings(append(transaction.Tags, config.Tags...))
	body.Fulltext = config.Fulltext
	body.Contacts = config.Contacts

	// Set timestamps based on action (server-side fields).
	canonicalAction := transaction.Action
	switch canonicalAction {
	case constants.ActionCreated:
		body.CreatedAt = &transaction.Timestamp
		body.UpdatedAt = body.CreatedAt // For search sorting
	case constants.ActionUpdated:
		body.UpdatedAt = &transaction.Timestamp
	case constants.ActionDeleted:
		body.DeletedAt = &transaction.Timestamp
	}

	slog.Debug("Built transaction body from indexing config", "body", body)

	return body, nil
}

// dedupeStrings returns the order-preserving, first-occurrence union of the
// input, dropping later duplicates.
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// rejectTemplateReference fails an identity or authorization value that still
// contains a `{{` template reference. indexing_config values are taken
// literally, so a publisher still sending templates would otherwise index every
// resource under the same literal reference (e.g. object_id "{{ uid }}") and
// overwrite one document's access fields with another's.
func rejectTemplateReference(field, value string) error {
	if strings.Contains(value, "{{") {
		return fmt.Errorf("indexing_config.%s: template references are not supported; send a concrete value", field)
	}
	return nil
}

// parseIndexingConfig parses a map[string]any into a strongly-typed
// IndexingConfig struct. Values are taken literally; no template expansion is
// performed.
func (s *IndexerService) parseIndexingConfig(data map[string]any, objectType string) (*types.IndexingConfig, error) {
	logger := s.logger
	logger.Debug("Parsing indexing config", "object_type", objectType, "field_count", len(data))

	config := &types.IndexingConfig{}

	// Parse required identity and authorization string fields
	required := []struct {
		key string
		dst *string
	}{
		{"object_id", &config.ObjectID},
		{"access_check_object", &config.AccessCheckObject},
		{"access_check_relation", &config.AccessCheckRelation},
		{"history_check_object", &config.HistoryCheckObject},
		{"history_check_relation", &config.HistoryCheckRelation},
	}
	for _, f := range required {
		value, ok := data[f.key].(string)
		if !ok || value == "" {
			return nil, fmt.Errorf("indexing_config: %s is required and must be a non-empty string", f.key)
		}
		if err := rejectTemplateReference(f.key, value); err != nil {
			return nil, err
		}
		*f.dst = value
	}

	// Parse optional boolean field
	if publicVal, ok := data["public"].(bool); ok {
		config.Public = &publicVal
	}

	// Parse optional string fields
	if sortName, ok := data["sort_name"].(string); ok {
		config.SortName = sortName
	}

	if fulltext, ok := data["fulltext"].(string); ok {
		config.Fulltext = fulltext
	}

	// Parse optional array fields
	if nameAndAliasesData, ok := data["name_and_aliases"].([]interface{}); ok {
		nameAndAliases := make([]string, 0, len(nameAndAliasesData))
		for _, item := range nameAndAliasesData {
			if str, ok := item.(string); ok {
				nameAndAliases = append(nameAndAliases, str)
			}
		}
		config.NameAndAliases = nameAndAliases
	}

	if parentRefsData, ok := data["parent_refs"].([]interface{}); ok {
		parentRefs := make([]string, 0, len(parentRefsData))
		for _, item := range parentRefsData {
			if str, ok := item.(string); ok {
				if err := rejectTemplateReference("parent_refs", str); err != nil {
					return nil, err
				}
				parentRefs = append(parentRefs, str)
			}
		}
		config.ParentRefs = parentRefs
	}

	if tagsData, ok := data["tags"].([]interface{}); ok {
		tags := make([]string, 0, len(tagsData))
		for _, item := range tagsData {
			if str, ok := item.(string); ok {
				tags = append(tags, str)
			}
		}
		config.Tags = tags
	}

	// Parse contacts array if present
	if contactsData, ok := data["contacts"].([]interface{}); ok {
		contacts := make([]types.ContactBody, 0, len(contactsData))
		for _, item := range contactsData {
			if contactMap, ok := item.(map[string]interface{}); ok {
				contact := types.ContactBody{}

				if lfxPrincipal, ok := contactMap["lfx_principal"].(string); ok {
					contact.LfxPrincipal = lfxPrincipal
				}
				if name, ok := contactMap["name"].(string); ok {
					contact.Name = name
				}
				if emailsData, ok := contactMap["emails"].([]interface{}); ok {
					emails := make([]string, 0, len(emailsData))
					for _, email := range emailsData {
						if emailStr, ok := email.(string); ok {
							emails = append(emails, emailStr)
						}
					}
					contact.Emails = emails
				}
				if bot, ok := contactMap["bot"].(bool); ok {
					contact.Bot = &bot
				}
				if profile, ok := contactMap["profile"].(map[string]interface{}); ok {
					contact.Profile = profile
				}

				contacts = append(contacts, contact)
			}
		}
		config.Contacts = contacts
	}

	logger.Debug("Parsed indexing config", "object_type", objectType, "contact_count", len(config.Contacts))

	return config, nil
}
