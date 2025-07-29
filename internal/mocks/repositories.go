// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package mocks provides mock implementations of repository interfaces for testing.
package mocks

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// MockStorageRepository implements the contracts.StorageRepository interface for testing
type MockStorageRepository struct {
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
	SearchWithVersionsResults     map[string][]contracts.VersionedDocument
	SearchWithVersionsError       error
	UpdateWithOptimisticLockError error

	// Call tracking for new methods
	SearchWithVersionsCalls       []SearchWithVersionsCall
	UpdateWithOptimisticLockCalls []UpdateWithOptimisticLockCall
}

// IndexCall tracks calls to the Index method with its parameters
type IndexCall struct {
	Index string
	DocID string
	Body  string
}

// SearchCall represents a call to the Search method with its parameters
type SearchCall struct {
	Index string
	Query map[string]any
}

// UpdateCall represents a call to the Update method with its parameters
type UpdateCall struct {
	Index string
	DocID string
	Body  string
}

// DeleteCall represents a call to the Delete method with its parameters
type DeleteCall struct {
	Index string
	DocID string
}

// BulkCall represents a call to the Bulk method with its parameters
type BulkCall struct {
	Operations []contracts.BulkOperation
}

// SearchWithVersionsCall represents a call to the SearchWithVersions method with its parameters
type SearchWithVersionsCall struct {
	Index string
	Query map[string]any
}

// UpdateWithOptimisticLockCall represents a call to the UpdateWithOptimisticLock method with its parameters
type UpdateWithOptimisticLockCall struct {
	Index  string
	DocID  string
	Body   string
	Params *contracts.OptimisticUpdateParams
}

// NewMockStorageRepository creates a new mock storage repository
func NewMockStorageRepository() *MockStorageRepository {
	return &MockStorageRepository{
		IndexedDocuments:              make(map[string]map[string]interface{}),
		SearchResults:                 make(map[string][]map[string]any),
		SearchWithVersionsResults:     make(map[string][]contracts.VersionedDocument),
		IndexCalls:                    make([]IndexCall, 0),
		SearchCalls:                   make([]SearchCall, 0),
		UpdateCalls:                   make([]UpdateCall, 0),
		DeleteCalls:                   make([]DeleteCall, 0),
		BulkCalls:                     make([]BulkCall, 0),
		SearchWithVersionsCalls:       make([]SearchWithVersionsCall, 0),
		UpdateWithOptimisticLockCalls: make([]UpdateWithOptimisticLockCall, 0),
	}
}

// NewMockTransactionRepository creates a new mock storage repository (backward compatibility)
func NewMockTransactionRepository() *MockStorageRepository {
	return NewMockStorageRepository()
}

// Index mocks indexing a document
func (m *MockStorageRepository) Index(_ context.Context, index string, docID string, body io.Reader) error {
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
func (m *MockStorageRepository) Search(_ context.Context, index string, query map[string]any) ([]map[string]any, error) {
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

	// Default empty results
	return []map[string]any{}, nil
}

// SearchWithVersions mocks searching for documents with version tracking
func (m *MockStorageRepository) SearchWithVersions(_ context.Context, index string, query map[string]any) ([]contracts.VersionedDocument, error) {
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

	// Default empty results
	return []contracts.VersionedDocument{}, nil
}

// Update mocks updating a document
func (m *MockStorageRepository) Update(_ context.Context, index string, docID string, body io.Reader) error {
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

	// Update the document
	if m.IndexedDocuments[index] == nil {
		m.IndexedDocuments[index] = make(map[string]interface{})
	}
	m.IndexedDocuments[index][docID] = string(bodyBytes)

	return nil
}

// Delete mocks deleting a document
func (m *MockStorageRepository) Delete(_ context.Context, index string, docID string) error {
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

	// Delete the document
	if m.IndexedDocuments[index] != nil {
		delete(m.IndexedDocuments[index], docID)
	}

	return nil
}

// BulkIndex mocks bulk indexing operations
func (m *MockStorageRepository) BulkIndex(_ context.Context, operations []contracts.BulkOperation) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.BulkError != nil {
		return m.BulkError
	}

	// Track the call
	m.BulkCalls = append(m.BulkCalls, BulkCall{
		Operations: operations,
	})

	// Process each operation
	for _, op := range operations {
		if op.Action != "delete" && op.Body != nil {
			bodyBytes, err := io.ReadAll(op.Body)
			if err != nil {
				return err
			}

			if m.IndexedDocuments[op.Index] == nil {
				m.IndexedDocuments[op.Index] = make(map[string]interface{})
			}
			m.IndexedDocuments[op.Index][op.DocID] = string(bodyBytes)
		} else if op.Action == "delete" {
			if m.IndexedDocuments[op.Index] != nil {
				delete(m.IndexedDocuments[op.Index], op.DocID)
			}
		}
	}

	return nil
}

// UpdateWithOptimisticLock mocks updating a document with optimistic concurrency control
func (m *MockStorageRepository) UpdateWithOptimisticLock(_ context.Context, index, docID string, body io.Reader, params *contracts.OptimisticUpdateParams) error {
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

	// Update the document
	if m.IndexedDocuments[index] == nil {
		m.IndexedDocuments[index] = make(map[string]interface{})
	}
	m.IndexedDocuments[index][docID] = string(bodyBytes)

	return nil
}

// HealthCheck mocks a health check
func (m *MockStorageRepository) HealthCheck(_ context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.HealthError
}

// Helper methods
func (m *MockStorageRepository) hashQuery(query map[string]any) string {
	jsonBytes, err := json.Marshal(query)
	if err != nil {
		// Fallback to string representation if marshal fails
		return fmt.Sprintf("%v", query)
	}
	hash := sha256.Sum256(jsonBytes)
	return fmt.Sprintf("%x", hash)
}

// SetSearchResults sets predefined search results for a given query in the mock
func (m *MockStorageRepository) SetSearchResults(query map[string]any, results []map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	queryHash := m.hashQuery(query)
	m.SearchResults[queryHash] = results
}

// SetSearchWithVersionsResults sets predefined search results with versions for a given query in the mock
func (m *MockStorageRepository) SetSearchWithVersionsResults(query map[string]any, results []contracts.VersionedDocument) {
	m.mu.Lock()
	defer m.mu.Unlock()
	queryHash := m.hashQuery(query)
	m.SearchWithVersionsResults[queryHash] = results
}

// GetIndexedDocument retrieves an indexed document by index and document ID from the mock storage
func (m *MockStorageRepository) GetIndexedDocument(index, docID string) (interface{}, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if indexDocs, exists := m.IndexedDocuments[index]; exists {
		doc, exists := indexDocs[docID]
		return doc, exists
	}
	return nil, false
}

// ClearCalls clears all recorded method calls in the mock storage repository
func (m *MockStorageRepository) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.IndexCalls = make([]IndexCall, 0)
	m.SearchCalls = make([]SearchCall, 0)
	m.UpdateCalls = make([]UpdateCall, 0)
	m.DeleteCalls = make([]DeleteCall, 0)
	m.BulkCalls = make([]BulkCall, 0)
	m.SearchWithVersionsCalls = make([]SearchWithVersionsCall, 0)
	m.UpdateWithOptimisticLockCalls = make([]UpdateWithOptimisticLockCall, 0)
}

// MockMessagingRepository implements the contracts.MessagingRepository interface for testing
type MockMessagingRepository struct {
	mu sync.RWMutex

	// Mock state
	PublishedMessages  map[string][]MockMessage
	Subscriptions      map[string]contracts.MessageHandler
	QueueSubs          map[string]map[string]contracts.MessageHandler
	QueueSubsWithReply map[string]map[string]contracts.MessageHandlerWithReply

	// Mock responses
	PublishError   error
	SubscribeError error
	HealthError    error
	DrainError     error
	CloseError     error

	// Auth delegation (embedded AuthRepository)
	AuthRepo *MockAuthRepository

	// Call tracking
	PublishCalls       []PublishCall
	SubscribeCalls     []SubscribeCall
	QueueSubCalls      []QueueSubscribeCall
	QueueReplySubCalls []QueueSubscribeWithReplyCall
}

// MockMessage represents a message used in messaging repository mocks for testing
type MockMessage struct {
	Subject string
	Data    []byte
}

// PublishCall represents a call to the Publish method with its parameters
type PublishCall struct {
	Subject string
	Data    []byte
}

// SubscribeCall represents a call to the Subscribe method with its parameters
type SubscribeCall struct {
	Subject string
}

// QueueSubscribeCall represents a call to the QueueSubscribe method with its parameters
type QueueSubscribeCall struct {
	Subject string
	Queue   string
}

// QueueSubscribeWithReplyCall represents a call to the QueueSubscribeWithReply method with its parameters
type QueueSubscribeWithReplyCall struct {
	Subject string
	Queue   string
}

// NewMockMessagingRepository creates a new mock messaging repository
func NewMockMessagingRepository() *MockMessagingRepository {
	return &MockMessagingRepository{
		PublishedMessages:  make(map[string][]MockMessage),
		Subscriptions:      make(map[string]contracts.MessageHandler),
		QueueSubs:          make(map[string]map[string]contracts.MessageHandler),
		QueueSubsWithReply: make(map[string]map[string]contracts.MessageHandlerWithReply),
		AuthRepo:           NewMockAuthRepository(),
		PublishCalls:       make([]PublishCall, 0),
		SubscribeCalls:     make([]SubscribeCall, 0),
		QueueSubCalls:      make([]QueueSubscribeCall, 0),
		QueueReplySubCalls: make([]QueueSubscribeWithReplyCall, 0),
	}
}

// NewMockMessageRepository creates a new mock messaging repository (backward compatibility)
func NewMockMessageRepository() *MockMessagingRepository {
	return NewMockMessagingRepository()
}

// Subscribe mocks subscribing to messages
func (m *MockMessagingRepository) Subscribe(_ context.Context, subject string, handler contracts.MessageHandler) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SubscribeError != nil {
		return m.SubscribeError
	}

	m.SubscribeCalls = append(m.SubscribeCalls, SubscribeCall{Subject: subject})
	m.Subscriptions[subject] = handler
	return nil
}

// QueueSubscribe mocks queue subscribing to messages
func (m *MockMessagingRepository) QueueSubscribe(_ context.Context, subject string, queue string, handler contracts.MessageHandler) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SubscribeError != nil {
		return m.SubscribeError
	}

	m.QueueSubCalls = append(m.QueueSubCalls, QueueSubscribeCall{Subject: subject, Queue: queue})

	if m.QueueSubs[subject] == nil {
		m.QueueSubs[subject] = make(map[string]contracts.MessageHandler)
	}
	m.QueueSubs[subject][queue] = handler
	return nil
}

// QueueSubscribeWithReply mocks queue subscribing to messages with reply support
func (m *MockMessagingRepository) QueueSubscribeWithReply(_ context.Context, subject string, queue string, handler contracts.MessageHandlerWithReply) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SubscribeError != nil {
		return m.SubscribeError
	}

	m.QueueReplySubCalls = append(m.QueueReplySubCalls, QueueSubscribeWithReplyCall{Subject: subject, Queue: queue})

	if m.QueueSubsWithReply[subject] == nil {
		m.QueueSubsWithReply[subject] = make(map[string]contracts.MessageHandlerWithReply)
	}
	m.QueueSubsWithReply[subject][queue] = handler
	return nil
}

// Publish mocks publishing a message
func (m *MockMessagingRepository) Publish(_ context.Context, subject string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.PublishError != nil {
		return m.PublishError
	}

	m.PublishCalls = append(m.PublishCalls, PublishCall{Subject: subject, Data: data})

	if m.PublishedMessages[subject] == nil {
		m.PublishedMessages[subject] = make([]MockMessage, 0)
	}
	m.PublishedMessages[subject] = append(m.PublishedMessages[subject], MockMessage{
		Subject: subject,
		Data:    data,
	})
	return nil
}

// DrainWithTimeout mocks draining connections
func (m *MockMessagingRepository) DrainWithTimeout() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.DrainError
}

// Close mocks closing connections
func (m *MockMessagingRepository) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.CloseError
}

// HealthCheck mocks a health check
func (m *MockMessagingRepository) HealthCheck(_ context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.HealthError
}

// ValidateToken validates a JWT token using the embedded auth repository
func (m *MockMessagingRepository) ValidateToken(ctx context.Context, token string) (*contracts.Principal, error) {
	return m.AuthRepo.ValidateToken(ctx, token)
}

// ParsePrincipals parses principals from headers using the embedded auth repository
func (m *MockMessagingRepository) ParsePrincipals(ctx context.Context, headers map[string]string) ([]contracts.Principal, error) {
	return m.AuthRepo.ParsePrincipals(ctx, headers)
}

// GetPublishedMessages returns all published messages for a given subject from the mock
func (m *MockMessagingRepository) GetPublishedMessages(subject string) []MockMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.PublishedMessages[subject]
}

// SimulateMessage simulates receiving a message by triggering registered handlers
func (m *MockMessagingRepository) SimulateMessage(subject string, data []byte) error {
	m.mu.RLock()
	handler, exists := m.Subscriptions[subject]
	m.mu.RUnlock()

	if exists {
		return handler.Handle(context.Background(), data, subject)
	}
	return fmt.Errorf("no subscription for subject: %s", subject)
}

// ClearCalls clears all recorded method calls in the mock messaging repository
func (m *MockMessagingRepository) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PublishCalls = make([]PublishCall, 0)
	m.SubscribeCalls = make([]SubscribeCall, 0)
	m.QueueSubCalls = make([]QueueSubscribeCall, 0)
	m.QueueReplySubCalls = make([]QueueSubscribeWithReplyCall, 0)
}

// MockAuthRepository implements the contracts.AuthRepository interface for testing
type MockAuthRepository struct {
	mu sync.RWMutex

	// Mock responses
	ValidateTokenResponse   *contracts.Principal
	ValidateTokenError      error
	ParsePrincipalsResponse []contracts.Principal
	ParsePrincipalsError    error

	// Call tracking
	ValidateTokenCalls   []ValidateTokenCall
	ParsePrincipalsCalls []ParsePrincipalsCall
}

// ValidateTokenCall represents a call to the ValidateToken method with its parameters
type ValidateTokenCall struct {
	Token string
}

// ParsePrincipalsCall represents a call to the ParsePrincipals method with its parameters
type ParsePrincipalsCall struct {
	Headers map[string]string
}

// NewMockAuthRepository creates a new mock auth repository
func NewMockAuthRepository() *MockAuthRepository {
	return &MockAuthRepository{
		ValidateTokenCalls:   make([]ValidateTokenCall, 0),
		ParsePrincipalsCalls: make([]ParsePrincipalsCall, 0),
	}
}

// ValidateToken mocks validating a token
func (m *MockAuthRepository) ValidateToken(_ context.Context, token string) (*contracts.Principal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ValidateTokenCalls = append(m.ValidateTokenCalls, ValidateTokenCall{Token: token})

	if m.ValidateTokenError != nil {
		return nil, m.ValidateTokenError
	}

	if m.ValidateTokenResponse != nil {
		return m.ValidateTokenResponse, nil
	}

	// Default valid response
	return &contracts.Principal{
		Principal: "test_user",
		Email:     "test@example.com",
	}, nil
}

// ParsePrincipals mocks parsing principals from headers
func (m *MockAuthRepository) ParsePrincipals(_ context.Context, headers map[string]string) ([]contracts.Principal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ParsePrincipalsCalls = append(m.ParsePrincipalsCalls, ParsePrincipalsCall{Headers: headers})

	if m.ParsePrincipalsError != nil {
		return nil, m.ParsePrincipalsError
	}

	if m.ParsePrincipalsResponse != nil {
		return m.ParsePrincipalsResponse, nil
	}

	// Default valid response
	return []contracts.Principal{
		{
			Principal: "test_user",
			Email:     "test@example.com",
		},
	}, nil
}

// ClearCalls clears all recorded method calls in the mock auth repository
func (m *MockAuthRepository) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ValidateTokenCalls = make([]ValidateTokenCall, 0)
	m.ParsePrincipalsCalls = make([]ParsePrincipalsCall, 0)
}
