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

func TestProjectSettingsEnricher_ObjectType(t *testing.T) {
	enricher := NewProjectSettingsEnricher()
	assert.Equal(t, constants.ObjectTypeProjectSettings, enricher.ObjectType())
}

func TestProjectSettingsEnricher_EnrichData(t *testing.T) {
	enricher := NewProjectSettingsEnricher()

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
				"uid": "project-123",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "project-123",
				Public:   false, // Always false for project settings
			},
			expectedFields: []string{"ObjectID", "Public"},
		},
		{
			name: "successful enrichment with legacy id field",
			parsedData: map[string]any{
				"id": "project-456",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "project-456",
				Public:   false, // Always false for project settings
			},
			expectedFields: []string{"ObjectID", "Public"},
		},
		{
			name: "successful enrichment with access control attributes",
			parsedData: map[string]any{
				"uid":                  "project-789",
				"accessCheckObject":    "custom-object",
				"accessCheckRelation":  "admin",
				"historyCheckObject":   "custom-history",
				"historyCheckRelation": "member",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "project-789",
				Public:               false,
				AccessCheckObject:    "custom-object",  // Override from data
				AccessCheckRelation:  "admin",          // Override from data
				HistoryCheckObject:   "custom-history", // Override from data
				HistoryCheckRelation: "member",         // Override from data
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation"},
		},
		{
			name: "access control with computed defaults",
			parsedData: map[string]any{
				"uid": "project-defaults",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "project-defaults",
				Public:               false,
				AccessCheckObject:    "project:project-defaults", // Computed default
				AccessCheckRelation:  "viewer",                   // Computed default
				HistoryCheckObject:   "project:project-defaults", // Computed default
				HistoryCheckRelation: "writer",                   // Computed default
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation"},
		},
		{
			name: "complete enrichment with all fields and settings data",
			parsedData: map[string]any{
				"uid":                  "project-complete",
				"accessCheckObject":    "organization",
				"accessCheckRelation":  "admin",
				"historyCheckObject":   "organization",
				"historyCheckRelation": "admin",
				"settings": map[string]any{
					"feature_enabled": true,
					"config_value":    "production",
				},
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "project-complete",
				Public:               false,
				AccessCheckObject:    "organization",
				AccessCheckRelation:  "admin",
				HistoryCheckObject:   "organization",
				HistoryCheckRelation: "admin",
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation"},
		},
		{
			name: "project settings with configuration data",
			parsedData: map[string]any{
				"uid": "project-config",
				"configuration": map[string]any{
					"notifications_enabled": false,
					"email_frequency":       "weekly",
					"privacy_settings": map[string]any{
						"show_contributors": true,
					},
				},
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "project-config",
				Public:               false,
				AccessCheckObject:    "project:project-config",
				AccessCheckRelation:  "viewer",
				HistoryCheckObject:   "project:project-config",
				HistoryCheckRelation: "writer",
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation"},
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
		{
			name: "error: invalid id type",
			parsedData: map[string]any{
				"id": false,
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
				}
			}
		})
	}
}

func TestProjectSettingsEnricher_EnrichData_AccessControlComputedDefaults(t *testing.T) {
	enricher := NewProjectSettingsEnricher()
	body := &contracts.TransactionBody{}

	// Test that access control fields get computed defaults when not provided
	transaction := &contracts.LFXTransaction{
		ParsedData: map[string]any{
			"uid": "project-computed-defaults",
		},
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	assert.Equal(t, "project-computed-defaults", body.ObjectID)
	assert.False(t, body.Public) // Always false for project settings
	assert.Equal(t, "project:project-computed-defaults", body.AccessCheckObject)
	assert.Equal(t, "viewer", body.AccessCheckRelation)
	assert.Equal(t, "project:project-computed-defaults", body.HistoryCheckObject)
	assert.Equal(t, "writer", body.HistoryCheckRelation)
}

func TestProjectSettingsEnricher_EnrichData_AlwaysPrivate(t *testing.T) {
	enricher := NewProjectSettingsEnricher()
	body := &contracts.TransactionBody{}

	// Test that project settings are always private, regardless of input
	transaction := &contracts.LFXTransaction{
		ParsedData: map[string]any{
			"uid":    "project-privacy-test",
			"public": true, // This should be ignored
		},
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	assert.Equal(t, "project-privacy-test", body.ObjectID)
	assert.False(t, body.Public, "Project settings should always be private")
}

func TestProjectSettingsEnricher_EnrichData_BackwardsCompatibility(t *testing.T) {
	enricher := NewProjectSettingsEnricher()

	// Test that both 'uid' and 'id' fields work, with 'uid' taking precedence
	t.Run("uid takes precedence over id", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"uid": "project-uid",
				"id":  "project-id",
			},
		}

		err := enricher.EnrichData(body, transaction)
		require.NoError(t, err)
		assert.Equal(t, "project-uid", body.ObjectID, "uid should take precedence over id")
	})

	t.Run("fallback to id when uid missing", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"id": "project-legacy",
			},
		}

		err := enricher.EnrichData(body, transaction)
		require.NoError(t, err)
		assert.Equal(t, "project-legacy", body.ObjectID, "should fallback to id field")
	})
}

func TestProjectSettingsEnricher_EnrichData_EdgeCases(t *testing.T) {
	enricher := NewProjectSettingsEnricher()

	t.Run("empty string values for access control", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"uid":                  "project-empty-strings",
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

	t.Run("non-string access control values ignored", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"uid":                  "project-non-string",
				"accessCheckObject":    123,
				"accessCheckRelation":  false,
				"historyCheckObject":   nil,
				"historyCheckRelation": []string{"invalid"},
			},
		}

		err := enricher.EnrichData(body, transaction)
		require.NoError(t, err)

		// Non-string values should be ignored (left empty, no override)
		assert.Equal(t, "", body.AccessCheckObject)
		assert.Equal(t, "", body.AccessCheckRelation)
		assert.Equal(t, "", body.HistoryCheckObject)
		assert.Equal(t, "", body.HistoryCheckRelation)
	})

	t.Run("complex settings data preserved", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		complexData := map[string]any{
			"uid": "project-complex",
			"settings": map[string]any{
				"notifications": map[string]any{
					"email":  true,
					"slack":  false,
					"digest": "weekly",
				},
				"permissions": []string{"read", "write", "admin"},
				"metadata": map[string]any{
					"created_by": "admin@example.com",
					"version":    "1.0.0",
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
		assert.Equal(t, "project-complex", body.ObjectID)
		assert.False(t, body.Public)
	})

	t.Run("nil and missing fields handled correctly", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"uid":              "project-minimal",
				"some_other_field": nil,
			},
		}

		err := enricher.EnrichData(body, transaction)
		require.NoError(t, err)

		assert.Equal(t, "project-minimal", body.ObjectID)
		assert.False(t, body.Public)
		// Should have computed defaults since access control fields don't exist
		assert.Equal(t, "project:project-minimal", body.AccessCheckObject)
		assert.Equal(t, "viewer", body.AccessCheckRelation)
		assert.Equal(t, "project:project-minimal", body.HistoryCheckObject)
		assert.Equal(t, "writer", body.HistoryCheckRelation)
	})
}

func TestProjectSettingsEnricher_EnrichData_DataOwnership(t *testing.T) {
	enricher := NewProjectSettingsEnricher()
	body := &contracts.TransactionBody{}

	originalData := map[string]any{
		"uid": "project-data-test",
		"settings": map[string]any{
			"feature": "enabled",
		},
	}

	transaction := &contracts.LFXTransaction{
		ParsedData: originalData,
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	// Enricher should own the data assignment
	assert.Equal(t, originalData, body.Data, "Enricher should set the data on the body")
	assert.NotNil(t, body.Data, "Data should not be nil")
}
