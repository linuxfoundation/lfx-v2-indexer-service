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

func TestGroupsIOMailingListSettingsEnricher_ObjectType(t *testing.T) {
	enricher := NewGroupsIOMailingListSettingsEnricher()
	assert.Equal(t, constants.ObjectTypeGroupsIOMailingListSettings, enricher.ObjectType())
}

func TestGroupsIOMailingListSettingsEnricher_EnrichData(t *testing.T) {
	enricher := NewGroupsIOMailingListSettingsEnricher()

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
				"uid": "list-123",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "list-123",
				Public:   false, // Settings should default to private
			},
			expectedFields: []string{"ObjectID", "Public"},
		},
		{
			name: "successful enrichment with legacy id field",
			parsedData: map[string]any{
				"id": "list-456",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "list-456",
				Public:   false,
			},
			expectedFields: []string{"ObjectID", "Public"},
		},
		{
			name: "access control with computed defaults - parent is groupsio_mailing_list",
			parsedData: map[string]any{
				"uid": "list-defaults",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "list-defaults",
				Public:               false,
				AccessCheckObject:    "groupsio_mailing_list:list-defaults", // Parent object type
				AccessCheckRelation:  "auditor",                             // Settings default
				HistoryCheckObject:   "groupsio_mailing_list:list-defaults",
				HistoryCheckRelation: "writer",
				AccessCheckQuery:     "groupsio_mailing_list:list-defaults#auditor",
				HistoryCheckQuery:    "groupsio_mailing_list:list-defaults#writer",
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation", "AccessCheckQuery", "HistoryCheckQuery"},
		},
		{
			name: "successful enrichment with custom access control",
			parsedData: map[string]any{
				"uid":                  "list-789",
				"accessCheckObject":    "custom-object",
				"accessCheckRelation":  "admin",
				"historyCheckObject":   "custom-history",
				"historyCheckRelation": "member",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "list-789",
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
				"uid": "list-complete",
				"settings": map[string]any{
					"moderation_enabled": true,
					"max_message_size":   1024,
				},
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "list-complete",
				Public:               false,
				AccessCheckObject:    "groupsio_mailing_list:list-complete",
				AccessCheckRelation:  "auditor",
				HistoryCheckObject:   "groupsio_mailing_list:list-complete",
				HistoryCheckRelation: "writer",
				AccessCheckQuery:     "groupsio_mailing_list:list-complete#auditor",
				HistoryCheckQuery:    "groupsio_mailing_list:list-complete#writer",
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

func TestGroupsIOMailingListSettingsEnricher_EnrichData_ParentPermissions(t *testing.T) {
	enricher := NewGroupsIOMailingListSettingsEnricher()
	body := &contracts.TransactionBody{}

	// Test that permissions are based on parent groupsio_mailing_list object
	transaction := &contracts.LFXTransaction{
		ParsedData: map[string]any{
			"uid": "list-parent-test",
		},
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	assert.Equal(t, "list-parent-test", body.ObjectID)
	assert.False(t, body.Public)
	// Permissions should reference parent groupsio_mailing_list object
	assert.Equal(t, "groupsio_mailing_list:list-parent-test", body.AccessCheckObject)
	assert.Equal(t, "auditor", body.AccessCheckRelation)
	assert.Equal(t, "groupsio_mailing_list:list-parent-test", body.HistoryCheckObject)
	assert.Equal(t, "writer", body.HistoryCheckRelation)
	assert.Equal(t, "groupsio_mailing_list:list-parent-test#auditor", body.AccessCheckQuery)
	assert.Equal(t, "groupsio_mailing_list:list-parent-test#writer", body.HistoryCheckQuery)
}

func TestGroupsIOMailingListSettingsEnricher_EnrichData_EdgeCases(t *testing.T) {
	enricher := NewGroupsIOMailingListSettingsEnricher()

	t.Run("empty string values for access control", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"uid":                  "list-empty-strings",
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
			"uid": "list-complex",
			"settings": map[string]any{
				"moderation": map[string]any{
					"enabled":        true,
					"auto_approve":   false,
					"require_reason": true,
				},
				"delivery_options": map[string]any{
					"digest_frequency": "daily",
					"max_size_mb":      10,
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
		assert.Equal(t, "list-complex", body.ObjectID)
		assert.False(t, body.Public)
	})
}
