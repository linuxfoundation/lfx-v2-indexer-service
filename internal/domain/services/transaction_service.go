package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/valueobjects"
)

const machineUserPrefix = "clients@"

// TransactionService handles transaction processing logic
type TransactionService struct {
	transactionRepo repositories.TransactionRepository
	authRepo        repositories.AuthRepository
}

// NewTransactionService creates a new transaction service
func NewTransactionService(
	transactionRepo repositories.TransactionRepository,
	authRepo repositories.AuthRepository,
) *TransactionService {
	return &TransactionService{
		transactionRepo: transactionRepo,
		authRepo:        authRepo,
	}
}

// ParseTransaction parses a transaction from message data
func (s *TransactionService) ParseTransaction(ctx context.Context, data []byte, subject string) (*entities.LFXTransaction, error) {
	var transaction entities.LFXTransaction
	if err := json.Unmarshal(data, &transaction); err != nil {
		return nil, fmt.Errorf("failed to unmarshal transaction: %w", err)
	}

	// Extract object type from subject
	parts := strings.Split(subject, ".")
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid subject format: %s", subject)
	}
	transaction.ObjectType = parts[2]

	// Parse data based on action
	switch transaction.Action {
	case "delete":
		// For delete actions, data is just the object ID
		if objectID, ok := transaction.Data.(string); ok {
			transaction.ParsedObjectID = objectID
		} else {
			return nil, errors.New("invalid delete data format")
		}
	case "create", "update":
		// For create/update actions, data is the object data
		if dataMap, ok := transaction.Data.(map[string]any); ok {
			transaction.ParsedData = dataMap
		} else {
			return nil, errors.New("invalid create/update data format")
		}
	default:
		return nil, fmt.Errorf("unknown action: %s", transaction.Action)
	}

	// Parse principals
	principals, err := s.authRepo.ParsePrincipals(ctx, transaction.Headers)
	if err != nil {
		return nil, fmt.Errorf("failed to parse principals: %w", err)
	}
	transaction.ParsedPrincipals = principals

	// Set timestamp
	transaction.Timestamp = time.Now()

	return &transaction, nil
}

// ParseV1Transaction parses a V1 transaction from message data
func (s *TransactionService) ParseV1Transaction(ctx context.Context, data []byte, subject string) (*entities.LFXTransaction, error) {
	var transaction entities.LFXTransaction
	if err := json.Unmarshal(data, &transaction); err != nil {
		return nil, fmt.Errorf("failed to unmarshal V1 transaction: %w", err)
	}

	// Extract object type from subject
	parts := strings.Split(subject, ".")
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid V1 subject format: %s", subject)
	}
	transaction.ObjectType = parts[3]

	// For V1 transactions, parse differently
	if transaction.V1Data != nil {
		transaction.ParsedData = transaction.V1Data
	}

	// Parse V1 principals
	transaction.ParsedPrincipals = s.parseV1Principals(&transaction)

	// Set timestamp
	transaction.Timestamp = time.Now()

	return &transaction, nil
}

// GenerateIndexBody generates the OpenSearch index body for a transaction
func (s *TransactionService) GenerateIndexBody(ctx context.Context, transaction *entities.LFXTransaction) (string, io.Reader, error) {
	body := &entities.TransactionBody{
		ObjectType: transaction.ObjectType,
		Data:       transaction.ParsedData,
		V1Data:     transaction.V1Data,
	}

	// Set common fields based on action
	switch transaction.Action {
	case "create":
		body.CreatedAt = toTimePointer(transaction.Timestamp)
		body.Latest = toBoolPointer(true)
		s.setPrincipalFields(body, transaction.ParsedPrincipals, "created")
	case "update":
		body.UpdatedAt = toTimePointer(transaction.Timestamp)
		body.Latest = toBoolPointer(true)
		s.setPrincipalFields(body, transaction.ParsedPrincipals, "updated")
	case "delete":
		body.DeletedAt = toTimePointer(transaction.Timestamp)
		body.Latest = toBoolPointer(false)
		s.setPrincipalFields(body, transaction.ParsedPrincipals, "deleted")
	}

	// Enrich based on object type
	if err := s.enrichTransaction(ctx, body); err != nil {
		return "", nil, fmt.Errorf("failed to enrich transaction: %w", err)
	}

	// Generate document ID
	docID := s.generateDocumentID(body)

	// Marshal to JSON
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal transaction body: %w", err)
	}

	return docID, strings.NewReader(string(bodyBytes)), nil
}

// ProcessTransaction processes a complete transaction
func (s *TransactionService) ProcessTransaction(ctx context.Context, transaction *entities.LFXTransaction, index string) (*valueobjects.ProcessingResult, error) {
	startTime := time.Now()
	result := &valueobjects.ProcessingResult{
		ProcessedAt: startTime,
		MessageID:   s.generateMessageID(transaction),
	}

	// Generate index body
	docID, body, err := s.GenerateIndexBody(ctx, transaction)
	if err != nil {
		result.Success = false
		result.Error = err
		result.Duration = time.Since(startTime)
		return result, err
	}

	result.DocumentID = docID

	// Index the document
	if err := s.transactionRepo.Index(ctx, index, docID, body); err != nil {
		result.Success = false
		result.Error = err
		result.Duration = time.Since(startTime)
		return result, err
	}

	result.Success = true
	result.IndexSuccess = true
	result.Duration = time.Since(startTime)

	return result, nil
}

// enrichTransaction enriches a transaction based on its object type
func (s *TransactionService) enrichTransaction(ctx context.Context, body *entities.TransactionBody) error {
	switch body.ObjectType {
	case "project":
		return s.enrichProject(body)
	default:
		return fmt.Errorf("unsupported object type for enrichment: %s", body.ObjectType)
	}
}

// enrichProject enriches a project transaction
func (s *TransactionService) enrichProject(body *entities.TransactionBody) error {
	// Extract resource name.
	var ok bool
	if body.ObjectID, ok = body.Data["uid"].(string); !ok {
		return errors.New("error mapping `uid` to `object_id`")
	}

	// Access control attributes.
	if body.Public, ok = body.Data["public"].(bool); !ok {
		return errors.New("error mapping `public` attribute")
	}
	body.AccessCheckObject = fmt.Sprintf("project:%s", body.ObjectID)
	body.AccessCheckRelation = "viewer"
	body.HistoryCheckObject = body.AccessCheckObject
	body.HistoryCheckRelation = "writer"

	// Additional enrichment.
	if parentUID, ok := body.Data["parent_uid"].(string); ok && parentUID != "" {
		body.ParentRefs = append(body.ParentRefs, "project:"+parentUID)
	}
	if name, ok := body.Data["name"].(string); ok && name != "" {
		body.SortName = name
		body.NameAndAliases = append(body.NameAndAliases, name)
	}
	if slug, ok := body.Data["slug"].(string); ok && slug != "" {
		body.NameAndAliases = append(body.NameAndAliases, slug)
	}
	if description, ok := body.Data["description"].(string); ok && description != "" {
		body.Fulltext = description
	}

	return nil
}

// Helper functions
func toBoolPointer(b bool) *bool {
	return &b
}

func toTimePointer(t time.Time) *time.Time {
	return &t
}

func (s *TransactionService) setPrincipalFields(body *entities.TransactionBody, principals []entities.Principal, action string) {
	var principalsList []string
	var emailsList []string

	for _, p := range principals {
		principalsList = append(principalsList, p.Principal)
		if p.Email != "" {
			emailsList = append(emailsList, p.Email)
		}
	}

	switch action {
	case "created":
		body.CreatedByPrincipals = principalsList
		body.CreatedByEmails = emailsList
	case "updated":
		body.UpdatedByPrincipals = principalsList
		body.UpdatedByEmails = emailsList
	case "deleted":
		body.DeletedByPrincipals = principalsList
		body.DeletedByEmails = emailsList
	}
}

func (s *TransactionService) generateDocumentID(body *entities.TransactionBody) string {
	return fmt.Sprintf("%s:%s", body.ObjectType, body.ObjectID)
}

func (s *TransactionService) generateMessageID(transaction *entities.LFXTransaction) string {
	return fmt.Sprintf("%s:%s:%d", transaction.ObjectType, transaction.Action, transaction.Timestamp.UnixNano())
}

func (s *TransactionService) parseV1Principals(transaction *entities.LFXTransaction) []entities.Principal {
	var principals []entities.Principal

	// Extract principals from V1 data format
	if transaction.V1Data != nil {
		if userID, ok := transaction.V1Data["user_id"].(string); ok && userID != "" {
			principal := entities.Principal{
				Principal: userID,
			}
			if email, ok := transaction.V1Data["user_email"].(string); ok {
				principal.Email = email
			}
			principals = append(principals, principal)
		}
	}

	return principals
}
