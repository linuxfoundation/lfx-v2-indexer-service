package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/logging"
)

const machineUserPrefix = "clients@"

// TransactionService handles transaction processing
type TransactionService struct {
	transactionRepo repositories.TransactionRepository
	authRepo        repositories.AuthRepository
	logger          *slog.Logger
}

// NewTransactionService creates a new transaction service
func NewTransactionService(transactionRepo repositories.TransactionRepository, authRepo repositories.AuthRepository, logger *slog.Logger) *TransactionService {
	return &TransactionService{
		transactionRepo: transactionRepo,
		authRepo:        authRepo,
		logger:          logging.WithComponent(logger, "transaction_service"),
	}
}

// EnrichTransaction enriches a transaction with additional data and validation
func (s *TransactionService) EnrichTransaction(ctx context.Context, transaction *entities.LFXTransaction) error {
	// Validate action based on transaction source
	if err := transaction.ValidateAction(); err != nil {
		return fmt.Errorf("transaction validation failed: %w", err)
	}

	// ObjectType and IsV1 are already set by IndexingUseCase from NATS subject parsing
	// No need to parse subject again - this was causing the duplicate parsing error

	// Validate object type using domain business rule
	if err := transaction.ValidateObjectType(); err != nil {
		return fmt.Errorf("object type validation failed: %w", err)
	}

	// Parse data based on action
	if err := s.parseTransactionData(transaction); err != nil {
		return fmt.Errorf("failed to parse transaction data: %w", err)
	}

	// Parse principals based on transaction type
	if transaction.IsV1 {
		transaction.ParsedPrincipals = s.parseV1Principals(transaction)
	} else {
		// Parse principals using auth repository with delegation support
		principals, err := s.authRepo.ParsePrincipals(ctx, transaction.Headers)
		if err != nil {
			return fmt.Errorf("failed to parse principals: %w", err)
		}
		transaction.ParsedPrincipals = principals
	}

	return nil
}

// GenerateTransactionBody creates a transaction body for indexing
func (s *TransactionService) GenerateTransactionBody(ctx context.Context, transaction *entities.LFXTransaction) (*entities.TransactionBody, error) {
	body := &entities.TransactionBody{
		ObjectType: transaction.ObjectType,
		V1Data:     transaction.V1Data,
	}

	// Set latest flag
	latest := true
	body.Latest = &latest

	// Set fields based on action
	canonicalAction := transaction.GetCanonicalAction()
	switch canonicalAction {
	case "created":
		body.CreatedAt = &transaction.Timestamp
		body.UpdatedAt = body.CreatedAt // For search sorting
		s.setPrincipalFields(body, transaction.ParsedPrincipals, "created")
		body.Data = transaction.ParsedData
	case "updated":
		body.UpdatedAt = &transaction.Timestamp
		s.setPrincipalFields(body, transaction.ParsedPrincipals, "updated")
		body.Data = transaction.ParsedData
	case "deleted":
		body.DeletedAt = &transaction.Timestamp
		s.setPrincipalFields(body, transaction.ParsedPrincipals, "deleted")
		body.ObjectID = transaction.ParsedObjectID
		body.ObjectRef = transaction.ObjectType + ":" + transaction.ParsedObjectID

		// Early return for delete transactions (no enrichment needed)
		return body, nil
	default:
		return nil, fmt.Errorf("unsupported action: %s", canonicalAction)
	}

	// Enrich data for create/update actions
	if err := s.enrichTransactionData(body, transaction); err != nil {
		return nil, fmt.Errorf("failed to enrich transaction data: %w", err)
	}

	// Set object reference
	body.ObjectRef = transaction.ObjectType + ":" + body.ObjectID

	return body, nil
}

// ProcessTransaction processes a complete transaction
func (s *TransactionService) ProcessTransaction(ctx context.Context, transaction *entities.LFXTransaction, index string) (*entities.ProcessingResult, error) {
	logger := logging.FromContext(ctx, s.logger)

	startTime := time.Now()
	result := &entities.ProcessingResult{
		ProcessedAt: startTime,
		MessageID:   s.generateMessageID(transaction),
	}

	logger.Info("Processing transaction",
		"action", transaction.Action,
		"object_type", transaction.ObjectType,
		"is_v1", transaction.IsV1,
		"index", index)

	// Enrich the transaction
	if err := s.EnrichTransaction(ctx, transaction); err != nil {
		logging.LogError(logger, "Failed to enrich transaction", err)
		result.Error = err
		result.Success = false
		result.Duration = time.Since(startTime)
		return result, err
	}

	// Generate transaction body
	body, err := s.GenerateTransactionBody(ctx, transaction)
	if err != nil {
		logging.LogError(logger, "Failed to generate transaction body", err)
		result.Error = err
		result.Success = false
		result.Duration = time.Since(startTime)
		return result, err
	}

	// Convert body to JSON for indexing
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		result.Error = fmt.Errorf("failed to marshal body: %w", err)
		result.Success = false
		result.Duration = time.Since(startTime)
		return result, err
	}

	// Index the transaction
	err = s.transactionRepo.Index(ctx, index, body.ObjectRef, bytes.NewReader(bodyBytes))
	if err != nil {
		result.Error = err
		result.Success = false
		result.IndexSuccess = false
		result.Duration = time.Since(startTime)
		return result, err
	}

	// Success
	result.Success = true
	result.IndexSuccess = true
	result.DocumentID = body.ObjectRef
	result.Duration = time.Since(startTime)

	return result, nil
}

// setPrincipalFields sets principal-related fields on the transaction body following deployment pattern
func (s *TransactionService) setPrincipalFields(body *entities.TransactionBody, principals []entities.Principal, action string) {
	for _, principal := range principals {
		principalRef := fmt.Sprintf("%s <%s>", principal.Principal, principal.Email)

		switch action {
		case "created":
			body.CreatedBy = append(body.CreatedBy, principalRef)
			body.CreatedByPrincipals = append(body.CreatedByPrincipals, principal.Principal)
			if principal.Email != "" {
				body.CreatedByEmails = append(body.CreatedByEmails, principal.Email)
			}
		case "updated":
			body.UpdatedBy = append(body.UpdatedBy, principalRef)
			body.UpdatedByPrincipals = append(body.UpdatedByPrincipals, principal.Principal)
			if principal.Email != "" {
				body.UpdatedByEmails = append(body.UpdatedByEmails, principal.Email)
			}
		case "deleted":
			body.DeletedBy = append(body.DeletedBy, principalRef)
			body.DeletedByPrincipals = append(body.DeletedByPrincipals, principal.Principal)
			if principal.Email != "" {
				body.DeletedByEmails = append(body.DeletedByEmails, principal.Email)
			}
		}
	}
}

// parseV1Principals converts V1 headers to Principal structure (deployment pattern)
func (s *TransactionService) parseV1Principals(transaction *entities.LFXTransaction) []entities.Principal {
	var principal entities.Principal

	for header, value := range transaction.Headers {
		header = strings.ToLower(header)
		switch header {
		case "x-username":
			principal.Principal = value
		case "x-email":
			principal.Email = value
		}
	}

	// If there was no X-Username header, return empty set
	if principal.Principal == "" {
		return nil
	}

	// Return the parsed principal as a slice of 1
	return []entities.Principal{principal}
}

// parseTransactionData parses the transaction data based on action
func (s *TransactionService) parseTransactionData(transaction *entities.LFXTransaction) error {
	switch {
	case transaction.IsCreateAction() || transaction.IsUpdateAction():
		parsedData, ok := transaction.Data.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid data for action %s: expected object", transaction.Action)
		}
		transaction.ParsedData = parsedData
	case transaction.IsDeleteAction():
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

// enrichTransactionData enriches transaction data based on object type
func (s *TransactionService) enrichTransactionData(body *entities.TransactionBody, transaction *entities.LFXTransaction) error {
	switch transaction.ObjectType {
	case "project":
		return s.enrichProjectData(body, transaction)
	default:
		return fmt.Errorf("unsupported object type: %s", transaction.ObjectType)
	}
}

// enrichProjectData enriches project-specific data
func (s *TransactionService) enrichProjectData(body *entities.TransactionBody, transaction *entities.LFXTransaction) error {
	data := transaction.ParsedData

	// Extract project ID
	if projectID, ok := data["id"].(string); ok {
		body.ObjectID = projectID
	} else {
		return fmt.Errorf("missing or invalid project ID")
	}

	// Extract project name for sorting
	if name, ok := data["name"].(string); ok {
		body.SortName = name
		body.NameAndAliases = []string{name}
	}

	// Extract slug as additional alias
	if slug, ok := data["slug"].(string); ok && slug != "" {
		body.NameAndAliases = append(body.NameAndAliases, slug)
	}

	// Set public flag
	if public, ok := data["public"].(bool); ok {
		body.Public = public
	}

	// Build fulltext search content
	var fulltext []string
	if body.SortName != "" {
		fulltext = append(fulltext, body.SortName)
	}
	for _, alias := range body.NameAndAliases {
		if alias != body.SortName {
			fulltext = append(fulltext, alias)
		}
	}
	body.Fulltext = strings.Join(fulltext, " ")

	return nil
}

// generateMessageID generates a unique message ID for tracking
func (s *TransactionService) generateMessageID(transaction *entities.LFXTransaction) string {
	timestamp := strconv.FormatInt(transaction.Timestamp.UnixNano(), 10)
	return fmt.Sprintf("%s-%s-%s", transaction.ObjectType, transaction.Action, timestamp)
}
