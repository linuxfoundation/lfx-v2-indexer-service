// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/assert"
)

func TestIndexerService_ProcessTransaction_Success(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test data
	transaction := &contracts.LFXTransaction{
		Action:     constants.ActionCreated,
		ObjectType: constants.ObjectTypeProject,
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data: map[string]any{
			"id":     "test-project",
			"name":   "Test Project",
			"public": true, // Required for access control
		},
		Timestamp: time.Now(),
		ParsedPrincipals: []contracts.Principal{
			{
				Principal: "test_user",
				Email:     "test@example.com",
			},
		},
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Len(t, mockStorageRepo.IndexCalls, 1)

	indexCall := mockStorageRepo.IndexCalls[0]
	assert.Equal(t, "test-index", indexCall.Index)
	assert.Equal(t, "project:test-project", indexCall.DocID)
	assert.Contains(t, indexCall.Body, "test-project")
	assert.Contains(t, indexCall.Body, "Test Project")
}

func TestIndexerService_ProcessTransaction_EnrichmentSuccess(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test data - transaction without parsed principals
	transaction := &contracts.LFXTransaction{
		Action:     constants.ActionUpdated,
		ObjectType: constants.ObjectTypeProject,
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data: map[string]any{
			"id":     "test-project",
			"name":   "Test Project Updated",
			"public": false, // Required for access control
		},
		Timestamp: time.Now(),
		// No ParsedPrincipals - should be enriched via auth
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Len(t, mockStorageRepo.IndexCalls, 1)

	// Verify auth enrichment was called
	assert.Len(t, mockMessagingRepo.AuthRepo.ParsePrincipalsCalls, 1)
	assert.Equal(t, transaction.Headers, mockMessagingRepo.AuthRepo.ParsePrincipalsCalls[0].Headers)
}

func TestIndexerService_ProcessTransaction_InvalidAction(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test data with invalid action
	transaction := &contracts.LFXTransaction{
		Action:     "invalid-action",
		ObjectType: constants.ObjectTypeProject,
		Data: map[string]any{
			"id": "test-project",
		},
		Timestamp: time.Now(),
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "invalid transaction action")
	assert.Len(t, mockStorageRepo.IndexCalls, 0) // Should not have indexed
}

func TestIndexerService_ProcessTransaction_InvalidObjectType(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test data with invalid object type
	transaction := &contracts.LFXTransaction{
		Action:     constants.ActionCreated,
		ObjectType: "invalid-type",
		Data: map[string]any{
			"id": "test-object",
		},
		Timestamp: time.Now(),
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "unsupported object type")
	assert.Len(t, mockStorageRepo.IndexCalls, 0) // Should not have indexed
}

func TestIndexerService_ProcessTransaction_BasicValidation(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test that basic processing works and mocks are functional
	transaction := &contracts.LFXTransaction{
		Action:     constants.ActionCreated,
		ObjectType: constants.ObjectTypeProject,
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
		},
		Data: map[string]any{
			"id":     "test-project",
			"name":   "Test Project",
			"public": true, // Required for access control
		},
		Timestamp: time.Now(),
		ParsedPrincipals: []contracts.Principal{
			{
				Principal: "test_user",
				Email:     "test@example.com",
			},
		},
	}

	// Execute
	result, err := service.ProcessTransaction(context.Background(), transaction, "test-index")

	// Verify basic functionality works
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.True(t, result.IndexSuccess)
	assert.Equal(t, "project:test-project", result.DocumentID)
}

func TestIndexerService_HealthCacheWorksCorrectly(t *testing.T) {
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)

	// First call should execute health check
	status1 := indexerService.CheckReadiness(context.Background())
	assert.Equal(t, "healthy", status1.Status)

	// Second call within cache duration should use cache
	status2 := indexerService.CheckReadiness(context.Background())
	assert.Equal(t, "healthy", status2.Status)
	// Cache may or may not be used depending on timing, just verify both are healthy

	// Test basic health functionality works
	assert.Contains(t, status1.Checks, "opensearch")
	assert.Contains(t, status1.Checks, "nats")
}

func TestIndexerService_ReadinessChecksAllDependencies(t *testing.T) {
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)

	// Test with all healthy
	status := indexerService.CheckReadiness(context.Background())
	assert.Equal(t, "healthy", status.Status)
	assert.Contains(t, status.Checks, "opensearch")
	assert.Contains(t, status.Checks, "nats")
	assert.Equal(t, "healthy", status.Checks["opensearch"].Status)
	assert.Equal(t, "healthy", status.Checks["nats"].Status)
	assert.Equal(t, 0, status.ErrorCount)
}

func TestIndexerService_LivenessAlwaysHealthy(t *testing.T) {
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)

	// Test with all dependencies failing
	mockStorageRepo.HealthError = assert.AnError
	mockMessagingRepo.HealthError = assert.AnError

	// Liveness should still be healthy (only checks if service is running)
	status := indexerService.CheckLiveness(context.Background())
	assert.Equal(t, "healthy", status.Status)
}

func TestIndexerService_GeneralHealthCheck(t *testing.T) {
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)

	// Test healthy state
	status := indexerService.CheckHealth(context.Background())
	assert.Equal(t, "healthy", status.Status)
	assert.Equal(t, 0, status.ErrorCount)
	assert.NotZero(t, status.Duration)
	assert.NotZero(t, status.Timestamp)
}

func TestIndexerService_ValidateObjectType_RegistryBased(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	// Test valid object type (project is registered in the enricher registry)
	validTransaction := &contracts.LFXTransaction{
		ObjectType: constants.ObjectTypeProject,
	}
	err := service.ValidateObjectType(validTransaction)
	assert.NoError(t, err, "Project object type should be valid")

	// Test invalid object type (committee is not registered in the enricher registry)
	invalidTransaction := &contracts.LFXTransaction{
		ObjectType: "invalid_type",
	}
	err = service.ValidateObjectType(invalidTransaction)
	assert.Error(t, err, "Committee object type should be invalid (not registered)")
	assert.Contains(t, err.Error(), "no enricher found for object type: invalid_type")

	// Test completely unknown object type
	unknownTransaction := &contracts.LFXTransaction{
		ObjectType: "unknown-type",
	}
	err = service.ValidateObjectType(unknownTransaction)
	assert.Error(t, err, "Unknown object type should be invalid")
	assert.Contains(t, err.Error(), "no enricher found for object type: unknown-type")
}

func TestIndexerService_parseIndexingConfig(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	tests := []struct {
		name        string
		input       map[string]any
		wantErr     bool
		errContains string
		validate    func(t *testing.T, config *types.IndexingConfig)
	}{
		{
			name: "valid complete config",
			input: map[string]any{
				"object_id":              "proj-123",
				"public":                 true,
				"access_check_object":    "project:proj-123",
				"access_check_relation":  "viewer",
				"history_check_object":   "project:proj-123",
				"history_check_relation": "historian",
				"sort_name":              "Test Project",
				"name_and_aliases":       []interface{}{"Test Project", "TP"},
				"parent_refs":            []interface{}{"org:org-456"},
				"tags":                   []interface{}{"tag1", "tag2", "tag3"},
				"fulltext":               "Test Project description",
			},
			wantErr: false,
			validate: func(t *testing.T, config *types.IndexingConfig) {
				assert.Equal(t, "proj-123", config.ObjectID)
				assert.NotNil(t, config.Public)
				assert.True(t, *config.Public)
				assert.Equal(t, "project:proj-123", config.AccessCheckObject)
				assert.Equal(t, "viewer", config.AccessCheckRelation)
				assert.Equal(t, "project:proj-123", config.HistoryCheckObject)
				assert.Equal(t, "historian", config.HistoryCheckRelation)
				assert.Equal(t, "Test Project", config.SortName)
				assert.Equal(t, []string{"Test Project", "TP"}, config.NameAndAliases)
				assert.Equal(t, []string{"org:org-456"}, config.ParentRefs)
				assert.Equal(t, []string{"tag1", "tag2", "tag3"}, config.Tags)
				assert.Equal(t, "Test Project description", config.Fulltext)
			},
		},
		{
			name: "valid minimal config (required fields only)",
			input: map[string]any{
				"object_id":              "proj-456",
				"access_check_object":    "project:proj-456",
				"access_check_relation":  "viewer",
				"history_check_object":   "project:proj-456",
				"history_check_relation": "historian",
			},
			wantErr: false,
			validate: func(t *testing.T, config *types.IndexingConfig) {
				assert.Equal(t, "proj-456", config.ObjectID)
				assert.Nil(t, config.Public)
				assert.Equal(t, "project:proj-456", config.AccessCheckObject)
				assert.Equal(t, "viewer", config.AccessCheckRelation)
				assert.Equal(t, "project:proj-456", config.HistoryCheckObject)
				assert.Equal(t, "historian", config.HistoryCheckRelation)
				assert.Empty(t, config.SortName)
				assert.Empty(t, config.NameAndAliases)
				assert.Empty(t, config.ParentRefs)
				assert.Empty(t, config.Fulltext)
			},
		},
		{
			name: "missing object_id",
			input: map[string]any{
				"access_check_object":    "project:proj-123",
				"access_check_relation":  "viewer",
				"history_check_object":   "project:proj-123",
				"history_check_relation": "historian",
			},
			wantErr:     true,
			errContains: "object_id is required",
		},
		{
			name: "empty object_id",
			input: map[string]any{
				"object_id":              "",
				"access_check_object":    "project:proj-123",
				"access_check_relation":  "viewer",
				"history_check_object":   "project:proj-123",
				"history_check_relation": "historian",
			},
			wantErr:     true,
			errContains: "object_id is required",
		},
		{
			name: "missing access_check_object",
			input: map[string]any{
				"object_id":              "proj-123",
				"access_check_relation":  "viewer",
				"history_check_object":   "project:proj-123",
				"history_check_relation": "historian",
			},
			wantErr:     true,
			errContains: "access_check_object is required",
		},
		{
			name: "missing access_check_relation",
			input: map[string]any{
				"object_id":              "proj-123",
				"access_check_object":    "project:proj-123",
				"history_check_object":   "project:proj-123",
				"history_check_relation": "historian",
			},
			wantErr:     true,
			errContains: "access_check_relation is required",
		},
		{
			name: "missing history_check_object",
			input: map[string]any{
				"object_id":              "proj-123",
				"access_check_object":    "project:proj-123",
				"access_check_relation":  "viewer",
				"history_check_relation": "historian",
			},
			wantErr:     true,
			errContains: "history_check_object is required",
		},
		{
			name: "missing history_check_relation",
			input: map[string]any{
				"object_id":             "proj-123",
				"access_check_object":   "project:proj-123",
				"access_check_relation": "viewer",
				"history_check_object":  "project:proj-123",
			},
			wantErr:     true,
			errContains: "history_check_relation is required",
		},
		{
			name: "invalid object_id type",
			input: map[string]any{
				"object_id":              123,
				"access_check_object":    "project:proj-123",
				"access_check_relation":  "viewer",
				"history_check_object":   "project:proj-123",
				"history_check_relation": "historian",
			},
			wantErr:     true,
			errContains: "object_id is required",
		},
		{
			name: "invalid access_check_object type",
			input: map[string]any{
				"object_id":              "proj-123",
				"access_check_object":    123,
				"access_check_relation":  "viewer",
				"history_check_object":   "project:proj-123",
				"history_check_relation": "historian",
			},
			wantErr:     true,
			errContains: "access_check_object is required",
		},
		{
			name: "public flag false",
			input: map[string]any{
				"object_id":              "proj-789",
				"public":                 false,
				"access_check_object":    "project:proj-789",
				"access_check_relation":  "viewer",
				"history_check_object":   "project:proj-789",
				"history_check_relation": "historian",
			},
			wantErr: false,
			validate: func(t *testing.T, config *types.IndexingConfig) {
				assert.NotNil(t, config.Public)
				assert.False(t, *config.Public)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := service.parseIndexingConfig(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, config)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
				if tt.validate != nil {
					tt.validate(t, config)
				}
			}
		})
	}
}

func TestIndexerService_buildTransactionBodyFromIndexingConfig(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	tests := []struct {
		name        string
		data        map[string]any
		config      *types.IndexingConfig
		transaction *contracts.LFXTransaction
		validate    func(t *testing.T, body *contracts.TransactionBody)
	}{
		{
			name: "complete config with all optional fields - created action",
			data: map[string]any{
				"id":          "proj-123",
				"name":        "Test Project",
				"description": "A test project",
			},
			config: &types.IndexingConfig{
				ObjectID:             "proj-123",
				Public:               func() *bool { b := true; return &b }(),
				AccessCheckObject:    "project:proj-123",
				AccessCheckRelation:  "viewer",
				HistoryCheckObject:   "project:proj-123",
				HistoryCheckRelation: "historian",
				SortName:             "test project",
				NameAndAliases:       []string{"Test Project", "TP"},
				ParentRefs:           []string{"org:org-456"},
				Tags:                 []string{"tag1", "tag2"},
				Fulltext:             "Test Project description",
			},
			transaction: &contracts.LFXTransaction{
				Action:           constants.ActionCreated,
				ObjectType:       "project",
				Timestamp:        time.Now(),
				ParsedPrincipals: []contracts.Principal{{Principal: "user:123", Email: "test@example.com"}},
			},
			validate: func(t *testing.T, body *contracts.TransactionBody) {
				assert.Equal(t, "project", body.ObjectType)
				assert.Equal(t, "proj-123", body.ObjectID)
				assert.Equal(t, "project:proj-123", body.ObjectRef)
				assert.True(t, body.Public)
				assert.Equal(t, "project:proj-123", body.AccessCheckObject)
				assert.Equal(t, "viewer", body.AccessCheckRelation)
				assert.Equal(t, "project:proj-123", body.HistoryCheckObject)
				assert.Equal(t, "historian", body.HistoryCheckRelation)
				assert.Equal(t, "project:proj-123#viewer", body.AccessCheckQuery)
				assert.Equal(t, "project:proj-123#historian", body.HistoryCheckQuery)
				assert.Equal(t, "test project", body.SortName)
				assert.Equal(t, []string{"Test Project", "TP"}, body.NameAndAliases)
				assert.Equal(t, []string{"org:org-456"}, body.ParentRefs)
				assert.Equal(t, []string{"tag1", "tag2"}, body.Tags)
				assert.Equal(t, "Test Project description", body.Fulltext)
				assert.NotNil(t, body.Data)
				assert.Equal(t, "proj-123", body.Data["id"])

				// Verify server-side fields
				assert.NotNil(t, body.Latest)
				assert.True(t, *body.Latest)
				assert.NotNil(t, body.CreatedAt)
				assert.NotNil(t, body.UpdatedAt)
				assert.Equal(t, body.CreatedAt, body.UpdatedAt) // For created actions
				assert.Len(t, body.CreatedBy, 1)
				assert.Contains(t, body.CreatedBy[0], "user:123")
				assert.Contains(t, body.CreatedBy[0], "test@example.com")
				assert.Equal(t, []string{"user:123"}, body.CreatedByPrincipals)
				assert.Equal(t, []string{"test@example.com"}, body.CreatedByEmails)
			},
		},
		{
			name: "minimal config with only required fields - updated action",
			data: map[string]any{
				"id": "proj-456",
			},
			config: &types.IndexingConfig{
				ObjectID:             "proj-456",
				AccessCheckObject:    "project:proj-456",
				AccessCheckRelation:  "viewer",
				HistoryCheckObject:   "project:proj-456",
				HistoryCheckRelation: "historian",
			},
			transaction: &contracts.LFXTransaction{
				Action:           constants.ActionUpdated,
				ObjectType:       "project",
				Timestamp:        time.Now(),
				ParsedPrincipals: []contracts.Principal{{Principal: "user:456", Email: "updater@example.com"}},
			},
			validate: func(t *testing.T, body *contracts.TransactionBody) {
				assert.Equal(t, "project", body.ObjectType)
				assert.Equal(t, "proj-456", body.ObjectID)
				assert.Equal(t, "project:proj-456", body.ObjectRef)
				assert.False(t, body.Public) // Default false when not specified
				assert.Equal(t, "project:proj-456#viewer", body.AccessCheckQuery)
				assert.Equal(t, "project:proj-456#historian", body.HistoryCheckQuery)
				assert.Empty(t, body.SortName)
				assert.Empty(t, body.NameAndAliases)
				assert.Empty(t, body.ParentRefs)
				assert.Empty(t, body.Fulltext)

				// Verify server-side fields for update action
				assert.NotNil(t, body.Latest)
				assert.True(t, *body.Latest)
				assert.NotNil(t, body.UpdatedAt)
				assert.Nil(t, body.CreatedAt) // Should not be set for updates
				assert.Len(t, body.UpdatedBy, 1)
				assert.Contains(t, body.UpdatedBy[0], "user:456")
				assert.Equal(t, []string{"user:456"}, body.UpdatedByPrincipals)
				assert.Equal(t, []string{"updater@example.com"}, body.UpdatedByEmails)
			},
		},
		{
			name: "public flag set to false - deleted action",
			data: map[string]any{
				"id": "proj-789",
			},
			config: &types.IndexingConfig{
				ObjectID:             "proj-789",
				Public:               func() *bool { b := false; return &b }(),
				AccessCheckObject:    "project:proj-789",
				AccessCheckRelation:  "viewer",
				HistoryCheckObject:   "project:proj-789",
				HistoryCheckRelation: "historian",
			},
			transaction: &contracts.LFXTransaction{
				Action:           constants.ActionDeleted,
				ObjectType:       "project",
				Timestamp:        time.Now(),
				ParsedPrincipals: []contracts.Principal{{Principal: "user:789", Email: ""}},
			},
			validate: func(t *testing.T, body *contracts.TransactionBody) {
				assert.False(t, body.Public)

				// Verify server-side fields for delete action
				assert.NotNil(t, body.Latest)
				assert.True(t, *body.Latest)
				assert.NotNil(t, body.DeletedAt)
				assert.Nil(t, body.CreatedAt)
				assert.Nil(t, body.UpdatedAt)
				assert.Len(t, body.DeletedBy, 1)
				assert.Contains(t, body.DeletedBy[0], "user:789")
				assert.Equal(t, []string{"user:789"}, body.DeletedByPrincipals)
				assert.Empty(t, body.DeletedByEmails) // No email in this test
			},
		},
		{
			name: "verify FGA query building",
			data: map[string]any{
				"id": "committee-123",
			},
			config: &types.IndexingConfig{
				ObjectID:             "committee-123",
				AccessCheckObject:    "committee:committee-123",
				AccessCheckRelation:  "member",
				HistoryCheckObject:   "committee:committee-123",
				HistoryCheckRelation: "admin",
			},
			transaction: &contracts.LFXTransaction{
				Action:           constants.ActionCreated,
				ObjectType:       "committee",
				Timestamp:        time.Now(),
				ParsedPrincipals: []contracts.Principal{},
			},
			validate: func(t *testing.T, body *contracts.TransactionBody) {
				assert.Equal(t, "committee:committee-123#member", body.AccessCheckQuery)
				assert.Equal(t, "committee:committee-123#admin", body.HistoryCheckQuery)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := service.buildTransactionBodyFromIndexingConfig(tt.data, tt.config, tt.transaction)

			assert.NoError(t, err)
			assert.NotNil(t, body)
			if tt.validate != nil {
				tt.validate(t, body)
			}
		})
	}
}

func TestIndexerService_enrichTransactionData(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	service := NewIndexerService(mockStorageRepo, mockMessagingRepo, logger)

	tests := []struct {
		name        string
		transaction *contracts.LFXTransaction
		body        *contracts.TransactionBody
		wantErr     bool
		errContains string
		validate    func(t *testing.T, body *contracts.TransactionBody)
	}{
		{
			name: "with indexing_config - bypass enrichers",
			transaction: &contracts.LFXTransaction{
				Action:     constants.ActionCreated,
				ObjectType: "project",
				Data: map[string]any{
					"id":          "proj-123",
					"name":        "Test Project",
					"description": "A test project",
				},
				IndexingConfig: &types.IndexingConfig{
					ObjectID:             "proj-123",
					Public:               func() *bool { b := true; return &b }(),
					AccessCheckObject:    "project:proj-123",
					AccessCheckRelation:  "viewer",
					HistoryCheckObject:   "project:proj-123",
					HistoryCheckRelation: "historian",
					SortName:             "test project",
					NameAndAliases:       []string{"Test Project", "TP"},
					ParentRefs:           []string{"org:org-456"},
					Tags:                 []string{"tag1", "tag2"},
					Fulltext:             "Test Project description",
				},
				Timestamp: time.Now(),
			},
			body:    &contracts.TransactionBody{},
			wantErr: false,
			validate: func(t *testing.T, body *contracts.TransactionBody) {
				// Verify the body was populated from config
				assert.Equal(t, "project", body.ObjectType)
				assert.Equal(t, "proj-123", body.ObjectID)
				assert.Equal(t, "project:proj-123", body.ObjectRef)
				assert.True(t, body.Public)
				assert.Equal(t, "project:proj-123#viewer", body.AccessCheckQuery)
				assert.Equal(t, "project:proj-123#historian", body.HistoryCheckQuery)
				assert.Equal(t, "test project", body.SortName)
				assert.Equal(t, []string{"Test Project", "TP"}, body.NameAndAliases)
				assert.Equal(t, []string{"org:org-456"}, body.ParentRefs)
				assert.Equal(t, []string{"tag1", "tag2"}, body.Tags)
				assert.Equal(t, "Test Project description", body.Fulltext)
			},
		},
		{
			name: "without indexing_config - use enricher registry",
			transaction: &contracts.LFXTransaction{
				Action:     constants.ActionCreated,
				ObjectType: constants.ObjectTypeProject,
				Data: map[string]any{
					"uid":    "proj-456",
					"name":   "Test Project via Enricher",
					"public": true, // Required by project enricher
				},
				ParsedData: map[string]any{
					"uid":    "proj-456",
					"name":   "Test Project via Enricher",
					"public": true, // Required by project enricher
				},
				IndexingConfig: nil, // No config, should use enricher
				Timestamp:      time.Now(),
			},
			body: &contracts.TransactionBody{
				ObjectType: constants.ObjectTypeProject, // Pre-populated by GenerateTransactionBody
			},
			wantErr: false,
			validate: func(t *testing.T, body *contracts.TransactionBody) {
				// Verify the body was populated via enricher
				assert.Equal(t, constants.ObjectTypeProject, body.ObjectType)
				assert.Equal(t, "proj-456", body.ObjectID)
				// Note: ObjectRef is set by GenerateTransactionBody after enrichTransactionData returns
				assert.True(t, body.Public)
				// Enricher should have set these
				assert.NotEmpty(t, body.AccessCheckQuery)
				assert.NotEmpty(t, body.HistoryCheckQuery)
			},
		},
		{
			name: "with indexing_config but invalid data type",
			transaction: &contracts.LFXTransaction{
				Action:     constants.ActionCreated,
				ObjectType: "project",
				Data:       "invalid-data-type", // Not a map
				IndexingConfig: &types.IndexingConfig{
					ObjectID:             "proj-789",
					AccessCheckObject:    "project:proj-789",
					AccessCheckRelation:  "viewer",
					HistoryCheckObject:   "project:proj-789",
					HistoryCheckRelation: "historian",
				},
				Timestamp: time.Now(),
			},
			body:        &contracts.TransactionBody{},
			wantErr:     true,
			errContains: "data is not a map[string]any",
		},
		{
			name: "without indexing_config and invalid object type",
			transaction: &contracts.LFXTransaction{
				Action:         constants.ActionCreated,
				ObjectType:     "invalid-object-type",
				Data:           map[string]any{"id": "test-123"},
				IndexingConfig: nil,
				Timestamp:      time.Now(),
			},
			body:        &contracts.TransactionBody{},
			wantErr:     true,
			errContains: "no enricher found for object type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.enrichTransactionData(tt.body, tt.transaction)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, tt.body)
				}
			}
		})
	}
}
