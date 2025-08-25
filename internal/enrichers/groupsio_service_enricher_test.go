// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package enrichers

import (
	"testing"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupsIOServiceEnricher_ObjectType(t *testing.T) {
	enricher := NewGroupsIOServiceEnricher()
	assert.Equal(t, constants.ObjectTypeGroupsIOService, enricher.ObjectType())
}

func TestGroupsIOServiceEnricher_EnrichData_Success(t *testing.T) {
	enricher := NewGroupsIOServiceEnricher()

	tests := []struct {
		name           string
		parsedData     map[string]any
		expectedBody   *contracts.TransactionBody
		expectedFields []string
	}{
		{
			name: "successful enrichment with uid and public flag",
			parsedData: map[string]any{
				"uid":    "groupsio-123",
				"public": true,
				"name":   "Test GroupsIO Service",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "groupsio-123",
				Public:               true,
				SortName:             "Test GroupsIO Service",
				NameAndAliases:       []string{"Test GroupsIO Service"},
				AccessCheckObject:    "groupsio_service:groupsio-123",
				AccessCheckRelation:  "viewer",
				HistoryCheckObject:   "groupsio_service:groupsio-123",
				HistoryCheckRelation: "writer",
				ObjectType:           constants.ObjectTypeGroupsIOService,
			},
			expectedFields: []string{"ObjectID", "Public", "SortName", "NameAndAliases", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation", "ObjectType"},
		},
		{
			name: "enrichment with legacy id field",
			parsedData: map[string]any{
				"id":     "groupsio-456",
				"public": false,
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "groupsio-456",
				Public:               false,
				AccessCheckObject:    "groupsio_service:groupsio-456",
				AccessCheckRelation:  "viewer",
				HistoryCheckObject:   "groupsio_service:groupsio-456",
				HistoryCheckRelation: "writer",
				ObjectType:           constants.ObjectTypeGroupsIOService,
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation", "ObjectType"},
		},
		{
			name: "enrichment with custom access control",
			parsedData: map[string]any{
				"uid":                  "groupsio-789",
				"public":               true,
				"accessCheckObject":    "organization:test-org",
				"accessCheckRelation":  "member",
				"historyCheckObject":   "organization:test-org",
				"historyCheckRelation": "admin",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "groupsio-789",
				Public:               true,
				AccessCheckObject:    "organization:test-org",
				AccessCheckRelation:  "member",
				HistoryCheckObject:   "organization:test-org",
				HistoryCheckRelation: "admin",
				ObjectType:           constants.ObjectTypeGroupsIOService,
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation", "ObjectType"},
		},
		{
			name: "enrichment with parent references",
			parsedData: map[string]any{
				"uid":        "groupsio-child",
				"public":     true,
				"parent_uid": "groupsio-parent",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "groupsio-child",
				Public:               true,
				ParentRefs:           []string{"groupsio_service:groupsio-parent"},
				AccessCheckObject:    "groupsio_service:groupsio-child",
				AccessCheckRelation:  "viewer",
				HistoryCheckObject:   "groupsio_service:groupsio-child",
				HistoryCheckRelation: "writer",
				ObjectType:           constants.ObjectTypeGroupsIOService,
			},
			expectedFields: []string{"ObjectID", "Public", "ParentRefs", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation", "ObjectType"},
		},
		{
			name: "enrichment with fulltext content",
			parsedData: map[string]any{
				"uid":         "groupsio-fulltext",
				"public":      true,
				"name":        "Test GroupsIO",
				"title":       "GroupsIO Service",
				"description": "A test GroupsIO service for indexing",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:       "groupsio-fulltext",
				Public:         true,
				SortName:       "Test GroupsIO",
				NameAndAliases: []string{"Test GroupsIO", "GroupsIO Service"},
				Fulltext:       "Test GroupsIO GroupsIO Service A test GroupsIO service for indexing",
				ObjectType:     constants.ObjectTypeGroupsIOService,
			},
			expectedFields: []string{"ObjectID", "Public", "SortName", "NameAndAliases", "Fulltext", "ObjectType"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
				ObjectType: constants.ObjectTypeGroupsIOService,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)

			// Verify data is assigned to body
			assert.Equal(t, tt.parsedData, body.Data)

			// Verify specified fields
			for _, field := range tt.expectedFields {
				switch field {
				case "ObjectID":
					assert.Equal(t, tt.expectedBody.ObjectID, body.ObjectID, "ObjectID mismatch")
				case "Public":
					assert.Equal(t, tt.expectedBody.Public, body.Public, "Public mismatch")
				case "SortName":
					assert.Equal(t, tt.expectedBody.SortName, body.SortName, "SortName mismatch")
				case "NameAndAliases":
					assert.ElementsMatch(t, tt.expectedBody.NameAndAliases, body.NameAndAliases, "NameAndAliases mismatch")
				case "AccessCheckObject":
					assert.Equal(t, tt.expectedBody.AccessCheckObject, body.AccessCheckObject, "AccessCheckObject mismatch")
				case "AccessCheckRelation":
					assert.Equal(t, tt.expectedBody.AccessCheckRelation, body.AccessCheckRelation, "AccessCheckRelation mismatch")
				case "HistoryCheckObject":
					assert.Equal(t, tt.expectedBody.HistoryCheckObject, body.HistoryCheckObject, "HistoryCheckObject mismatch")
				case "HistoryCheckRelation":
					assert.Equal(t, tt.expectedBody.HistoryCheckRelation, body.HistoryCheckRelation, "HistoryCheckRelation mismatch")
				case "ParentRefs":
					assert.ElementsMatch(t, tt.expectedBody.ParentRefs, body.ParentRefs, "ParentRefs mismatch")
				case "ObjectType":
					assert.Equal(t, tt.expectedBody.ObjectType, body.ObjectType, "ObjectType mismatch")
				case "Fulltext":
					// For fulltext, check that all expected components are present
					if tt.expectedBody.Fulltext != "" {
						expectedComponents := []string{"Test GroupsIO", "GroupsIO Service", "A test GroupsIO service for indexing"}
						for _, component := range expectedComponents {
							assert.Contains(t, body.Fulltext, component, "fulltext should contain component: %s", component)
						}
					}
				}
			}
		})
	}
}

func TestGroupsIOServiceEnricher_EnrichData_ErrorCases(t *testing.T) {
	enricher := NewGroupsIOServiceEnricher()

	tests := []struct {
		name        string
		body        *contracts.TransactionBody
		transaction *contracts.LFXTransaction
		expectedErr string
	}{
		{
			name:        "nil body",
			body:        nil,
			transaction: &contracts.LFXTransaction{ParsedData: map[string]any{}},
			expectedErr: "transaction body cannot be nil",
		},
		{
			name:        "nil transaction",
			body:        &contracts.TransactionBody{},
			transaction: nil,
			expectedErr: "transaction cannot be nil",
		},
		{
			name: "nil parsed data",
			body: &contracts.TransactionBody{},
			transaction: &contracts.LFXTransaction{
				ParsedData: nil,
			},
			expectedErr: "transaction parsed data cannot be nil",
		},
		{
			name: "missing uid and id",
			body: &contracts.TransactionBody{},
			transaction: &contracts.LFXTransaction{
				ParsedData: map[string]any{
					"public": true,
				},
			},
			expectedErr: "missing required 'uid' or 'id' field",
		},
		{
			name: "invalid uid type",
			body: &contracts.TransactionBody{},
			transaction: &contracts.LFXTransaction{
				ParsedData: map[string]any{
					"uid":    123, // invalid type
					"public": true,
				},
			},
			expectedErr: "'uid' field exists but is not a valid non-empty string",
		},
		{
			name: "empty uid string",
			body: &contracts.TransactionBody{},
			transaction: &contracts.LFXTransaction{
				ParsedData: map[string]any{
					"uid":    "", // empty string
					"public": true,
				},
			},
			expectedErr: "'uid' field exists but is not a valid non-empty string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := enricher.EnrichData(tt.body, tt.transaction)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestGroupsIOServiceEnricher_EnrichData_DefaultBehavior(t *testing.T) {
	enricher := NewGroupsIOServiceEnricher()

	// Test default behavior when public flag is missing
	body := &contracts.TransactionBody{}
	transaction := &contracts.LFXTransaction{
		ParsedData: map[string]any{
			"uid": "test-groupsio",
			// public flag intentionally missing
		},
		ObjectType: constants.ObjectTypeGroupsIOService,
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	// Should default to false when public flag is missing
	assert.Equal(t, false, body.Public)
	assert.Equal(t, "test-groupsio", body.ObjectID)
	assert.Equal(t, constants.ObjectTypeGroupsIOService, body.ObjectType)
}

func TestNewGroupsIOServiceEnricher(t *testing.T) {
	enricher := NewGroupsIOServiceEnricher()
	require.NotNil(t, enricher)
	assert.Equal(t, constants.ObjectTypeGroupsIOService, enricher.ObjectType())
}
