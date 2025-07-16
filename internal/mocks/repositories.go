package mocks

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
)

// MockTransactionRepository implements the repositories.TransactionRepository interface for testing
type MockTransactionRepository struct {
	mu sync.RWMutex

	// Mock state
	IndexedDocuments map[string]map[string]interface{}

	// Mock responses
	SearchResults map[string][]map[string]any
	SearchError   error
	IndexError    error
	UpdateError   error
	DeleteError   error
	BulkError     error
	HealthError   error

	// Call tracking
	IndexCalls  []IndexCall
	SearchCalls []SearchCall
	UpdateCalls []UpdateCall
	DeleteCalls []DeleteCall
	BulkCalls   []BulkCall

	// Mock responses for new methods
	SearchWithVersionsResults     map[string][]repositories.VersionedDocument
	SearchWithVersionsError       error
	UpdateWithOptimisticLockError error

	// Call tracking for new methods
	SearchWithVersionsCalls       []SearchWithVersionsCall
	UpdateWithOptimisticLockCalls []UpdateWithOptimisticLockCall
}

// Call tracking structures
type IndexCall struct {
	Index string
	DocID string
	Body  string
}

type SearchCall struct {
	Index string
	Query map[string]any
}

type UpdateCall struct {
	Index string
	DocID string
	Body  string
}

type DeleteCall struct {
	Index string
	DocID string
}

type BulkCall struct {
	Operations []repositories.BulkOperation
}

type SearchWithVersionsCall struct {
	Index string
	Query map[string]any
}

type UpdateWithOptimisticLockCall struct {
	Index  string
	DocID  string
	Body   string
	Params *repositories.OptimisticUpdateParams
}

// NewMockTransactionRepository creates a new mock transaction repository
func NewMockTransactionRepository() *MockTransactionRepository {
	return &MockTransactionRepository{
		IndexedDocuments:              make(map[string]map[string]interface{}),
		SearchResults:                 make(map[string][]map[string]any),
		SearchWithVersionsResults:     make(map[string][]repositories.VersionedDocument),
		IndexCalls:                    make([]IndexCall, 0),
		SearchCalls:                   make([]SearchCall, 0),
		UpdateCalls:                   make([]UpdateCall, 0),
		DeleteCalls:                   make([]DeleteCall, 0),
		BulkCalls:                     make([]BulkCall, 0),
		SearchWithVersionsCalls:       make([]SearchWithVersionsCall, 0),
		UpdateWithOptimisticLockCalls: make([]UpdateWithOptimisticLockCall, 0),
	}
}

// Index mocks indexing a document
func (m *MockTransactionRepository) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.IndexError != nil {
		return m.IndexError
	}

	// Read the body
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	// Track the call
	m.IndexCalls = append(m.IndexCalls, IndexCall{
		Index: index,
		DocID: docID,
		Body:  string(bodyBytes),
	})

	// Store the document
	if m.IndexedDocuments[index] == nil {
		m.IndexedDocuments[index] = make(map[string]interface{})
	}
	m.IndexedDocuments[index][docID] = string(bodyBytes)

	return nil
}

// Search mocks searching for documents
func (m *MockTransactionRepository) Search(ctx context.Context, index string, query map[string]any) ([]map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SearchError != nil {
		return nil, m.SearchError
	}

	// Track the call
	m.SearchCalls = append(m.SearchCalls, SearchCall{
		Index: index,
		Query: query,
	})

	// Return pre-configured results
	queryHash := m.hashQuery(query)
	if results, exists := m.SearchResults[queryHash]; exists {
		return results, nil
	}

	// Return empty results if no pre-configured results
	return []map[string]any{}, nil
}

// SearchWithVersions mocks searching for documents with version information
func (m *MockTransactionRepository) SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]repositories.VersionedDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SearchWithVersionsError != nil {
		return nil, m.SearchWithVersionsError
	}

	// Track the call
	m.SearchWithVersionsCalls = append(m.SearchWithVersionsCalls, SearchWithVersionsCall{
		Index: index,
		Query: query,
	})

	// Return pre-configured results
	queryHash := m.hashQuery(query)
	if results, exists := m.SearchWithVersionsResults[queryHash]; exists {
		return results, nil
	}

	// Return empty results if no pre-configured results
	return []repositories.VersionedDocument{}, nil
}

// Update mocks updating a document
func (m *MockTransactionRepository) Update(ctx context.Context, index string, docID string, body io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.UpdateError != nil {
		return m.UpdateError
	}

	// Read the body
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	// Track the call
	m.UpdateCalls = append(m.UpdateCalls, UpdateCall{
		Index: index,
		DocID: docID,
		Body:  string(bodyBytes),
	})

	return nil
}

// UpdateWithOptimisticLock mocks updating a document with optimistic concurrency control
func (m *MockTransactionRepository) UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *repositories.OptimisticUpdateParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.UpdateWithOptimisticLockError != nil {
		return m.UpdateWithOptimisticLockError
	}

	// Read the body
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	// Track the call
	m.UpdateWithOptimisticLockCalls = append(m.UpdateWithOptimisticLockCalls, UpdateWithOptimisticLockCall{
		Index:  index,
		DocID:  docID,
		Body:   string(bodyBytes),
		Params: params,
	})

	return nil
}

// Delete mocks deleting a document
func (m *MockTransactionRepository) Delete(ctx context.Context, index string, docID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.DeleteError != nil {
		return m.DeleteError
	}

	// Track the call
	m.DeleteCalls = append(m.DeleteCalls, DeleteCall{
		Index: index,
		DocID: docID,
	})

	return nil
}

// BulkIndex mocks bulk indexing operations
func (m *MockTransactionRepository) BulkIndex(ctx context.Context, operations []repositories.BulkOperation) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.BulkError != nil {
		return m.BulkError
	}

	// Track the call
	m.BulkCalls = append(m.BulkCalls, BulkCall{
		Operations: operations,
	})

	return nil
}

// HealthCheck mocks health checking
func (m *MockTransactionRepository) HealthCheck(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.HealthError
}

// Helper methods for testing

// SetSearchResult sets a search result for a specific query
func (m *MockTransactionRepository) SetSearchResult(query map[string]any, result []map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	queryHash := m.hashQuery(query)
	m.SearchResults[queryHash] = result
}

// SetSearchWithVersionsResult sets a search result for a specific query with version information
func (m *MockTransactionRepository) SetSearchWithVersionsResult(query map[string]any, result []repositories.VersionedDocument) {
	m.mu.Lock()
	defer m.mu.Unlock()

	queryHash := m.hashQuery(query)
	m.SearchWithVersionsResults[queryHash] = result
}

// GetIndexCalls returns all index calls
func (m *MockTransactionRepository) GetIndexCalls() []IndexCall {
	m.mu.RLock()
	defer m.mu.RUnlock()

	calls := make([]IndexCall, len(m.IndexCalls))
	copy(calls, m.IndexCalls)
	return calls
}

// GetSearchCalls returns all search calls
func (m *MockTransactionRepository) GetSearchCalls() []SearchCall {
	m.mu.RLock()
	defer m.mu.RUnlock()

	calls := make([]SearchCall, len(m.SearchCalls))
	copy(calls, m.SearchCalls)
	return calls
}

// GetSearchWithVersionsCalls returns all search with versions calls
func (m *MockTransactionRepository) GetSearchWithVersionsCalls() []SearchWithVersionsCall {
	m.mu.RLock()
	defer m.mu.RUnlock()

	calls := make([]SearchWithVersionsCall, len(m.SearchWithVersionsCalls))
	copy(calls, m.SearchWithVersionsCalls)
	return calls
}

// GetUpdateWithOptimisticLockCalls returns all optimistic update calls
func (m *MockTransactionRepository) GetUpdateWithOptimisticLockCalls() []UpdateWithOptimisticLockCall {
	m.mu.RLock()
	defer m.mu.RUnlock()

	calls := make([]UpdateWithOptimisticLockCall, len(m.UpdateWithOptimisticLockCalls))
	copy(calls, m.UpdateWithOptimisticLockCalls)
	return calls
}

// Reset clears all call tracking and state
func (m *MockTransactionRepository) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.IndexedDocuments = make(map[string]map[string]interface{})
	m.SearchResults = make(map[string][]map[string]any)
	m.SearchWithVersionsResults = make(map[string][]repositories.VersionedDocument)

	m.SearchError = nil
	m.IndexError = nil
	m.UpdateError = nil
	m.DeleteError = nil
	m.BulkError = nil
	m.HealthError = nil
	m.SearchWithVersionsError = nil
	m.UpdateWithOptimisticLockError = nil

	m.IndexCalls = make([]IndexCall, 0)
	m.SearchCalls = make([]SearchCall, 0)
	m.UpdateCalls = make([]UpdateCall, 0)
	m.DeleteCalls = make([]DeleteCall, 0)
	m.BulkCalls = make([]BulkCall, 0)
	m.SearchWithVersionsCalls = make([]SearchWithVersionsCall, 0)
	m.UpdateWithOptimisticLockCalls = make([]UpdateWithOptimisticLockCall, 0)
}

// hashQuery creates a hash of the query for lookup
func (m *MockTransactionRepository) hashQuery(query map[string]any) string {
	queryBytes, _ := json.Marshal(query)
	return fmt.Sprintf("%x", md5.Sum(queryBytes))
}

// MockMessageRepository implements the MessageRepository interface for testing
type MockMessageRepository struct {
	mu sync.RWMutex

	// Mock data
	PublishedMessages map[string][]PublishedMessage // subject -> messages
	Subscriptions     map[string]repositories.MessageHandler

	// Mock behavior
	SubscribeError error
	PublishError   error
	CloseError     error
	HealthError    error

	// Call tracking
	SubscribeCalls []SubscribeCall
	PublishCalls   []PublishCall
	CloseCalls     int
	HealthCalls    int
}

type PublishedMessage struct {
	Data []byte
}

type SubscribeCall struct {
	Subject string
	Handler repositories.MessageHandler
}

type PublishCall struct {
	Subject string
	Data    []byte
}

// NewMockMessageRepository creates a new mock message repository
func NewMockMessageRepository() *MockMessageRepository {
	return &MockMessageRepository{
		PublishedMessages: make(map[string][]PublishedMessage),
		Subscriptions:     make(map[string]repositories.MessageHandler),
		SubscribeCalls:    make([]SubscribeCall, 0),
		PublishCalls:      make([]PublishCall, 0),
	}
}

// Subscribe mocks subscribing to messages
func (m *MockMessageRepository) Subscribe(ctx context.Context, subject string, handler repositories.MessageHandler) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SubscribeError != nil {
		return m.SubscribeError
	}

	// Track the call
	m.SubscribeCalls = append(m.SubscribeCalls, SubscribeCall{
		Subject: subject,
		Handler: handler,
	})

	// Store the subscription
	m.Subscriptions[subject] = handler

	return nil
}

// Publish mocks publishing a message
func (m *MockMessageRepository) Publish(ctx context.Context, subject string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.PublishError != nil {
		return m.PublishError
	}

	// Track the call
	m.PublishCalls = append(m.PublishCalls, PublishCall{
		Subject: subject,
		Data:    data,
	})

	// Store the message
	m.PublishedMessages[subject] = append(m.PublishedMessages[subject], PublishedMessage{
		Data: data,
	})

	return nil
}

// Close mocks closing the connection
func (m *MockMessageRepository) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CloseCalls++

	return m.CloseError
}

// HealthCheck mocks health check
func (m *MockMessageRepository) HealthCheck(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.HealthCalls++

	return m.HealthError
}

// Helper methods for testing
func (m *MockMessageRepository) SimulateMessage(ctx context.Context, subject string, data []byte) error {
	m.mu.RLock()
	handler, exists := m.Subscriptions[subject]
	m.mu.RUnlock()

	if !exists {
		return nil // No handler subscribed
	}

	return handler.Handle(ctx, data, subject)
}

func (m *MockMessageRepository) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.PublishedMessages = make(map[string][]PublishedMessage)
	m.Subscriptions = make(map[string]repositories.MessageHandler)
	m.SubscribeCalls = make([]SubscribeCall, 0)
	m.PublishCalls = make([]PublishCall, 0)
	m.CloseCalls = 0
	m.HealthCalls = 0
	m.SubscribeError = nil
	m.PublishError = nil
	m.CloseError = nil
	m.HealthError = nil
}

// MockAuthRepository provides a mock implementation of AuthRepository
type MockAuthRepository struct {
	ValidTokens          map[string]*entities.Principal
	ParsePrincipalsError error

	// Call tracking
	ParsePrincipalsCalls []ParsePrincipalsCall
	mu                   sync.Mutex
}

type ParsePrincipalsCall struct {
	Headers map[string]string
}

// NewMockAuthRepository creates a new mock auth repository
func NewMockAuthRepository() *MockAuthRepository {
	return &MockAuthRepository{
		ValidTokens:          make(map[string]*entities.Principal),
		ParsePrincipalsError: nil,
		ParsePrincipalsCalls: make([]ParsePrincipalsCall, 0),
	}
}

// ValidateToken validates a JWT token and returns principal information
func (m *MockAuthRepository) ValidateToken(ctx context.Context, token string) (*entities.Principal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for validation errors first
	if m.ParsePrincipalsError != nil {
		return nil, m.ParsePrincipalsError
	}

	// Return pre-configured principal
	if principal, exists := m.ValidTokens[token]; exists {
		return principal, nil
	}

	// Return default principal
	return &entities.Principal{
		Principal: "test_user",
		Email:     "test@example.com",
	}, nil
}

// ParsePrincipals mocks parsing principals from headers with delegation support
func (m *MockAuthRepository) ParsePrincipals(ctx context.Context, headers map[string]string) ([]entities.Principal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ParsePrincipalsError != nil {
		return nil, m.ParsePrincipalsError
	}

	// Track the call
	m.ParsePrincipalsCalls = append(m.ParsePrincipalsCalls, ParsePrincipalsCall{
		Headers: headers,
	})

	// Return simple list of principals (deployment pattern)
	principal := entities.Principal{
		Principal: "test_user",
		Email:     "test@example.com",
	}

	return []entities.Principal{principal}, nil
}

// Test helper methods

// SetValidToken configures a valid token for testing
func (m *MockAuthRepository) SetValidToken(token string, principal *entities.Principal) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ValidTokens[token] = principal
}

// Reset clears all mock data
func (m *MockAuthRepository) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ValidTokens = make(map[string]*entities.Principal)
	m.ParsePrincipalsCalls = make([]ParsePrincipalsCall, 0)
	m.ParsePrincipalsError = nil
}

// SetParseError configures the mock to return an error
func (m *MockAuthRepository) SetParseError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ParsePrincipalsError = err
}
