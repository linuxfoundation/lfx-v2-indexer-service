package mocks

import (
	"context"
	"io"
	"sync"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
)

// MockTransactionRepository implements the TransactionRepository interface for testing
type MockTransactionRepository struct {
	mu sync.RWMutex

	// Mock data
	IndexedDocuments map[string]map[string]interface{} // index -> docID -> document
	SearchResults    map[string][]map[string]any       // query hash -> results

	// Mock behavior
	IndexError     error
	SearchError    error
	UpdateError    error
	DeleteError    error
	BulkIndexError error
	HealthError    error

	// Call tracking
	IndexCalls     []IndexCall
	SearchCalls    []SearchCall
	UpdateCalls    []UpdateCall
	DeleteCalls    []DeleteCall
	BulkIndexCalls []BulkIndexCall
	HealthCalls    int
}

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

type BulkIndexCall struct {
	Operations []repositories.BulkOperation
}

// NewMockTransactionRepository creates a new mock transaction repository
func NewMockTransactionRepository() *MockTransactionRepository {
	return &MockTransactionRepository{
		IndexedDocuments: make(map[string]map[string]interface{}),
		SearchResults:    make(map[string][]map[string]any),
		IndexCalls:       make([]IndexCall, 0),
		SearchCalls:      make([]SearchCall, 0),
		UpdateCalls:      make([]UpdateCall, 0),
		DeleteCalls:      make([]DeleteCall, 0),
		BulkIndexCalls:   make([]BulkIndexCall, 0),
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

	// Remove from indexed documents
	if docs, exists := m.IndexedDocuments[index]; exists {
		delete(docs, docID)
	}

	return nil
}

// BulkIndex mocks bulk indexing operations
func (m *MockTransactionRepository) BulkIndex(ctx context.Context, operations []repositories.BulkOperation) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.BulkIndexError != nil {
		return m.BulkIndexError
	}

	// Track the call
	m.BulkIndexCalls = append(m.BulkIndexCalls, BulkIndexCall{
		Operations: operations,
	})

	return nil
}

// HealthCheck mocks health check
func (m *MockTransactionRepository) HealthCheck(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.HealthCalls++

	return m.HealthError
}

// Helper methods for testing
func (m *MockTransactionRepository) SetSearchResults(query map[string]any, results []map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	queryHash := m.hashQuery(query)
	m.SearchResults[queryHash] = results
}

func (m *MockTransactionRepository) GetIndexedDocument(index, docID string) (interface{}, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if docs, exists := m.IndexedDocuments[index]; exists {
		if doc, exists := docs[docID]; exists {
			return doc, true
		}
	}
	return nil, false
}

func (m *MockTransactionRepository) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.IndexedDocuments = make(map[string]map[string]interface{})
	m.SearchResults = make(map[string][]map[string]any)
	m.IndexCalls = make([]IndexCall, 0)
	m.SearchCalls = make([]SearchCall, 0)
	m.UpdateCalls = make([]UpdateCall, 0)
	m.DeleteCalls = make([]DeleteCall, 0)
	m.BulkIndexCalls = make([]BulkIndexCall, 0)
	m.HealthCalls = 0
	m.IndexError = nil
	m.SearchError = nil
	m.UpdateError = nil
	m.DeleteError = nil
	m.BulkIndexError = nil
	m.HealthError = nil
}

func (m *MockTransactionRepository) hashQuery(query map[string]any) string {
	// Simple hash for query - in real implementation, would use proper hashing
	return "query_hash"
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

// MockAuthRepository implements the AuthRepository interface for testing
type MockAuthRepository struct {
	mu sync.RWMutex

	// Mock data
	ValidTokens map[string]*entities.Principal

	// Mock behavior
	ValidateError        error
	ParsePrincipalsError error
	HealthError          error

	// Call tracking
	ValidateCalls        []ValidateCall
	ParsePrincipalsCalls []ParsePrincipalsCall
	HealthCalls          int
}

type ValidateCall struct {
	Token string
}

type ParsePrincipalsCall struct {
	Headers map[string]string
}

// NewMockAuthRepository creates a new mock auth repository
func NewMockAuthRepository() *MockAuthRepository {
	return &MockAuthRepository{
		ValidTokens:          make(map[string]*entities.Principal),
		ValidateCalls:        make([]ValidateCall, 0),
		ParsePrincipalsCalls: make([]ParsePrincipalsCall, 0),
	}
}

// ValidateToken mocks token validation
func (m *MockAuthRepository) ValidateToken(ctx context.Context, token string) (*entities.Principal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ValidateError != nil {
		return nil, m.ValidateError
	}

	// Track the call
	m.ValidateCalls = append(m.ValidateCalls, ValidateCall{
		Token: token,
	})

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

// ParsePrincipals mocks parsing principals from headers
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

	// Return default principals
	return []entities.Principal{
		{
			Principal: "test_user",
			Email:     "test@example.com",
		},
	}, nil
}

// HealthCheck mocks health check
func (m *MockAuthRepository) HealthCheck(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.HealthCalls++

	return m.HealthError
}

// Helper methods for testing
func (m *MockAuthRepository) SetValidToken(token string, principal *entities.Principal) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ValidTokens[token] = principal
}

func (m *MockAuthRepository) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ValidTokens = make(map[string]*entities.Principal)
	m.ValidateCalls = make([]ValidateCall, 0)
	m.ParsePrincipalsCalls = make([]ParsePrincipalsCall, 0)
	m.HealthCalls = 0
	m.ValidateError = nil
	m.ParsePrincipalsError = nil
	m.HealthError = nil
}
