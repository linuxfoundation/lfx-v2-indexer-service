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

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-indexer-service/internal/enrichers"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
)

// IndexerService handles transaction processing and health checking
type IndexerService struct {
	// Core dependencies
	storageRepo   contracts.StorageRepository
	messagingRepo contracts.MessagingRepository
	logger        *slog.Logger

	// Enrichment registry for extensible object-specific enrichment
	enricherRegistry *enrichers.Registry

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
	// Initialize enricher registry with project enricher
	registry := enrichers.NewRegistry()
	registry.Register(enrichers.NewProjectEnricher())

	return &IndexerService{
		storageRepo:      storageRepo,
		messagingRepo:    messagingRepo,
		logger:           logging.WithComponent(logger, constants.Component),
		enricherRegistry: registry,
		timeout:          constants.HealthCheckTimeout,
		cacheDuration:    constants.CacheDuration,
	}
}

// =================
// TRANSACTION ACTION HELPERS
// =================

// isCreateAction returns true if this is a create action (both V1 and V2)
func (s *IndexerService) isCreateAction(transaction *contracts.LFXTransaction) bool {
	return transaction.Action == constants.ActionCreate || transaction.Action == constants.ActionCreated
}

// isUpdateAction returns true if this is an update action (both V1 and V2)
func (s *IndexerService) isUpdateAction(transaction *contracts.LFXTransaction) bool {
	return transaction.Action == constants.ActionUpdate || transaction.Action == constants.ActionUpdated
}

// isDeleteAction returns true if this is a delete action (both V1 and V2)
func (s *IndexerService) isDeleteAction(transaction *contracts.LFXTransaction) bool {
	return transaction.Action == constants.ActionDelete || transaction.Action == constants.ActionDeleted
}

// =================
// TRANSACTION CREATION METHODS
// =================

// CreateTransactionFromMessage creates a transaction from message data
func (s *IndexerService) CreateTransactionFromMessage(messageData map[string]any, objectType string, isV1 bool) (*contracts.LFXTransaction, error) {
	logger := s.logger

	action, ok := messageData["action"].(string)
	if !ok {
		logging.LogError(logger, "Failed to create transaction: missing action",
			fmt.Errorf("missing or invalid action in message data"),
			"object_type", objectType,
			"is_v1", isV1)
		return nil, fmt.Errorf("missing or invalid action in message data")
	}

	logger.Debug("Creating transaction from message",
		"action", action,
		"object_type", objectType,
		"is_v1", isV1)

	// Create transaction directly without constructors
	transaction := &contracts.LFXTransaction{
		Action:     action,
		ObjectType: objectType,
		Data:       messageData["data"],
		Headers:    make(map[string]string),
		Timestamp:  time.Now(),
		IsV1:       isV1,
	}

	// Set V1 data if this is a V1 transaction
	if isV1 {
		s.setV1Data(transaction, messageData)
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

	logger.Info("Transaction created successfully",
		"transaction_id", s.generateTransactionID(transaction),
		"action", action,
		"object_type", objectType,
		"is_v1", isV1,
		"header_count", headerCount)

	return transaction, nil
}

// setV1Data sets V1-specific data on the transaction
func (s *IndexerService) setV1Data(transaction *contracts.LFXTransaction, messageData map[string]any) {
	if v1Data, ok := messageData["v1_data"].(map[string]any); ok {
		transaction.V1Data = v1Data
		s.logger.Debug("V1 data set on transaction",
			"object_type", transaction.ObjectType,
			"v1_data_fields", len(v1Data))
	} else {
		s.logger.Debug("No V1 data found in message",
			"object_type", transaction.ObjectType)
	}
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

	// Decode the data if it is base64 encoded
	if data, ok := transaction.Data.(string); ok {
		decodedData, err := base64.StdEncoding.DecodeString(data)
		if err == nil {
			// If there is no error, then we can unmarshal the data.
			// Otherwise, it means the data wasn't base64 encoded and therefore
			// we can just use the data as is.
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

// ValidateTransactionHeaders validates transaction headers
func (s *IndexerService) ValidateTransactionHeaders(transaction *contracts.LFXTransaction) error {
	logger := s.logger
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Validating transaction headers",
		"transaction_id", transactionID,
		"is_v1", transaction.IsV1,
		"header_count", len(transaction.Headers))

	if transaction.Headers == nil {
		err := fmt.Errorf("headers field is required")
		logging.LogError(logger, "Transaction header validation failed: missing headers", err,
			"transaction_id", transactionID,
			"validation_step", "headers_presence",
			"is_v1", transaction.IsV1)
		return err
	}

	// Dispatch to version-specific validation
	var err error
	if transaction.IsV1 {
		err = s.validateV1Headers(transaction, transactionID)
	} else {
		err = s.validateV2Headers(transaction, transactionID)
	}

	if err != nil {
		return err
	}

	logger.Debug("Transaction header validation completed successfully",
		"transaction_id", transactionID,
		"is_v1", transaction.IsV1)
	return nil
}

// validateV1Headers validates headers for V1 transactions
func (s *IndexerService) validateV1Headers(transaction *contracts.LFXTransaction, transactionID string) error {
	logger := s.logger
	// V1 transactions don't require authorization headers
	// They use X-Username and X-Email headers instead

	logger.Debug("Validating V1 headers",
		"transaction_id", transactionID,
		"has_x_username", transaction.Headers[constants.HeaderXUsername] != "",
		"has_x_email", transaction.Headers[constants.HeaderXEmail] != "")

	// Log if V1 headers are present (for debugging)
	if username := transaction.Headers[constants.HeaderXUsername]; username != "" {
		logger.Debug("V1 username header found",
			"transaction_id", transactionID,
			"username", username)
	}

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

// ValidateObjectType validates object type using the enricher registry
func (s *IndexerService) ValidateObjectType(transaction *contracts.LFXTransaction) error {
	logger := s.logger
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Validating object type",
		"transaction_id", transactionID,
		"object_type", transaction.ObjectType)

	// Check if we have an enricher registered for this object type
	enricher, exists := s.enricherRegistry.GetEnricher(transaction.ObjectType)
	if !exists {
		err := fmt.Errorf("no enricher found for object type: %s", transaction.ObjectType)
		logging.LogError(logger, "Object type validation failed: no enricher found", err,
			"transaction_id", transactionID,
			"validation_step", "enricher_lookup",
			"object_type", transaction.ObjectType)
		return err
	}

	logger.Debug("Object type validation passed",
		"transaction_id", transactionID,
		"object_type", transaction.ObjectType,
		"enricher_type", fmt.Sprintf("%T", enricher))
	return nil
}

// ValidateTransactionAction validates the transaction action based on transaction version
func (s *IndexerService) ValidateTransactionAction(transaction *contracts.LFXTransaction) error {
	logger := s.logger
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Validating transaction action",
		"transaction_id", transactionID,
		"action", transaction.Action,
		"is_v1", transaction.IsV1)

	// Dispatch to version-specific validation
	var err error
	if transaction.IsV1 {
		err = s.validateV1Action(transaction, transactionID)
	} else {
		err = s.validateV2Action(transaction, transactionID)
	}

	if err != nil {
		return err
	}

	logger.Debug("Transaction action validation completed successfully",
		"transaction_id", transactionID,
		"action", transaction.Action,
		"is_v1", transaction.IsV1)
	return nil
}

// validateV1Action validates actions for V1 transactions
func (s *IndexerService) validateV1Action(transaction *contracts.LFXTransaction, transactionID string) error {
	logger := s.logger
	// V1 transactions are sent by the v1-sync-helper service
	// These use present-tense actions to match the original V1 API format
	// Subject pattern: lfx.v1.index.{object_type}

	logger.Debug("Validating V1 action",
		"transaction_id", transactionID,
		"action", transaction.Action)

	switch transaction.Action {
	case constants.ActionCreate, constants.ActionUpdate, constants.ActionDelete:
		logger.Debug("V1 action validation passed",
			"transaction_id", transactionID,
			"action", transaction.Action)
		return nil
	default:
		err := fmt.Errorf("invalid V1 transaction action: %s", transaction.Action)
		logging.LogError(logger, "V1 action validation failed", err,
			"transaction_id", transactionID,
			"validation_step", "action_check",
			"action", transaction.Action,
			"valid_actions", []string{constants.ActionCreate, constants.ActionUpdate, constants.ActionDelete})
		return err
	}
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
			"valid_actions", []string{constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted})
		return err
	}
}

// GetCanonicalAction returns the canonical (past-tense) action for indexing
func (s *IndexerService) GetCanonicalAction(transaction *contracts.LFXTransaction) string {
	switch transaction.Action {
	case constants.ActionCreate, constants.ActionCreated:
		return constants.ActionCreated
	case constants.ActionUpdate, constants.ActionUpdated:
		return constants.ActionUpdated
	case constants.ActionDelete, constants.ActionDeleted:
		return constants.ActionDeleted
	default:
		return transaction.Action
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

	logger.Info("Starting transaction enrichment",
		"transaction_id", transactionID,
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"is_v1", transaction.IsV1)

	// Validate transaction action
	if err := s.ValidateTransactionAction(transaction); err != nil {
		logging.LogError(logger, "Transaction enrichment failed: action validation", err,
			"transaction_id", transactionID,
			"step", "validate_action")
		return fmt.Errorf("%s: %w", constants.ErrInvalidAction, err)
	}
	logger.Debug("Action validation completed", "transaction_id", transactionID)

	// Validate object type using enricher registry
	if err := s.ValidateObjectType(transaction); err != nil {
		logging.LogError(logger, "Transaction enrichment failed: object type validation", err,
			"transaction_id", transactionID,
			"step", "validate_object_type")
		return fmt.Errorf("%s: %w", constants.ErrInvalidObjectType, err)
	}
	logger.Debug("Object type validation completed", "transaction_id", transactionID)

	// Validate transaction data
	if err := s.ValidateTransactionData(transaction); err != nil {
		logging.LogError(logger, "Transaction enrichment failed: data validation", err,
			"transaction_id", transactionID,
			"step", "validate_data")
		return fmt.Errorf("invalid transaction data: %w", err)
	}
	logger.Debug("Data validation completed", "transaction_id", transactionID)

	// Validate transaction headers
	if err := s.ValidateTransactionHeaders(transaction); err != nil {
		logging.LogError(logger, "Transaction enrichment failed: header validation", err,
			"transaction_id", transactionID,
			"step", "validate_headers")
		return fmt.Errorf("invalid transaction headers: %w", err)
	}
	logger.Debug("Header validation completed", "transaction_id", transactionID)

	// Parse data based on action
	if err := s.parseTransactionData(transaction); err != nil {
		logging.LogError(logger, "Transaction enrichment failed: data parsing", err,
			"transaction_id", transactionID,
			"step", "parse_data")
		return fmt.Errorf("%s: %w", constants.ErrParseTransaction, err)
	}
	logger.Debug("Data parsing completed", "transaction_id", transactionID)

	// Parse principals based on transaction version
	principals, err := s.parsePrincipals(ctx, transaction)
	if err != nil {
		logging.LogError(logger, "Transaction enrichment failed: principal parsing", err,
			"transaction_id", transactionID,
			"step", "parse_principals")
		return fmt.Errorf("failed to parse principals: %w", err)
	}
	transaction.ParsedPrincipals = principals
	logger.Debug("Principal parsing completed",
		"transaction_id", transactionID,
		"principal_count", len(principals))

	logger.Info("Transaction enrichment completed successfully",
		"transaction_id", transactionID,
		"principal_count", len(principals))

	return nil
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
		V1Data:     transaction.V1Data,
	}

	// Set latest flag
	latest := true
	body.Latest = &latest

	// Set fields based on action
	canonicalAction := s.GetCanonicalAction(transaction)
	logger.Debug("Using canonical action",
		"transaction_id", transactionID,
		"original_action", transaction.Action,
		"canonical_action", canonicalAction)

	switch canonicalAction {
	case constants.ActionCreated:
		body.CreatedAt = &transaction.Timestamp
		body.UpdatedAt = body.CreatedAt // For search sorting
		s.setPrincipalFields(body, transaction.ParsedPrincipals, constants.ActionCreated)
		logger.Debug("Created action body prepared",
			"transaction_id", transactionID,
			"principal_count", len(transaction.ParsedPrincipals))
	case constants.ActionUpdated:
		body.UpdatedAt = &transaction.Timestamp
		s.setPrincipalFields(body, transaction.ParsedPrincipals, constants.ActionUpdated)
		logger.Debug("Updated action body prepared",
			"transaction_id", transactionID,
			"principal_count", len(transaction.ParsedPrincipals))
	case constants.ActionDeleted:
		body.DeletedAt = &transaction.Timestamp
		s.setPrincipalFields(body, transaction.ParsedPrincipals, constants.ActionDeleted)
		body.ObjectID = transaction.ParsedObjectID
		body.ObjectRef = transaction.ObjectType + ":" + transaction.ParsedObjectID

		logger.Debug("Deleted action body prepared",
			"transaction_id", transactionID,
			"object_id", body.ObjectID,
			"object_ref", body.ObjectRef,
			"principal_count", len(transaction.ParsedPrincipals))

		// Early return for delete transactions (no enrichment needed)
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

	logger.Info("Processing transaction",
		"transaction_id", transactionID,
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"is_v1", transaction.IsV1,
		"index", index)

	// Enrich the transaction
	if err := s.EnrichTransaction(ctx, transaction); err != nil {
		logging.LogError(logger, "Failed to enrich transaction", err,
			"transaction_id", transactionID,
			"step", "enrichment")
		result.Error = err
		result.Success = false
		return result, err
	}
	logger.Debug("Transaction enrichment completed", "transaction_id", transactionID)

	// Generate transaction body
	body, err := s.GenerateTransactionBody(ctx, transaction)
	if err != nil {
		logging.LogError(logger, "Failed to generate transaction body", err,
			"transaction_id", transactionID,
			"step", "body_generation")
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
		logging.LogError(logger, "Failed to marshal transaction body", err,
			"transaction_id", transactionID,
			"step", "json_marshaling")
		result.Error = err
		result.Success = false
		return result, err
	}
	logger.Debug("Transaction body marshaled",
		"transaction_id", transactionID,
		"body_size_bytes", len(bodyBytes))

	// Index the transaction using storage repository
	err = s.storageRepo.Index(ctx, index, body.ObjectRef, bytes.NewReader(bodyBytes))
	if err != nil {
		logging.LogError(logger, "Failed to index transaction", err,
			"transaction_id", transactionID,
			"step", "indexing",
			"index", index,
			"object_ref", body.ObjectRef)
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

	logger.Info("Transaction processing completed successfully",
		"transaction_id", transactionID,
		"object_ref", body.ObjectRef)

	return result, nil
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

	logger.Info("Health readiness check completed",
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

// setPrincipalFields sets principal-related fields on the transaction body
func (s *IndexerService) setPrincipalFields(body *contracts.TransactionBody, principals []contracts.Principal, action string) {
	for _, principal := range principals {
		principalRef := fmt.Sprintf("%s <%s>", principal.Principal, principal.Email)

		switch action {
		case constants.ActionCreated:
			body.CreatedBy = append(body.CreatedBy, principalRef)
			body.CreatedByPrincipals = append(body.CreatedByPrincipals, principal.Principal)
			if principal.Email != "" {
				body.CreatedByEmails = append(body.CreatedByEmails, principal.Email)
			}
		case constants.ActionUpdated:
			body.UpdatedBy = append(body.UpdatedBy, principalRef)
			body.UpdatedByPrincipals = append(body.UpdatedByPrincipals, principal.Principal)
			if principal.Email != "" {
				body.UpdatedByEmails = append(body.UpdatedByEmails, principal.Email)
			}
		case constants.ActionDeleted:
			body.DeletedBy = append(body.DeletedBy, principalRef)
			body.DeletedByPrincipals = append(body.DeletedByPrincipals, principal.Principal)
			if principal.Email != "" {
				body.DeletedByEmails = append(body.DeletedByEmails, principal.Email)
			}
		}
	}
}

// parsePrincipals parses principals based on transaction version
func (s *IndexerService) parsePrincipals(ctx context.Context, transaction *contracts.LFXTransaction) ([]contracts.Principal, error) {
	logger := logging.FromContext(ctx, s.logger)
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Parsing principals",
		"transaction_id", transactionID,
		"is_v1", transaction.IsV1,
		"header_count", len(transaction.Headers))

	// Dispatch to version-specific parsing
	if transaction.IsV1 {
		principals := s.parseV1Principals(transaction, transactionID)
		logger.Debug("V1 principals parsed",
			"transaction_id", transactionID,
			"principal_count", len(principals))
		return principals, nil
	}

	principals, err := s.parseV2Principals(ctx, transaction, transactionID)
	if err != nil {
		logging.LogError(logger, "Failed to parse V2 principals", err,
			"transaction_id", transactionID)
		return nil, err
	}

	logger.Debug("V2 principals parsed",
		"transaction_id", transactionID,
		"principal_count", len(principals))
	return principals, nil
}

// parseV1Principals converts V1 headers to Principal structure
func (s *IndexerService) parseV1Principals(transaction *contracts.LFXTransaction, transactionID string) []contracts.Principal {
	logger := s.logger
	var principal contracts.Principal

	for header, value := range transaction.Headers {
		header = strings.ToLower(header)
		switch header {
		case constants.HeaderXUsername:
			principal.Principal = value
			logger.Debug("Found V1 username header",
				"transaction_id", transactionID,
				"username", value)
		case constants.HeaderXEmail:
			principal.Email = value
			logger.Debug("Found V1 email header",
				"transaction_id", transactionID,
				"email", value)
		}
	}

	// If there was no X-Username header, return empty set
	if principal.Principal == "" {
		logger.Debug("No V1 username header found, returning empty principals",
			"transaction_id", transactionID)
		return nil
	}

	// Return the parsed principal as a slice of 1
	return []contracts.Principal{principal}
}

// parseV2Principals parses principals for V2 transactions
func (s *IndexerService) parseV2Principals(ctx context.Context, transaction *contracts.LFXTransaction, transactionID string) ([]contracts.Principal, error) {
	logger := logging.FromContext(ctx, s.logger)

	logger.Debug("Parsing V2 principals using messaging repository",
		"transaction_id", transactionID,
		"auth_header_present", transaction.Headers[constants.AuthorizationHeader] != "")

	// Parse principals using messaging repository
	principals, err := s.messagingRepo.ParsePrincipals(ctx, transaction.Headers)
	if err != nil {
		logging.LogError(logger, "Failed to parse V2 principals via messaging repository", err,
			"transaction_id", transactionID)
		return nil, err
	}

	logger.Debug("V2 principals parsed successfully",
		"transaction_id", transactionID,
		"principal_count", len(principals))
	return principals, nil
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

// enrichTransactionData enriches transaction data using the enricher registry
func (s *IndexerService) enrichTransactionData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	logger := s.logger
	transactionID := s.generateTransactionID(transaction)

	logger.Debug("Starting transaction data enrichment",
		"transaction_id", transactionID,
		"object_type", transaction.ObjectType,
		"action", transaction.Action)

	// Use the registry to find the appropriate enricher for the object type
	enricher, exists := s.enricherRegistry.GetEnricher(transaction.ObjectType)
	if !exists {
		err := fmt.Errorf("no enricher found for object type: %s", transaction.ObjectType)
		logging.LogError(logger, "Enrichment failed: no enricher found", err,
			"transaction_id", transactionID,
			"object_type", transaction.ObjectType,
			"enrichment_step", "enricher_lookup")
		return err
	}

	logger.Debug("Found enricher for object type",
		"transaction_id", transactionID,
		"object_type", transaction.ObjectType,
		"enricher_type", fmt.Sprintf("%T", enricher))

	// Delegate to the specific enricher
	if err := enricher.EnrichData(body, transaction); err != nil {
		logging.LogError(logger, "Enrichment failed during data processing", err,
			"transaction_id", transactionID,
			"object_type", transaction.ObjectType,
			"enrichment_step", "data_processing")
		return err
	}

	logger.Info("Transaction data enrichment completed successfully",
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
