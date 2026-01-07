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

func TestGroupsIOServiceSettingsEnricher_ObjectType(t *testing.T) {
	enricher := NewGroupsIOServiceSettingsEnricher()
	assert.Equal(t, constants.ObjectTypeGroupsIOServiceSettings, enricher.ObjectType())
}

func TestGroupsIOServiceSettingsEnricher_EnrichData(t *testing.T) {
	enricher := NewGroupsIOServiceSettingsEnricher()

	tests := []struct {
		name           string
		parsedData     map[string]any
		expectedBody   *contracts.TransactionBody
		expectedError  string
		expectedFields []string // fields to verify
	}{
		{
			name: "successful enrichment with uid field",
			parsedData: map[string]any{
				"uid": "service-123",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "service-123",
				Public:   false, // Settings should default to private
			},
			expectedFields: []string{"ObjectID", "Public"},
		},
		{
			name: "successful enrichment with legacy id field",
			parsedData: map[string]any{
				"id": "service-456",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "service-456",
				Public:   false,
			},
			expectedFields: []string{"ObjectID", "Public"},
		},
		{
			name: "access control with computed defaults - parent is groupsio_service",
			parsedData: map[string]any{
				"uid": "service-defaults",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "service-defaults",
				Public:               false,
				AccessCheckObject:    "groupsio_service:service-defaults", // Parent object type
				AccessCheckRelation:  "auditor",                           // Settings default
				HistoryCheckObject:   "groupsio_service:service-defaults",
				HistoryCheckRelation: "writer",
				AccessCheckQuery:     "groupsio_service:service-defaults#auditor",
				HistoryCheckQuery:    "groupsio_service:service-defaults#writer",
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation", "AccessCheckQuery", "HistoryCheckQuery"},
		},
		{
			name: "successful enrichment with custom access control",
			parsedData: map[string]any{
				"uid":                  "service-789",
				"accessCheckObject":    "custom-object",
				"accessCheckRelation":  "admin",
				"historyCheckObject":   "custom-history",
				"historyCheckRelation": "member",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "service-789",
				Public:               false,
				AccessCheckObject:    "custom-object",
				AccessCheckRelation:  "admin",
				HistoryCheckObject:   "custom-history",
				HistoryCheckRelation: "member",
				AccessCheckQuery:     "custom-object#admin",
				HistoryCheckQuery:    "custom-history#member",
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation", "AccessCheckQuery", "HistoryCheckQuery"},
		},
		{
			name: "complete enrichment with settings data",
			parsedData: map[string]any{
				"uid": "service-complete",
				"settings": map[string]any{
					"auto_sync_enabled":  true,
					"notification_email": "admin@example.com",
				},
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "service-complete",
				Public:               false,
				AccessCheckObject:    "groupsio_service:service-complete",
				AccessCheckRelation:  "auditor",
				HistoryCheckObject:   "groupsio_service:service-complete",
				HistoryCheckRelation: "writer",
				AccessCheckQuery:     "groupsio_service:service-complete#auditor",
				HistoryCheckQuery:    "groupsio_service:service-complete#writer",
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation", "AccessCheckQuery", "HistoryCheckQuery"},
		},
		{
			name: "error: missing uid and id",
			parsedData: map[string]any{
				"settings": map[string]any{"enabled": true},
			},
			expectedError: constants.ErrMappingUID,
		},
		{
			name: "error: invalid uid type",
			parsedData: map[string]any{
				"uid": 123,
			},
			expectedError: constants.ErrMappingUID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
			}

			err := enricher.EnrichData(body, transaction)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)

			// Verify that data is set on body
			assert.Equal(t, tt.parsedData, body.Data, "Data should be set on body")

			// Verify specified fields
			for _, field := range tt.expectedFields {
				switch field {
				case "ObjectID":
					assert.Equal(t, tt.expectedBody.ObjectID, body.ObjectID, "ObjectID mismatch")
				case "Public":
					assert.Equal(t, tt.expectedBody.Public, body.Public, "Public mismatch")
				case "AccessCheckObject":
					assert.Equal(t, tt.expectedBody.AccessCheckObject, body.AccessCheckObject, "AccessCheckObject mismatch")
				case "AccessCheckRelation":
					assert.Equal(t, tt.expectedBody.AccessCheckRelation, body.AccessCheckRelation, "AccessCheckRelation mismatch")
				case "HistoryCheckObject":
					assert.Equal(t, tt.expectedBody.HistoryCheckObject, body.HistoryCheckObject, "HistoryCheckObject mismatch")
				case "HistoryCheckRelation":
					assert.Equal(t, tt.expectedBody.HistoryCheckRelation, body.HistoryCheckRelation, "HistoryCheckRelation mismatch")
				case "AccessCheckQuery":
					assert.Equal(t, tt.expectedBody.AccessCheckQuery, body.AccessCheckQuery, "AccessCheckQuery mismatch")
				case "HistoryCheckQuery":
					assert.Equal(t, tt.expectedBody.HistoryCheckQuery, body.HistoryCheckQuery, "HistoryCheckQuery mismatch")
				}
			}
		})
	}
}

func TestGroupsIOServiceSettingsEnricher_EnrichData_ParentPermissions(t *testing.T) {
	enricher := NewGroupsIOServiceSettingsEnricher()
	body := &contracts.TransactionBody{}

	// Test that permissions are based on parent groupsio_service object
	transaction := &contracts.LFXTransaction{
		ParsedData: map[string]any{
			"uid": "service-parent-test",
		},
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	assert.Equal(t, "service-parent-test", body.ObjectID)
	assert.False(t, body.Public)
	// Permissions should reference parent groupsio_service object
	assert.Equal(t, "groupsio_service:service-parent-test", body.AccessCheckObject)
	assert.Equal(t, "auditor", body.AccessCheckRelation)
	assert.Equal(t, "groupsio_service:service-parent-test", body.HistoryCheckObject)
	assert.Equal(t, "writer", body.HistoryCheckRelation)
	assert.Equal(t, "groupsio_service:service-parent-test#auditor", body.AccessCheckQuery)
	assert.Equal(t, "groupsio_service:service-parent-test#writer", body.HistoryCheckQuery)
}

func TestGroupsIOServiceSettingsEnricher_EnrichData_EdgeCases(t *testing.T) {
	enricher := NewGroupsIOServiceSettingsEnricher()

	t.Run("empty string values for access control", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"uid":                  "service-empty-strings",
				"accessCheckObject":    "",
				"accessCheckRelation":  "",
				"historyCheckObject":   "",
				"historyCheckRelation": "",
			},
		}

		err := enricher.EnrichData(body, transaction)
		require.NoError(t, err)

		// Empty strings should be set (not ignored)
		assert.Equal(t, "", body.AccessCheckObject)
		assert.Equal(t, "", body.AccessCheckRelation)
		assert.Equal(t, "", body.HistoryCheckObject)
		assert.Equal(t, "", body.HistoryCheckRelation)
	})

	t.Run("complex settings data preserved", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		complexData := map[string]any{
			"uid": "service-complex",
			"settings": map[string]any{
				"sync_config": map[string]any{
					"frequency": "hourly",
					"enabled":   true,
				},
				"api_settings": map[string]any{
					"api_key": "encrypted-key",
					"timeout": 30,
				},
			},
		}
		transaction := &contracts.LFXTransaction{
			ParsedData: complexData,
		}

		err := enricher.EnrichData(body, transaction)
		require.NoError(t, err)

		// Complex data should be preserved exactly
		assert.Equal(t, complexData, body.Data, "Complex settings data should be preserved")
		assert.Equal(t, "service-complex", body.ObjectID)
		assert.False(t, body.Public)
	})
}
