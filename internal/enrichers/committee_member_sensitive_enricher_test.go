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

func TestCommitteeMemberSensitiveEnricher_setAccessControl(t *testing.T) {
	enricher := &CommitteeMemberSensitiveEnricher{}

	tests := []struct {
		name           string
		data           map[string]any
		objectType     string
		objectID       string
		expectedAccess map[string]string
	}{
		{
			name: "with committee_uid - uses committee auditor/writer permission",
			data: map[string]any{
				"committee_uid": "committee-123",
			},
			objectType: "committee_member_sensitive",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee:committee-123",
				"AccessCheckRelation":  "auditor",
				"HistoryCheckObject":   "committee:committee-123",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee:committee-123#auditor",
				"HistoryCheckQuery":    "committee:committee-123#writer",
			},
		},
		{
			name: "without committee_uid - falls back to object permission",
			data: map[string]any{
				"email": "user@example.com",
			},
			objectType: "committee_member_sensitive",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee_member_sensitive:member-456",
				"AccessCheckRelation":  "auditor",
				"HistoryCheckObject":   "committee_member_sensitive:member-456",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee_member_sensitive:member-456#auditor",
				"HistoryCheckQuery":    "committee_member_sensitive:member-456#writer",
			},
		},
		{
			name: "committee_uid empty string - uses empty committee permission",
			data: map[string]any{
				"committee_uid": "",
				"email":         "user@example.com",
			},
			objectType: "committee_member_sensitive",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee:",
				"AccessCheckRelation":  "auditor",
				"HistoryCheckObject":   "committee:",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee:#auditor",
				"HistoryCheckQuery":    "committee:#writer",
			},
		},
		{
			name: "committee_uid non-string - falls back to object permission",
			data: map[string]any{
				"committee_uid": 12345,
				"email":         "user@example.com",
			},
			objectType: "committee_member_sensitive",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee_member_sensitive:member-456",
				"AccessCheckRelation":  "auditor",
				"HistoryCheckObject":   "committee_member_sensitive:member-456",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee_member_sensitive:member-456#auditor",
				"HistoryCheckQuery":    "committee_member_sensitive:member-456#writer",
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
			objectType: "committee_member_sensitive",
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
			objectType: "committee_member_sensitive",
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
				// accessCheckRelation and historyCheckRelation will use defaults (auditor/writer)
			},
			objectType: "committee_member_sensitive",
			objectID:   "member-999",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "explicit:access",  // explicit
				"AccessCheckRelation":  "auditor",          // default for sensitive
				"HistoryCheckObject":   "explicit:history", // explicit
				"HistoryCheckRelation": "writer",           // default
				"AccessCheckQuery":     "explicit:access#auditor",
				"HistoryCheckQuery":    "explicit:history#writer",
			},
		},
		{
			name: "only accessCheckRelation is explicit",
			data: map[string]any{
				"committee_uid":       "committee-123",
				"accessCheckRelation": "custom_relation",
			},
			objectType: "committee_member_sensitive",
			objectID:   "member-456",
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee:committee-123",
				"AccessCheckRelation":  "custom_relation",
				"HistoryCheckObject":   "committee:committee-123",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee:committee-123#custom_relation",
				"HistoryCheckQuery":    "committee:committee-123#writer",
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

func TestCommitteeMemberSensitiveEnricher_setExtractNameAndAliases(t *testing.T) {
	enricher := &CommitteeMemberSensitiveEnricher{}

	tests := []struct {
		name            string
		data            map[string]any
		expectedAliases []string
	}{
		{
			name: "email field only",
			data: map[string]any{
				"email": "john.doe@example.com",
			},
			expectedAliases: []string{"john.doe@example.com"},
		},
		{
			name: "multiple email-related fields but only email matches",
			data: map[string]any{
				"email":          "user@example.com",
				"first_name":     "John",     // not in regex
				"last_name":      "Doe",      // not in regex
				"committee_name": "Eng Team", // not in regex
			},
			expectedAliases: []string{"user@example.com"},
		},
		{
			name: "empty email value is filtered",
			data: map[string]any{
				"email": "",
			},
			expectedAliases: nil,
		},
		{
			name: "whitespace-only email is filtered",
			data: map[string]any{
				"email": "   ",
			},
			expectedAliases: nil,
		},
		{
			name: "email with leading/trailing whitespace is trimmed",
			data: map[string]any{
				"email": "  user@example.com  ",
			},
			expectedAliases: []string{"user@example.com"},
		},
		{
			name: "duplicate emails are deduplicated",
			data: map[string]any{
				"email":  "duplicate@example.com",
				"EMAIL":  "duplicate@example.com", // case insensitive key match
				"eMAil":  "different@example.com",
				"e_mail": "another@example.com", // doesn't match regex
			},
			expectedAliases: []string{"duplicate@example.com", "different@example.com"},
		},
		{
			name: "non-string email values are ignored",
			data: map[string]any{
				"email": 12345, // number
			},
			expectedAliases: nil,
		},
		{
			name: "email as array is ignored",
			data: map[string]any{
				"email": []string{"user1@example.com", "user2@example.com"},
			},
			expectedAliases: nil,
		},
		{
			name: "email as map is ignored",
			data: map[string]any{
				"email": map[string]string{"primary": "user@example.com"},
			},
			expectedAliases: nil,
		},
		{
			name: "case insensitive field matching",
			data: map[string]any{
				"Email": "lowercase@example.com",
				"EMAIL": "uppercase@example.com",
				"eMaIl": "mixedcase@example.com",
			},
			expectedAliases: []string{"lowercase@example.com", "uppercase@example.com", "mixedcase@example.com"},
		},
		{
			name: "no matching fields",
			data: map[string]any{
				"user_id":        "user-123",
				"committee_id":   "committee-456",
				"role":           "member",
				"contact_email":  "test@example.com", // doesn't match regex exactly
				"email_address":  "test2@example.com", // doesn't match regex exactly
				"personal_email": "test3@example.com", // doesn't match regex exactly
			},
			expectedAliases: nil,
		},
		{
			name:            "empty data",
			data:            map[string]any{},
			expectedAliases: nil,
		},
		{
			name: "nil email value",
			data: map[string]any{
				"email": nil,
			},
			expectedAliases: nil,
		},
		{
			name: "email with special characters",
			data: map[string]any{
				"email": "user+tag@example.com",
			},
			expectedAliases: []string{"user+tag@example.com"},
		},
		{
			name: "email with subdomain",
			data: map[string]any{
				"email": "admin@mail.example.com",
			},
			expectedAliases: []string{"admin@mail.example.com"},
		},
		{
			name: "email with unicode characters",
			data: map[string]any{
				"email": "用户@example.com",
			},
			expectedAliases: []string{"用户@example.com"},
		},
		{
			name: "multiple valid emails without duplicates",
			data: map[string]any{
				"email": "first@example.com",
			},
			expectedAliases: []string{"first@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enricher.setExtractNameAndAliases(tt.data)

			if tt.expectedAliases == nil {
				assert.Nil(t, result)
			} else {
				assert.ElementsMatch(t, tt.expectedAliases, result)
			}
		})
	}
}

func TestCommitteeMemberSensitiveEnricher_extractSortName(t *testing.T) {
	enricher := &CommitteeMemberSensitiveEnricher{}

	tests := []struct {
		name             string
		data             map[string]any
		expectedSortName string
	}{
		{
			name: "valid email",
			data: map[string]any{
				"email":      "john.doe@example.com",
				"first_name": "John", // should be ignored
			},
			expectedSortName: "john.doe@example.com",
		},
		{
			name: "email with whitespace",
			data: map[string]any{
				"email": "  user@example.com  ",
			},
			expectedSortName: "user@example.com",
		},
		{
			name: "empty email",
			data: map[string]any{
				"email":      "",
				"first_name": "Smith", // should be ignored
			},
			expectedSortName: "",
		},
		{
			name: "email with only whitespace",
			data: map[string]any{
				"email": "   ",
			},
			expectedSortName: "",
		},
		{
			name: "non-string email",
			data: map[string]any{
				"email": 12345,
			},
			expectedSortName: "",
		},
		{
			name: "missing email field",
			data: map[string]any{
				"first_name": "Johnson",
				"last_name":  "User",
			},
			expectedSortName: "",
		},
		{
			name: "email is array",
			data: map[string]any{
				"email": []string{"user1@example.com", "user2@example.com"},
			},
			expectedSortName: "",
		},
		{
			name: "email is map",
			data: map[string]any{
				"email": map[string]string{"primary": "user@example.com"},
			},
			expectedSortName: "",
		},
		{
			name:             "empty data",
			data:             map[string]any{},
			expectedSortName: "",
		},
		{
			name: "email is nil",
			data: map[string]any{
				"email": nil,
			},
			expectedSortName: "",
		},
		{
			name: "email with special characters",
			data: map[string]any{
				"email": "user+tag@example.com",
			},
			expectedSortName: "user+tag@example.com",
		},
		{
			name: "email with subdomain",
			data: map[string]any{
				"email": "admin@mail.example.com",
			},
			expectedSortName: "admin@mail.example.com",
		},
		{
			name: "email with unicode domain",
			data: map[string]any{
				"email": "user@example.中国",
			},
			expectedSortName: "user@example.中国",
		},
		{
			name: "email with unicode local part",
			data: map[string]any{
				"email": "用户@example.com",
			},
			expectedSortName: "用户@example.com",
		},
		{
			name: "email with uppercase",
			data: map[string]any{
				"email": "USER@EXAMPLE.COM",
			},
			expectedSortName: "USER@EXAMPLE.COM",
		},
		{
			name: "email with mixed case",
			data: map[string]any{
				"email": "User.Name@Example.COM",
			},
			expectedSortName: "User.Name@Example.COM",
		},
		{
			name: "complex email address",
			data: map[string]any{
				"email": "user.name+tag@subdomain.example.co.uk",
			},
			expectedSortName: "user.name+tag@subdomain.example.co.uk",
		},
		{
			name: "email with leading/trailing tabs",
			data: map[string]any{
				"email": "\tuser@example.com\t",
			},
			expectedSortName: "user@example.com",
		},
		{
			name: "email with mixed whitespace",
			data: map[string]any{
				"email": " \t user@example.com \n ",
			},
			expectedSortName: "user@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enricher.extractSortName(tt.data)
			assert.Equal(t, tt.expectedSortName, result)
		})
	}
}

func TestCommitteeMemberSensitiveEnricher_NewCommitteeMemberSensitiveEnricher(t *testing.T) {
	enricher := NewCommitteeMemberSensitiveEnricher()

	// Verify it returns the correct interface
	require.NotNil(t, enricher)
	assert.Equal(t, constants.ObjectTypeCommitteeMemberSensitive, enricher.ObjectType())

	// Verify it's a CommitteeMemberSensitiveEnricher
	committeeMemberSensitiveEnricher, ok := enricher.(*CommitteeMemberSensitiveEnricher)
	require.True(t, ok, "NewCommitteeMemberSensitiveEnricher should return a *CommitteeMemberSensitiveEnricher")
	assert.NotNil(t, committeeMemberSensitiveEnricher.defaultEnricher)
}

func TestCommitteeMemberSensitiveEnricher_Integration_AccessControlWithCommitteeUID(t *testing.T) {
	enricher := NewCommitteeMemberSensitiveEnricher()

	tests := []struct {
		name           string
		parsedData     map[string]any
		expectedAccess map[string]string
	}{
		{
			name: "sensitive member with committee_uid uses committee-level auditor/writer permissions",
			parsedData: map[string]any{
				"uid":           "member-123",
				"committee_uid": "committee-456",
				"email":         "john.doe@example.com",
			},
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee:committee-456",
				"AccessCheckRelation":  "auditor",
				"HistoryCheckObject":   "committee:committee-456",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee:committee-456#auditor",
				"HistoryCheckQuery":    "committee:committee-456#writer",
			},
		},
		{
			name: "sensitive member without committee_uid uses member-level auditor/writer permissions",
			parsedData: map[string]any{
				"uid":   "member-789",
				"email": "jane.smith@example.com",
			},
			expectedAccess: map[string]string{
				"AccessCheckObject":    "committee_member_sensitive:member-789",
				"AccessCheckRelation":  "auditor",
				"HistoryCheckObject":   "committee_member_sensitive:member-789",
				"HistoryCheckRelation": "writer",
				"AccessCheckQuery":     "committee_member_sensitive:member-789#auditor",
				"HistoryCheckQuery":    "committee_member_sensitive:member-789#writer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
				ObjectType: constants.ObjectTypeCommitteeMemberSensitive,
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

func TestCommitteeMemberSensitiveEnricher_Integration_NameAndAliasesExtraction(t *testing.T) {
	enricher := NewCommitteeMemberSensitiveEnricher()

	parsedData := map[string]any{
		"uid":           "member-123",
		"email":         "john.doe@example.com",
		"committee_uid": "committee-456",
		"first_name":    "John",      // should be ignored - only email is extracted
		"last_name":     "Doe",       // should be ignored
		"username":      "johndoe",   // should be ignored
	}

	body := &contracts.TransactionBody{}
	transaction := &contracts.LFXTransaction{
		ParsedData: parsedData,
		ObjectType: constants.ObjectTypeCommitteeMemberSensitive,
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	// Verify name and aliases extraction (only email should be included)
	expectedAliases := []string{"john.doe@example.com"}
	assert.ElementsMatch(t, expectedAliases, body.NameAndAliases)

	// Verify sort name extraction (should use email)
	assert.Equal(t, "john.doe@example.com", body.SortName)

	// Verify fulltext includes the email
	assert.Contains(t, body.Fulltext, "john.doe@example.com")
}

func TestCommitteeMemberSensitiveEnricher_Integration_SortNameExtraction(t *testing.T) {
	enricher := NewCommitteeMemberSensitiveEnricher()

	tests := []struct {
		name             string
		parsedData       map[string]any
		expectedSortName string
	}{
		{
			name: "email present - uses email",
			parsedData: map[string]any{
				"uid":        "member-123",
				"email":      "alice@example.com",
				"first_name": "Alice", // should be ignored
				"last_name":  "Johnson", // should be ignored
			},
			expectedSortName: "alice@example.com",
		},
		{
			name: "email empty - should be empty",
			parsedData: map[string]any{
				"uid":        "member-456",
				"email":      "",
				"first_name": "Bob",
				"last_name":  "Smith",
			},
			expectedSortName: "",
		},
		{
			name: "no email field - should be empty",
			parsedData: map[string]any{
				"uid":        "member-789",
				"first_name": "Charlie",
				"last_name":  "Brown",
			},
			expectedSortName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
				ObjectType: constants.ObjectTypeCommitteeMemberSensitive,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedSortName, body.SortName)
		})
	}
}

func TestCommitteeMemberSensitiveEnricher_Integration_OnlyEmailExtracted(t *testing.T) {
	enricher := NewCommitteeMemberSensitiveEnricher()

	parsedData := map[string]any{
		"uid":            "member-123",
		"email":          "sensitive@example.com",
		"committee_uid":  "committee-456",
		"committee_name": "Engineering Committee", // not extracted
		"first_name":     "John",                   // not extracted
		"last_name":      "Doe",                    // not extracted
		"username":       "johndoe",                // not extracted
		"phone":          "+1-555-0100",            // not extracted
		"address":        "123 Main St",            // not extracted
	}

	body := &contracts.TransactionBody{}
	transaction := &contracts.LFXTransaction{
		ParsedData: parsedData,
		ObjectType: constants.ObjectTypeCommitteeMemberSensitive,
	}

	err := enricher.EnrichData(body, transaction)
	require.NoError(t, err)

	// Only email should be in name and aliases
	assert.Equal(t, []string{"sensitive@example.com"}, body.NameAndAliases)

	// Only email should be the sort name
	assert.Equal(t, "sensitive@example.com", body.SortName)

	// Fulltext should contain email but may contain other data from default enrichment
	assert.Contains(t, body.Fulltext, "sensitive@example.com")
}

func TestCommitteeMemberSensitiveEnricher_Integration_EmptyEmailHandling(t *testing.T) {
	enricher := NewCommitteeMemberSensitiveEnricher()

	tests := []struct {
		name       string
		parsedData map[string]any
	}{
		{
			name: "empty email string",
			parsedData: map[string]any{
				"uid":           "member-123",
				"email":         "",
				"committee_uid": "committee-456",
			},
		},
		{
			name: "whitespace-only email",
			parsedData: map[string]any{
				"uid":           "member-456",
				"email":         "   ",
				"committee_uid": "committee-456",
			},
		},
		{
			name: "missing email field",
			parsedData: map[string]any{
				"uid":           "member-789",
				"committee_uid": "committee-456",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &contracts.TransactionBody{}
			transaction := &contracts.LFXTransaction{
				ParsedData: tt.parsedData,
				ObjectType: constants.ObjectTypeCommitteeMemberSensitive,
			}

			err := enricher.EnrichData(body, transaction)
			require.NoError(t, err)

			// Should have no aliases when email is empty/missing
			assert.Nil(t, body.NameAndAliases)

			// Should have empty sort name
			assert.Equal(t, "", body.SortName)
		})
	}
}
