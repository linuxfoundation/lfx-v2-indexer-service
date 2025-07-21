// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package enrichers

import (
	"testing"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectEnricher_EnrichData(t *testing.T) {
	enricher := &ProjectEnricher{}

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
				"uid":    "project-123",
				"public": true,
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "project-123",
				Public:   true,
			},
			expectedFields: []string{"ObjectID", "Public"},
		},
		{
			name: "successful enrichment with legacy id field",
			parsedData: map[string]any{
				"id":     "project-456",
				"public": false,
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "project-456",
				Public:   false,
			},
			expectedFields: []string{"ObjectID", "Public"},
		},
		{
			name: "successful enrichment with access control attributes",
			parsedData: map[string]any{
				"uid":                  "project-789",
				"public":               true,
				"accessCheckObject":    "project",
				"accessCheckRelation":  "member",
				"historyCheckObject":   "project",
				"historyCheckRelation": "viewer",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "project-789",
				Public:               true,
				AccessCheckObject:    "project", // Override from data
				AccessCheckRelation:  "member",  // Override from data
				HistoryCheckObject:   "project", // Override from data
				HistoryCheckRelation: "viewer",  // Override from data
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation"},
		},
		{
			name: "access control with computed defaults",
			parsedData: map[string]any{
				"uid":    "project-defaults",
				"public": true,
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "project-defaults",
				Public:               true,
				AccessCheckObject:    "project:project-defaults", // Computed default
				AccessCheckRelation:  "viewer",                   // Computed default
				HistoryCheckObject:   "project:project-defaults", // Computed default
				HistoryCheckRelation: "writer",                   // Computed default
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation"},
		},
		{
			name: "successful enrichment with parent reference",
			parsedData: map[string]any{
				"uid":      "project-child",
				"public":   true,
				"parentID": "project-parent",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:   "project-child",
				Public:     true,
				ParentRefs: []string{"project-parent"},
			},
			expectedFields: []string{"ObjectID", "Public", "ParentRefs"},
		},
		{
			name: "successful enrichment with empty parent reference",
			parsedData: map[string]any{
				"uid":      "project-orphan",
				"public":   true,
				"parentID": "",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:   "project-orphan",
				Public:     true,
				ParentRefs: nil,
			},
			expectedFields: []string{"ObjectID", "Public"},
		},
		{
			name: "complete enrichment with all fields",
			parsedData: map[string]any{
				"uid":                  "project-complete",
				"public":               false,
				"accessCheckObject":    "organization",
				"accessCheckRelation":  "admin",
				"historyCheckObject":   "organization",
				"historyCheckRelation": "member",
				"parentID":             "org-parent",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:             "project-complete",
				Public:               false,
				AccessCheckObject:    "organization",
				AccessCheckRelation:  "admin",
				HistoryCheckObject:   "organization",
				HistoryCheckRelation: "member",
				ParentRefs:           []string{"org-parent"},
			},
			expectedFields: []string{"ObjectID", "Public", "AccessCheckObject", "AccessCheckRelation", "HistoryCheckObject", "HistoryCheckRelation", "ParentRefs"},
		},
		{
			name: "complete enrichment with search fields",
			parsedData: map[string]any{
				"uid":         "project-search",
				"public":      true,
				"name":        "Example Project",
				"slug":        "example-project",
				"description": "A sample project for testing search functionality",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:       "project-search",
				Public:         true,
				SortName:       "Example Project",
				NameAndAliases: []string{"Example Project", "example-project"},
				Fulltext:       "Example Project example-project A sample project for testing search functionality",
			},
			expectedFields: []string{"ObjectID", "Public", "SortName", "NameAndAliases", "Fulltext"},
		},
		{
			name: "parent reference with legacy field",
			parsedData: map[string]any{
				"uid":        "project-child-legacy",
				"public":     false,
				"parent_uid": "parent-legacy",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:   "project-child-legacy",
				Public:     false,
				ParentRefs: []string{"project:parent-legacy"},
			},
			expectedFields: []string{"ObjectID", "Public", "ParentRefs"},
		},
		{
			name: "error: missing uid and id",
			parsedData: map[string]any{
				"public": true,
			},
			expectedError: constants.ErrMappingUID,
		},
		{
			name: "error: invalid uid type",
			parsedData: map[string]any{
				"uid":    123,
				"public": true,
			},
			expectedError: constants.ErrMappingUID,
		},
		{
			name: "error: missing public field",
			parsedData: map[string]any{
				"uid": "project-missing-public",
			},
			expectedError: constants.ErrMappingPublic,
		},
		{
			name: "error: invalid public type",
			parsedData: map[string]any{
				"uid":    "project-invalid-public",
				"public": "not-a-boolean",
			},
			expectedError: constants.ErrMappingPublic,
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
				case "ParentRefs":
					assert.Equal(t, tt.expectedBody.ParentRefs, body.ParentRefs, "ParentRefs mismatch")
				case "SortName":
					assert.Equal(t, tt.expectedBody.SortName, body.SortName, "SortName mismatch")
				case "NameAndAliases":
					assert.Equal(t, tt.expectedBody.NameAndAliases, body.NameAndAliases, "NameAndAliases mismatch")
				case "Fulltext":
					assert.Equal(t, tt.expectedBody.Fulltext, body.Fulltext, "Fulltext mismatch")
				}
			}
		})
	}
}

func TestProjectEnricher_EnrichData_AccessControlComputedDefaults(t *testing.T) {
	enricher := &ProjectEnricher{}
	body := &contracts.TransactionBody{}

	// Test that access control fields get computed defaults when not provided
	transaction := &contracts.LFXTransaction{
		ParsedData: map[string]any{
			"uid":    "project-computed-defaults",
			"public": true,
		},
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	assert.Equal(t, "project-computed-defaults", body.ObjectID)
	assert.True(t, body.Public)
	assert.Equal(t, "project:project-computed-defaults", body.AccessCheckObject)
	assert.Equal(t, "viewer", body.AccessCheckRelation)
	assert.Equal(t, "project:project-computed-defaults", body.HistoryCheckObject)
	assert.Equal(t, "writer", body.HistoryCheckRelation)
}

func TestProjectEnricher_EnrichData_ParentReferenceOptional(t *testing.T) {
	enricher := &ProjectEnricher{}
	body := &contracts.TransactionBody{}

	// Test that parent reference is optional
	transaction := &contracts.LFXTransaction{
		ParsedData: map[string]any{
			"uid":    "project-no-parent",
			"public": false,
		},
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	assert.Equal(t, "project-no-parent", body.ObjectID)
	assert.False(t, body.Public)
	assert.Nil(t, body.ParentRefs)
}

func TestProjectEnricher_EnrichData_BackwardsCompatibility(t *testing.T) {
	enricher := &ProjectEnricher{}

	// Test that both 'uid' and 'id' fields work, with 'uid' taking precedence
	t.Run("uid takes precedence over id", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"uid":    "project-uid",
				"id":     "project-id",
				"public": true,
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
				"id":     "project-legacy",
				"public": true,
			},
		}

		err := enricher.EnrichData(body, transaction)
		require.NoError(t, err)
		assert.Equal(t, "project-legacy", body.ObjectID, "should fallback to id field")
	})
}

func TestProjectEnricher_EnrichData_EdgeCases(t *testing.T) {
	enricher := &ProjectEnricher{}

	t.Run("empty string values for access control", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"uid":                  "project-empty-strings",
				"public":               true,
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
				"public":               true,
				"accessCheckObject":    123,
				"accessCheckRelation":  false,
				"historyCheckObject":   nil,
				"historyCheckRelation": []string{"invalid"},
			},
		}

		err := enricher.EnrichData(body, transaction)
		require.NoError(t, err)

		// Non-string values should be ignored (not set)
		assert.Empty(t, body.AccessCheckObject)
		assert.Empty(t, body.AccessCheckRelation)
		assert.Empty(t, body.HistoryCheckObject)
		assert.Empty(t, body.HistoryCheckRelation)
	})

	t.Run("non-string parent ID ignored", func(t *testing.T) {
		body := &contracts.TransactionBody{}
		transaction := &contracts.LFXTransaction{
			ParsedData: map[string]any{
				"uid":      "project-non-string-parent",
				"public":   true,
				"parentID": 123,
			},
		}

		err := enricher.EnrichData(body, transaction)
		require.NoError(t, err)

		// Non-string parent ID should be ignored
		assert.Nil(t, body.ParentRefs)
	})
}
