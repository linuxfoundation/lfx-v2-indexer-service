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

func TestCommitteeMemberEnricher_setAccessControl(t *testing.T) {
	enricher := &CommitteeMemberEnricher{}

	tests := []struct {
		name           string
		data           map[string]any
		objectType     string
		objectID       string
		expectedAccess map[string]string
	}{
		{
			name: "with committee_uid - uses committee permission",
			data: map[string]any{
				"committee_uid": "committee-123",
			},
			objectType: "committee_member",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee:committee-123",
				"AccessCheckRelation":  "viewer",
				"HistoryCheckObject":   "committee:committee-123",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee:committee-123#viewer",
				"HistoryCheckQuery":    "committee:committee-123#writer",
			},
		},
		{
			name: "without committee_uid - falls back to object permission",
			data: map[string]any{
				"user_id": "user-789",
			},
			objectType: "committee_member",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee_member:member-456",
				"AccessCheckRelation":  "viewer",
				"HistoryCheckObject":   "committee_member:member-456",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee_member:member-456#viewer",
				"HistoryCheckQuery":    "committee_member:member-456#writer",
			},
		},
		{
			name: "committee_uid empty string - uses empty committee permission",
			data: map[string]any{
				"committee_uid": "",
				"user_id":       "user-789",
			},
			objectType: "committee_member",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee:",
				"AccessCheckRelation":  "viewer",
				"HistoryCheckObject":   "committee:",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee:#viewer",
				"HistoryCheckQuery":    "committee:#writer",
			},
		},
		{
			name: "committee_uid non-string - falls back to object permission",
			data: map[string]any{
				"committee_uid": 12345,
				"user_id":       "user-789",
			},
			objectType: "committee_member",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee_member:member-456",
				"AccessCheckRelation":  "viewer",
				"HistoryCheckObject":   "committee_member:member-456",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee_member:member-456#viewer",
				"HistoryCheckQuery":    "committee_member:member-456#writer",
			},
		},
		{
			name: "explicit access control values override defaults",
			data: map[string]any{
				"committee_uid":        "committee-123",
				"accessCheckObject":    "custom:object",
				"accessCheckRelation":  "admin",
				"historyCheckObject":   "custom:history",
				"historyCheckRelation": "reader",
			},
			objectType: "committee_member",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "custom:object",
				"AccessCheckRelation":  "admin",
				"HistoryCheckObject":   "custom:history",
				"HistoryCheckRelation": "reader",
				"AccessCheckQuery":     "custom:object#admin",
				"HistoryCheckQuery":    "custom:history#reader",
			},
		},
		{
			name: "empty string access control values are preserved",
			data: map[string]any{
				"committee_uid":       "committee-123",
				"accessCheckObject":   "", // empty but present
				"accessCheckRelation": "", // empty but present
			},
			objectType: "committee_member",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "",                        // empty preserved
				"AccessCheckRelation":  "",                        // empty preserved
				"HistoryCheckObject":   "committee:committee-123", // computed default
				"HistoryCheckRelation": "writer",                  // computed default
				"AccessCheckQuery":     "",                        // empty when both object and relation are empty
				"HistoryCheckQuery":    "committee:committee-123#writer",
			},
		},
		{
			name: "partial explicit values - mix of explicit and computed",
			data: map[string]any{
				"committee_uid":      "committee-789",
				"accessCheckObject":  "explicit:access",
				"historyCheckObject": "explicit:history",
				// accessCheckRelation and historyCheckRelation will use defaults
			},
			objectType: "committee_member",
			objectID:   "member-999",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "explicit:access",  // explicit
				"AccessCheckRelation":  "viewer",           // default
				"HistoryCheckObject":   "explicit:history", // explicit
				"HistoryCheckRelation": "writer",           // default
				"AccessCheckQuery":     "explicit:access#viewer",
				"HistoryCheckQuery":    "explicit:history#writer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}

			enricher.setAccessControl(body, tt.data, tt.objectType, tt.objectID)

			assert.Equal(t, tt.expectedAccess["AccessCheckObject"], body.AccessCheckObject)
			assert.Equal(t, tt.expectedAccess["AccessCheckRelation"], body.AccessCheckRelation)
			assert.Equal(t, tt.expectedAccess["HistoryCheckObject"], body.HistoryCheckObject)
			assert.Equal(t, tt.expectedAccess["HistoryCheckRelation"], body.HistoryCheckRelation)

			// Check new query fields if expected
			if expectedQuery, exists := tt.expectedAccess["AccessCheckQuery"]; exists {
				assert.Equal(t, expectedQuery, body.AccessCheckQuery)
			}
			if expectedQuery, exists := tt.expectedAccess["HistoryCheckQuery"]; exists {
				assert.Equal(t, expectedQuery, body.HistoryCheckQuery)
			}
		})
	}
}

func TestCommitteeMemberEnricher_settExtractNameAndAliases(t *testing.T) {
	enricher := &CommitteeMemberEnricher{}

	tests := []struct {
		name            string
		data            map[string]any
		expectedAliases []string
	}{
		{
			name: "committee member specific fields",
			data: map[string]any{
				"committee_name": "Engineering Committee",
				"first_name":     "John",
				"last_name":      "Doe",
				"username":       "johndoe",
			},
			expectedAliases: []string{"Engineering Committee", "John", "Doe", "johndoe"},
		},
		{
			name: "partial fields - missing some name fields",
			data: map[string]any{
				"committee_name": "Marketing Committee",
				"first_name":     "Jane",
				"user_id":        "user-123", // not a name field
			},
			expectedAliases: []string{"Marketing Committee", "Jane"},
		},
		{
			name: "empty and whitespace values are filtered",
			data: map[string]any{
				"committee_name": "Valid Committee",
				"first_name":     "",       // empty
				"last_name":      "   ",    // whitespace only
				"username":       "valid",  // valid
				"other_field":    "ignore", // not a name field
			},
			expectedAliases: []string{"Valid Committee", "valid"},
		},
		{
			name: "duplicate names are deduplicated",
			data: map[string]any{
				"committee_name": "Duplicate Committee",
				"first_name":     "Duplicate Committee", // same as committee_name
				"last_name":      "Smith",
				"username":       "Smith", // same as last_name
			},
			expectedAliases: []string{"Duplicate Committee", "Smith"},
		},
		{
			name: "non-string values are ignored",
			data: map[string]any{
				"committee_name": 12345,                       // number
				"first_name":     []string{"array"},           // array
				"last_name":      map[string]string{"k": "v"}, // map
				"username":       "valid_username",            // valid string
			},
			expectedAliases: []string{"valid_username"},
		},
		{
			name: "case insensitive field matching",
			data: map[string]any{
				"Committee_Name": "Case Test Committee", // different case
				"FIRST_NAME":     "CaseTest",            // uppercase
				"Last_Name":      "User",                // mixed case
				"UserName":       "caseuser",            // camelCase
			},
			expectedAliases: []string{"Case Test Committee", "CaseTest", "User", "caseuser"},
		},
		{
			name: "no matching fields",
			data: map[string]any{
				"user_id":      "user-123",
				"committee_id": "committee-456",
				"role":         "member",
			},
			expectedAliases: nil,
		},
		{
			name:            "empty data",
			data:            map[string]any{},
			expectedAliases: nil,
		},
		{
			name: "trimming whitespace",
			data: map[string]any{
				"committee_name": "  Trimmed Committee  ",
				"first_name":     "  John  ",
				"last_name":      "  Doe  ",
				"username":       "  johndoe  ",
			},
			expectedAliases: []string{"Trimmed Committee", "John", "Doe", "johndoe"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enricher.settExtractNameAndAliases(tt.data)

			if tt.expectedAliases == nil {
				assert.Nil(t, result)
			} else {
				assert.ElementsMatch(t, tt.expectedAliases, result)
			}
		})
	}
}

func TestCommitteeMemberEnricher_extractSortName(t *testing.T) {
	enricher := &CommitteeMemberEnricher{}

	tests := []struct {
		name             string
		data             map[string]any
		expectedSortName string
	}{
		{
			name: "valid first_name",
			data: map[string]any{
				"first_name": "John",
				"last_name":  "Doe", // should be ignored
			},
			expectedSortName: "John",
		},
		{
			name: "first_name with whitespace",
			data: map[string]any{
				"first_name": "  Jane  ",
			},
			expectedSortName: "Jane",
		},
		{
			name: "empty first_name",
			data: map[string]any{
				"first_name": "",
				"last_name":  "Smith", // should be ignored
			},
			expectedSortName: "",
		},
		{
			name: "first_name with only whitespace",
			data: map[string]any{
				"first_name": "   ",
			},
			expectedSortName: "",
		},
		{
			name: "non-string first_name",
			data: map[string]any{
				"first_name": 12345,
			},
			expectedSortName: "",
		},
		{
			name: "missing first_name field",
			data: map[string]any{
				"last_name": "Johnson",
				"username":  "johnsonuser",
			},
			expectedSortName: "",
		},
		{
			name: "first_name is array",
			data: map[string]any{
				"first_name": []string{"John", "Jane"},
			},
			expectedSortName: "",
		},
		{
			name: "first_name is map",
			data: map[string]any{
				"first_name": map[string]string{"name": "John"},
			},
			expectedSortName: "",
		},
		{
			name:             "empty data",
			data:             map[string]any{},
			expectedSortName: "",
		},
		{
			name: "first_name is nil",
			data: map[string]any{
				"first_name": nil,
			},
			expectedSortName: "",
		},
		{
			name: "first_name with accented characters",
			data: map[string]any{
				"first_name": "José",
			},
			expectedSortName: "José",
		},
		{
			name: "first_name with umlauts",
			data: map[string]any{
				"first_name": "Björn",
			},
			expectedSortName: "Björn",
		},
		{
			name: "first_name with cedilla",
			data: map[string]any{
				"first_name": "François",
			},
			expectedSortName: "François",
		},
		{
			name: "first_name with Chinese characters",
			data: map[string]any{
				"first_name": "李明",
			},
			expectedSortName: "李明",
		},
		{
			name: "first_name with Japanese characters",
			data: map[string]any{
				"first_name": "田中太郎",
			},
			expectedSortName: "田中太郎",
		},
		{
			name: "first_name with Arabic characters",
			data: map[string]any{
				"first_name": "محمد",
			},
			expectedSortName: "محمد",
		},
		{
			name: "first_name with Cyrillic characters",
			data: map[string]any{
				"first_name": "Александр",
			},
			expectedSortName: "Александр",
		},
		{
			name: "first_name with emoji",
			data: map[string]any{
				"first_name": "John 😊",
			},
			expectedSortName: "John 😊",
		},
		{
			name: "first_name with mixed unicode and whitespace",
			data: map[string]any{
				"first_name": "  María José  ",
			},
			expectedSortName: "María José",
		},
		{
			name: "first_name with combining characters",
			data: map[string]any{
				"first_name": "André",
			},
			expectedSortName: "André",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enricher.extractSortName(tt.data)
			assert.Equal(t, tt.expectedSortName, result)
		})
	}
}

func TestCommitteeMemberEnricher_NewCommitteeMemberEnricher(t *testing.T) {
	enricher := NewCommitteeMemberEnricher()

	// Verify it returns the correct interface
	require.NotNil(t, enricher)
	assert.Equal(t, constants.ObjectTypeCommitteeMember, enricher.ObjectType())

	// Verify it's a CommitteeMemberEnricher
	committeeMemberEnricher, ok := enricher.(*CommitteeMemberEnricher)
	require.True(t, ok, "NewCommitteeMemberEnricher should return a *CommitteeMemberEnricher")
	assert.NotNil(t, committeeMemberEnricher.defaultEnricher)
}

func TestCommitteeMemberEnricher_Integration_AccessControlWithCommitteeUID(t *testing.T) {
	enricher := NewCommitteeMemberEnricher()

	tests := []struct {
		name           string
		parsedData     map[string]any
		expectedAccess map[string]string
	}{
		{
			name: "committee member with committee_uid uses committee-level permissions",
			parsedData: map[string]any{
				"uid":            "member-123",
				"committee_uid":  "committee-456",
				"first_name":     "John",
				"last_name":      "Doe",
				"committee_name": "Engineering Committee",
			},
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee:committee-456",
				"AccessCheckRelation":  "viewer",
				"HistoryCheckObject":   "committee:committee-456",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee:committee-456#viewer",
				"HistoryCheckQuery":    "committee:committee-456#writer",
			},
		},
		{
			name: "committee member without committee_uid uses member-level permissions",
			parsedData: map[string]any{
				"uid":        "member-789",
				"first_name": "Jane",
				"last_name":  "Smith",
			},
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee_member:member-789",
				"AccessCheckRelation":  "viewer",
				"HistoryCheckObject":   "committee_member:member-789",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee_member:member-789#viewer",
				"HistoryCheckQuery":    "committee_member:member-789#writer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
				ObjectType: constants.ObjectTypeCommitteeMember,
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

func TestCommitteeMemberEnricher_Integration_NameAndAliasesExtraction(t *testing.T) {
	enricher := NewCommitteeMemberEnricher()

	parsedData := map[string]any{
		"uid":            "member-123",
		"committee_name": "Engineering Committee",
		"first_name":     "John",
		"last_name":      "Doe",
		"username":       "johndoe",
		"committee_uid":  "committee-456",
	}

	body := &contracts.TransactionBody{}
	transaction := &contracts.LFXTransaction{
		ParsedData: parsedData,
		ObjectType: constants.ObjectTypeCommitteeMember,
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	// Verify name and aliases extraction
	expectedAliases := []string{"Engineering Committee", "John", "Doe", "johndoe"}
	assert.ElementsMatch(t, expectedAliases, body.NameAndAliases)

	// Verify sort name extraction (should use first_name)
	assert.Equal(t, "John", body.SortName)

	// Verify fulltext includes all the aliases
	for _, alias := range expectedAliases {
		assert.Contains(t, body.Fulltext, alias)
	}
}

func TestCommitteeMemberEnricher_Integration_SortNameExtraction(t *testing.T) {
	enricher := NewCommitteeMemberEnricher()

	tests := []struct {
		name             string
		parsedData       map[string]any
		expectedSortName string
	}{
		{
			name: "first_name present - uses first_name",
			parsedData: map[string]any{
				"uid":        "member-123",
				"first_name": "Alice",
				"last_name":  "Johnson",
				"username":   "alicej",
			},
			expectedSortName: "Alice",
		},
		{
			name: "first_name empty - should be empty (committee member specific behavior)",
			parsedData: map[string]any{
				"uid":       "member-456",
				"last_name": "Smith",
				"username":  "smithuser",
			},
			expectedSortName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
				ObjectType: constants.ObjectTypeCommitteeMember,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedSortName, body.SortName)
		})
	}
}
