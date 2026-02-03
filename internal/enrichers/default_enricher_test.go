// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package enrichers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultEnricher_ObjectType(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeProject)
	assert.Equal(t, constants.ObjectTypeProject, enricher.ObjectType())
}

func TestDefaultEnricher_EnrichData_NilValidation(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeCommittee)

	tests := []struct {
		name        string
		body        *contracts.TransactionBody
		transaction *contracts.LFXTransaction
		expectedErr string
	}{
		{
			name:        "nil body",
			body:        nil,
			transaction: &contracts.LFXTransaction{},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := enricher.EnrichData(tt.body, tt.transaction)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestDefaultEnricher_EnrichData_ObjectIDExtraction(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeCommittee)

	tests := []struct {
		name        string
		parsedData  map[string]any
		expectedID  string
		expectedErr string
	}{
		{
			name: "valid uid field",
			parsedData: map[string]any{
				"uid": "test-uid-123",
			},
			expectedID: "test-uid-123",
		},
		{
			name: "valid id field (fallback)",
			parsedData: map[string]any{
				"id": "test-id-456",
			},
			expectedID: "test-id-456",
		},
		{
			name: "uid takes precedence over id",
			parsedData: map[string]any{
				"uid": "test-uid-789",
				"id":  "test-id-789",
			},
			expectedID: "test-uid-789",
		},
		{
			name: "empty uid string",
			parsedData: map[string]any{
				"uid": "",
			},
			expectedErr: constants.ErrMappingUID,
		},
		{
			name: "non-string uid",
			parsedData: map[string]any{
				"uid": 123,
			},
			expectedErr: constants.ErrMappingUID,
		},
		{
			name:        "missing uid and id",
			parsedData:  map[string]any{},
			expectedErr: constants.ErrMappingUID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
			}

			err := enricher.EnrichData(body, transaction)

			if tt.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedID, body.ObjectID)
			}
		})
	}
}

func TestDefaultEnricher_EnrichData_PublicFlag(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeCommittee)

	tests := []struct {
		name           string
		parsedData     map[string]any
		expectedPublic bool
	}{
		{
			name: "public true",
			parsedData: map[string]any{
				"uid":    "test-123",
				"public": true,
			},
			expectedPublic: true,
		},
		{
			name: "public false",
			parsedData: map[string]any{
				"uid":    "test-123",
				"public": false,
			},
			expectedPublic: false,
		},
		{
			name: "missing public field defaults to false",
			parsedData: map[string]any{
				"uid": "test-123",
			},
			expectedPublic: false,
		},
		{
			name: "invalid public type defaults to false",
			parsedData: map[string]any{
				"uid":    "test-123",
				"public": "true", // string instead of bool
			},
			expectedPublic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedPublic, body.Public)
		})
	}
}

func TestDefaultEnricher_EnrichData_NameAndAliases(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeCommittee)

	tests := []struct {
		name             string
		parsedData       map[string]any
		expectedSortName string
		expectedAliases  []string
		expectedFulltext string
	}{
		{
			name: "name field only",
			parsedData: map[string]any{
				"uid":  "test-123",
				"name": "Test Project",
			},
			expectedSortName: "Test Project",
			expectedAliases:  []string{"Test Project"},
			expectedFulltext: "Test Project",
		},
		{
			name: "multiple name fields",
			parsedData: map[string]any{
				"uid":          "test-123",
				"name":         "Test Project",
				"title":        "Project Title",
				"display_name": "Display Name",
				"slug":         "test-slug",
			},
			expectedSortName: "Test Project",
			expectedAliases:  []string{"Display Name", "Project Title", "Test Project", "test-slug"},
			expectedFulltext: "Test Project Display Name Project Title test-slug",
		},
		{
			name: "duplicate names are deduplicated",
			parsedData: map[string]any{
				"uid":   "test-123",
				"name":  "Test Project",
				"title": "Test Project", // duplicate
				"alias": "Test Project", // duplicate
			},
			expectedSortName: "Test Project",
			expectedAliases:  []string{"Test Project"},
			expectedFulltext: "Test Project",
		},
		{
			name: "empty and whitespace names are filtered",
			parsedData: map[string]any{
				"uid":   "test-123",
				"name":  "Test Project",
				"title": "",      // empty
				"alias": "   ",   // whitespace
				"label": "Label", // valid
			},
			expectedSortName: "Test Project",
			expectedAliases:  []string{"Label", "Test Project"},
			expectedFulltext: "Test Project Label",
		},
		{
			name: "with description",
			parsedData: map[string]any{
				"uid":         "test-123",
				"name":        "Test Project",
				"description": "A test project for unit tests",
			},
			expectedSortName: "Test Project",
			expectedAliases:  []string{"Test Project"},
			expectedFulltext: "Test Project A test project for unit tests",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedSortName, body.SortName)
			assert.ElementsMatch(t, tt.expectedAliases, body.NameAndAliases)

			// For fulltext, check that all expected components are present
			// since the order depends on map iteration which is non-deterministic
			if tt.expectedFulltext != "" {
				expectedComponents := strings.Split(tt.expectedFulltext, " ")
				for _, component := range expectedComponents {
					if component != "" {
						assert.Contains(t, body.Fulltext, component, "fulltext should contain component: %s", component)
					}
				}
			}
		})
	}
}

func TestDefaultEnricher_EnrichData_SortName(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeCommittee)

	tests := []struct {
		name             string
		parsedData       map[string]any
		expectedSortName string
	}{
		{
			name: "prefers name field over others",
			parsedData: map[string]any{
				"uid":          "test-123",
				"name":         "Primary Name",
				"title":        "Title Value",
				"display_name": "Display Name",
				"label":        "Label Value",
			},
			expectedSortName: "Primary Name",
		},
		{
			name: "falls back to title when name missing",
			parsedData: map[string]any{
				"uid":          "test-123",
				"title":        "Title Value",
				"display_name": "Display Name",
				"label":        "Label Value",
			},
			expectedSortName: "Title Value",
		},
		{
			name: "falls back to display_name",
			parsedData: map[string]any{
				"uid":          "test-123",
				"display_name": "Display Name",
				"label":        "Label Value",
			},
			expectedSortName: "Display Name",
		},
		{
			name: "falls back to label",
			parsedData: map[string]any{
				"uid":   "test-123",
				"label": "Label Value",
			},
			expectedSortName: "Label Value",
		},
		{
			name: "returns empty when no name fields",
			parsedData: map[string]any{
				"uid":         "test-123",
				"description": "Some description",
			},
			expectedSortName: "",
		},
		{
			name: "trims whitespace",
			parsedData: map[string]any{
				"uid":  "test-123",
				"name": "  Trimmed Name  ",
			},
			expectedSortName: "Trimmed Name",
		},
		{
			name: "ignores empty strings",
			parsedData: map[string]any{
				"uid":   "test-123",
				"name":  "",
				"title": "Fallback Title",
			},
			expectedSortName: "Fallback Title",
		},
		{
			name: "ignores non-string values",
			parsedData: map[string]any{
				"uid":   "test-123",
				"name":  123,
				"title": "String Title",
			},
			expectedSortName: "String Title",
		},
		{
			name: "handles only whitespace returns empty",
			parsedData: map[string]any{
				"uid":   "test-123",
				"name":  "   ",
				"title": "Valid Title",
			},
			expectedSortName: "", // Whitespace-only name is trimmed to empty, doesn't fall back
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedSortName, body.SortName)
		})
	}
}

func TestDefaultEnricher_EnrichData_AccessControl(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeCommittee)

	tests := []struct {
		name           string
		parsedData     map[string]any
		objectType     string
		expectedAccess map[string]string
	}{
		{
			name: "computed defaults",
			parsedData: map[string]any{
				"uid": "test-123",
			},
			objectType: "project",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "project:test-123",
				"AccessCheckRelation":  "viewer",
				"HistoryCheckObject":   "project:test-123",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "project:test-123#viewer",
				"HistoryCheckQuery":    "project:test-123#writer",
			},
		},
		{
			name: "explicit values override defaults",
			parsedData: map[string]any{
				"uid":                  "test-123",
				"accessCheckObject":    "custom:object",
				"accessCheckRelation":  "admin",
				"historyCheckObject":   "history:object",
				"historyCheckRelation": "reader",
			},
			objectType: "project",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "custom:object",
				"AccessCheckRelation":  "admin",
				"HistoryCheckObject":   "history:object",
				"HistoryCheckRelation": "reader",
				"AccessCheckQuery":     "custom:object#admin",
				"HistoryCheckQuery":    "history:object#reader",
			},
		},
		{
			name: "empty string values are preserved",
			parsedData: map[string]any{
				"uid":                 "test-123",
				"accessCheckObject":   "", // empty but present
				"accessCheckRelation": "", // empty but present
			},
			objectType: "project",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "",                 // empty preserved
				"AccessCheckRelation":  "",                 // empty preserved
				"HistoryCheckObject":   "project:test-123", // computed default
				"HistoryCheckRelation": "writer",           // computed default
				"AccessCheckQuery":     "",                 // empty when both object and relation are empty
				"HistoryCheckQuery":    "project:test-123#writer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
				ObjectType: tt.objectType,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedAccess["AccessCheckObject"], body.AccessCheckObject)
			assert.Equal(t, tt.expectedAccess["AccessCheckRelation"], body.AccessCheckRelation)
			assert.Equal(t, tt.expectedAccess["HistoryCheckObject"], body.HistoryCheckObject)
			assert.Equal(t, tt.expectedAccess["HistoryCheckRelation"], body.HistoryCheckRelation)
			assert.Equal(t, tt.expectedAccess["AccessCheckQuery"], body.AccessCheckQuery)
			assert.Equal(t, tt.expectedAccess["HistoryCheckQuery"], body.HistoryCheckQuery)
		})
	}
}

func TestDefaultEnricher_EnrichData_ParentReferences(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeCommittee)

	tests := []struct {
		name            string
		parsedData      map[string]any
		objectType      string
		expectedParents []string
	}{
		{
			name: "parent_uid field",
			parsedData: map[string]any{
				"uid":        "child-123",
				"parent_uid": "parent-456",
			},
			objectType:      "project",
			expectedParents: []string{"project:parent-456"},
		},
		{
			name: "legacy parentID field",
			parsedData: map[string]any{
				"uid":      "child-123",
				"parentID": "parent-789",
			},
			objectType:      "project",
			expectedParents: []string{"project:parent-789"},
		},
		{
			name: "both parent fields (no duplicates)",
			parsedData: map[string]any{
				"uid":        "child-123",
				"parent_uid": "parent-456",
				"parentID":   "parent-456", // same parent
			},
			objectType:      "committee",
			expectedParents: []string{"committee:parent-456"},
		},
		{
			name: "both parent fields (different parents)",
			parsedData: map[string]any{
				"uid":        "child-123",
				"parent_uid": "parent-456",
				"parentID":   "parent-789",
			},
			objectType:      "project",
			expectedParents: []string{"project:parent-456", "project:parent-789"},
		},
		{
			name: "empty parent fields are ignored",
			parsedData: map[string]any{
				"uid":        "child-123",
				"parent_uid": "",
				"parentID":   "",
			},
			objectType:      "project",
			expectedParents: nil,
		},
		{
			name: "no parent fields",
			parsedData: map[string]any{
				"uid": "child-123",
			},
			objectType:      "project",
			expectedParents: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
				ObjectType: tt.objectType,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedParents, body.ParentRefs)
		})
	}
}

func TestDefaultSetParentReferences(t *testing.T) {
	tests := []struct {
		name            string
		data            map[string]any
		objectType      string
		expectedParents []string
	}{
		{
			name: "committee_uid field pattern",
			data: map[string]any{
				"uid":           "test-123",
				"committee_uid": "committee-456",
			},
			objectType:      "meeting",
			expectedParents: []string{"committee:committee-456"},
		},
		{
			name: "project_uid field pattern",
			data: map[string]any{
				"uid":         "test-123",
				"project_uid": "project-789",
			},
			objectType:      "meeting",
			expectedParents: []string{"project:project-789"},
		},
		{
			name: "meeting_uid field pattern",
			data: map[string]any{
				"uid":         "test-123",
				"meeting_uid": "meeting-abc",
			},
			objectType:      "participant",
			expectedParents: []string{"meeting:meeting-abc"},
		},
		{
			name: "ProjectID field pattern",
			data: map[string]any{
				"uid":       "test-123",
				"ProjectID": "project-def",
			},
			objectType:      "meeting",
			expectedParents: []string{"project:project-def"},
		},
		{
			name: "CommitteeID field pattern",
			data: map[string]any{
				"uid":         "test-123",
				"CommitteeID": "committee-ghi",
			},
			objectType:      "meeting",
			expectedParents: []string{"committee:committee-ghi"},
		},
		{
			name: "multiple parent references",
			data: map[string]any{
				"uid":           "test-123",
				"committee_uid": "committee-456",
				"project_uid":   "project-789",
				"meeting_uid":   "meeting-abc",
			},
			objectType: "event",
			expectedParents: []string{
				"committee:committee-456",
				"project:project-789",
				"meeting:meeting-abc",
			},
		},
		{
			name: "mixed *_uid and *ID patterns",
			data: map[string]any{
				"uid":           "test-123",
				"committee_uid": "committee-456",
				"ProjectID":     "project-789",
				"MeetingID":     "meeting-abc",
			},
			objectType: "event",
			expectedParents: []string{
				"committee:committee-456",
				"project:project-789",
				"meeting:meeting-abc",
			},
		},
		{
			name: "case insensitive pattern matching",
			data: map[string]any{
				"uid":           "test-123",
				"COMMITTEE_uid": "committee-456", // uppercase
				"project_uid":   "project-789",   // mixed case
			},
			objectType: "meeting",
			expectedParents: []string{
				"committee:committee-456",
				"project:project-789",
			},
		},
		{
			name: "non-string values are included",
			data: map[string]any{
				"uid":           "test-123",
				"committee_uid": 12345, // number instead of string
				"ProjectID":     false, // boolean instead of string
			},
			objectType: "meeting",
			expectedParents: []string{
				"committee:12345",
				"project:false",
			},
		},
		{
			name: "empty string values are skipped",
			data: map[string]any{
				"uid":           "test-123",
				"project_uid":   "",         // empty string should be skipped
				"committee_uid": "comm-456", // valid value
				"MeetingID":     "",         // empty string should be skipped
			},
			objectType: "event",
			expectedParents: []string{
				"committee:comm-456", // Only non-empty value should be included
			},
		},
		{
			name: "all empty string values result in no parent refs",
			data: map[string]any{
				"uid":           "test-123",
				"project_uid":   "",
				"committee_uid": "",
				"MeetingID":     "",
			},
			objectType:      "event",
			expectedParents: nil,
		},
		{
			name: "nil values are skipped",
			data: map[string]any{
				"uid":           "test-123",
				"project_uid":   nil,        // nil value should be skipped
				"committee_uid": "comm-789", // valid value
				"meeting_uid":   nil,        // nil value should be skipped
			},
			objectType: "event",
			expectedParents: []string{
				"committee:comm-789", // Only non-nil value should be included
			},
		},
		{
			name: "mixed nil, empty string, and valid values",
			data: map[string]any{
				"uid":           "test-123",
				"project_uid":   nil,        // nil - should be skipped
				"committee_uid": "",         // empty string - should be skipped
				"meeting_uid":   "meet-456", // valid value
				"ProjectID":     nil,        // nil - should be skipped
			},
			objectType: "event",
			expectedParents: []string{
				"meeting:meet-456", // Only valid value should be included
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}

			// Directly call the function being tested
			defaultSetParentReferences(body, tt.data, tt.objectType)

			// If there are expected parents, check that they're all present
			if len(tt.expectedParents) > 0 {
				require.NotNil(t, body.ParentRefs, "ParentRefs should not be nil")
				assert.Len(t, body.ParentRefs, len(tt.expectedParents), "Should have expected number of parent references")

				// Check that each expected parent is in the result
				for _, expectedParent := range tt.expectedParents {
					assert.Contains(t, body.ParentRefs, expectedParent, "Should contain parent reference %s", expectedParent)
				}
			} else {
				// If no parents are expected, ParentRefs should be nil or empty
				if body.ParentRefs != nil {
					assert.Empty(t, body.ParentRefs, "ParentRefs should be empty when no parent fields")
				}
			}
		})
	}
}

func TestDefaultEnricher_EnrichData_CompleteEnrichment(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeCommittee)

	parsedData := map[string]any{
		"uid":                  "complete-test-123",
		"name":                 "Complete Test Project",
		"title":                "Test Title",
		"slug":                 "complete-test",
		"description":          "A complete test project with all fields",
		"public":               true,
		"parent_uid":           "parent-project-456",
		"accessCheckObject":    "organization:test-org",
		"accessCheckRelation":  "member",
		"historyCheckObject":   "organization:test-org",
		"historyCheckRelation": "admin",
		"custom_field":         "custom_value",
	}

	body := &contracts.TransactionBody{}
	transaction := &contracts.LFXTransaction{
		ParsedData: parsedData,
		ObjectType: "project",
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	// Verify all fields are set correctly
	assert.Equal(t, parsedData, body.Data)
	assert.Equal(t, "project", body.ObjectType)
	assert.Equal(t, "complete-test-123", body.ObjectID)
	assert.Equal(t, true, body.Public)
	assert.Equal(t, "Complete Test Project", body.SortName)
	assert.ElementsMatch(t, []string{"Complete Test Project", "Test Title", "complete-test"}, body.NameAndAliases)
	assert.Equal(t, []string{"project:parent-project-456"}, body.ParentRefs)
	assert.Equal(t, "organization:test-org", body.AccessCheckObject)
	assert.Equal(t, "member", body.AccessCheckRelation)
	assert.Equal(t, "organization:test-org", body.HistoryCheckObject)
	assert.Equal(t, "admin", body.HistoryCheckRelation)
	assert.Equal(t, "organization:test-org#member", body.AccessCheckQuery)
	assert.Equal(t, "organization:test-org#admin", body.HistoryCheckQuery)

	// Verify fulltext includes all searchable content
	// Note: Order may vary due to map iteration, so check components individually
	expectedComponents := []string{"Complete Test Project", "Test Title", "complete-test", "A complete test project with all fields"}
	for _, component := range expectedComponents {
		assert.Contains(t, body.Fulltext, component, "fulltext should contain component: %s", component)
	}
}

func TestDefaultEnricher_EnrichData_EdgeCases(t *testing.T) {
	enricher := newDefaultEnricher(constants.ObjectTypeCommittee)

	tests := []struct {
		name       string
		parsedData map[string]any
		assertions func(t *testing.T, body *contracts.TransactionBody)
	}{
		{
			name: "minimal valid data",
			parsedData: map[string]any{
				"uid": "minimal-123",
			},
			assertions: func(t *testing.T, body *contracts.TransactionBody) {
				assert.Equal(t, "minimal-123", body.ObjectID)
				assert.Equal(t, false, body.Public)
				assert.Equal(t, "", body.SortName)
				assert.Empty(t, body.NameAndAliases)
				assert.Equal(t, "project:minimal-123", body.AccessCheckObject)
				assert.Equal(t, "viewer", body.AccessCheckRelation)
				assert.Equal(t, "project:minimal-123#viewer", body.AccessCheckQuery)
				assert.Equal(t, "project:minimal-123#writer", body.HistoryCheckQuery)
			},
		},
		{
			name: "non-string name fields are ignored",
			parsedData: map[string]any{
				"uid":   "test-123",
				"name":  123,                               // number
				"title": []string{"array"},                 // array
				"label": map[string]string{"key": "value"}, // map
			},
			assertions: func(t *testing.T, body *contracts.TransactionBody) {
				assert.Equal(t, "", body.SortName)
				assert.Empty(t, body.NameAndAliases)
			},
		},
		{
			name: "whitespace trimming",
			parsedData: map[string]any{
				"uid":         "test-123",
				"name":        "  Trimmed Name  ",
				"description": "  Trimmed Description  ",
			},
			assertions: func(t *testing.T, body *contracts.TransactionBody) {
				assert.Equal(t, "Trimmed Name", body.SortName)
				assert.ElementsMatch(t, []string{"Trimmed Name"}, body.NameAndAliases)
				assert.True(t, strings.Contains(body.Fulltext, "Trimmed Description"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
				ObjectType: "project",
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)
			tt.assertions(t, body)
		})
	}
}

func TestDefaultEnricher_WithAccessControl_Option(t *testing.T) {
	// Custom access control function that uses "auditor" instead of "viewer" as default
	customAccessControl := func(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
		// Access check object - same as default
		if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
			body.AccessCheckObject = accessCheckObject
		} else if _, exists := data["accessCheckObject"]; !exists {
			body.AccessCheckObject = fmt.Sprintf("%s:%s", objectType, objectID)
		}

		// Access check relation - CUSTOM: use "auditor" instead of "viewer"
		if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
			body.AccessCheckRelation = accessCheckRelation
		} else if _, exists := data["accessCheckRelation"]; !exists {
			body.AccessCheckRelation = "auditor" // Custom default!
		}

		// History check object - same as default
		if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
			body.HistoryCheckObject = historyCheckObject
		} else if _, exists := data["historyCheckObject"]; !exists {
			body.HistoryCheckObject = fmt.Sprintf("%s:%s", objectType, objectID)
		}

		// History check relation - same as default
		if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
			body.HistoryCheckRelation = historyCheckRelation
		} else if _, exists := data["historyCheckRelation"]; !exists {
			body.HistoryCheckRelation = "writer"
		}
	}

	tests := []struct {
		name     string
		data     map[string]any
		expected struct {
			accessCheckObject    string
			accessCheckRelation  string
			historyCheckObject   string
			historyCheckRelation string
		}
	}{
		{
			name: "uses custom default for accessCheckRelation",
			data: map[string]any{
				"uid": "test-123",
			},
			expected: struct {
				accessCheckObject    string
				accessCheckRelation  string
				historyCheckObject   string
				historyCheckRelation string
			}{
				accessCheckObject:    "committee:test-123",
				accessCheckRelation:  "auditor", // Custom default instead of "viewer"
				historyCheckObject:   "committee:test-123",
				historyCheckRelation: "writer",
			},
		},
		{
			name: "respects explicit accessCheckRelation value",
			data: map[string]any{
				"uid":                 "test-456",
				"accessCheckRelation": "admin",
			},
			expected: struct {
				accessCheckObject    string
				accessCheckRelation  string
				historyCheckObject   string
				historyCheckRelation string
			}{
				accessCheckObject:    "committee:test-456",
				accessCheckRelation:  "admin", // Explicit value is preserved
				historyCheckObject:   "committee:test-456",
				historyCheckRelation: "writer",
			},
		},
		{
			name: "preserves other custom access control values",
			data: map[string]any{
				"uid":                  "test-789",
				"accessCheckObject":    "custom:object",
				"historyCheckObject":   "custom:history",
				"historyCheckRelation": "custom-writer",
			},
			expected: struct {
				accessCheckObject    string
				accessCheckRelation  string
				historyCheckObject   string
				historyCheckRelation string
			}{
				accessCheckObject:    "custom:object",
				accessCheckRelation:  "auditor", // Still uses custom default
				historyCheckObject:   "custom:history",
				historyCheckRelation: "custom-writer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create enricher with custom access control
			enricher := newDefaultEnricher(constants.ObjectTypeCommittee, WithAccessControl(customAccessControl))

			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ObjectType: constants.ObjectTypeCommittee,
				ParsedData: tt.data,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)

			// Verify custom access control behavior
			assert.Equal(t, tt.expected.accessCheckObject, body.AccessCheckObject, "AccessCheckObject should match expected")
			assert.Equal(t, tt.expected.accessCheckRelation, body.AccessCheckRelation, "AccessCheckRelation should use custom default")
			assert.Equal(t, tt.expected.historyCheckObject, body.HistoryCheckObject, "HistoryCheckObject should match expected")
			assert.Equal(t, tt.expected.historyCheckRelation, body.HistoryCheckRelation, "HistoryCheckRelation should match expected")
		})
	}
}

func TestDefaultEnricher_WithAccessControl_vs_Default(t *testing.T) {
	data := map[string]any{
		"uid": "test-comparison",
	}

	// Test default enricher
	defaultEnricher := newDefaultEnricher(constants.ObjectTypeCommittee)
	defaultBody := &contracts.TransactionBody{}
	defaultTransaction := &contracts.LFXTransaction{
		ObjectType: constants.ObjectTypeCommittee,
		ParsedData: data,
	}

	err := defaultEnricher.EnrichData(defaultBody, defaultTransaction)
	require.NoError(t, err)

	// Test custom enricher with WithAccessControl option
	customAccessControl := func(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
		if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
			body.AccessCheckRelation = accessCheckRelation
		} else if _, exists := data["accessCheckRelation"]; !exists {
			body.AccessCheckRelation = "auditor" // Different from default "viewer"
		}

		// Set other fields same as default for this test
		if _, exists := data["accessCheckObject"]; !exists {
			body.AccessCheckObject = fmt.Sprintf("%s:%s", objectType, objectID)
		}
		if _, exists := data["historyCheckObject"]; !exists {
			body.HistoryCheckObject = fmt.Sprintf("%s:%s", objectType, objectID)
		}
		if _, exists := data["historyCheckRelation"]; !exists {
			body.HistoryCheckRelation = "writer"
		}
	}

	customEnricher := newDefaultEnricher(constants.ObjectTypeCommittee, WithAccessControl(customAccessControl))
	customBody := &contracts.TransactionBody{}
	customTransaction := &contracts.LFXTransaction{
		ObjectType: constants.ObjectTypeCommittee,
		ParsedData: data,
	}

	err = customEnricher.EnrichData(customBody, customTransaction)
	require.NoError(t, err)

	// Verify the difference
	assert.Equal(t, "viewer", defaultBody.AccessCheckRelation, "Default enricher should use 'viewer'")
	assert.Equal(t, "auditor", customBody.AccessCheckRelation, "Custom enricher should use 'auditor'")

	// Verify other fields are the same
	assert.Equal(t, defaultBody.AccessCheckObject, customBody.AccessCheckObject, "AccessCheckObject should be the same")
	assert.Equal(t, defaultBody.HistoryCheckObject, customBody.HistoryCheckObject, "HistoryCheckObject should be the same")
	assert.Equal(t, defaultBody.HistoryCheckRelation, customBody.HistoryCheckRelation, "HistoryCheckRelation should be the same")
}
