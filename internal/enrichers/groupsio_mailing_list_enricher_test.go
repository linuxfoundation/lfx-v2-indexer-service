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

func TestGroupsIOMailingListEnricher_ObjectType(t *testing.T) {
	enricher := NewGroupsIOMailingListEnricher()
	assert.Equal(t, constants.ObjectTypeGroupsIOMailingList, enricher.ObjectType())
}

func TestGroupsIOMailingListEnricher_EnrichData(t *testing.T) {
	enricher := NewGroupsIOMailingListEnricher()

	tests := []struct {
		name          string
		parsedData    map[string]any
		expectedBody  *contracts.TransactionBody
		expectedError string
	}{
		{
			name: "group_name used as sort name and alias",
			parsedData: map[string]any{
				"uid":        "ml-123",
				"group_name": "My Mailing List",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:       "ml-123",
				SortName:       "My Mailing List",
				NameAndAliases: []string{"My Mailing List"},
			},
		},
		{
			name: "group_name takes priority over name field",
			parsedData: map[string]any{
				"uid":        "ml-456",
				"group_name": "Group Name",
				"name":       "Other Name",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "ml-456",
				SortName: "Group Name",
			},
		},
		{
			name: "both group_name and name appear in aliases",
			parsedData: map[string]any{
				"uid":        "ml-789",
				"group_name": "Group Name",
				"name":       "Other Name",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:       "ml-789",
				SortName:       "Group Name",
				NameAndAliases: []string{"Group Name", "Other Name"},
			},
		},
		{
			name: "fallback to name when group_name absent",
			parsedData: map[string]any{
				"uid":  "ml-fallback",
				"name": "Fallback Name",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:       "ml-fallback",
				SortName:       "Fallback Name",
				NameAndAliases: []string{"Fallback Name"},
			},
		},
		{
			name: "group_name whitespace trimmed",
			parsedData: map[string]any{
				"uid":        "ml-trim",
				"group_name": "  Trimmed  ",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID:       "ml-trim",
				SortName:       "Trimmed",
				NameAndAliases: []string{"Trimmed"},
			},
		},
		{
			name: "empty group_name falls back to name",
			parsedData: map[string]any{
				"uid":        "ml-empty",
				"group_name": "",
				"name":       "Fallback",
			},
			expectedBody: &contracts.TransactionBody{
				ObjectID: "ml-empty",
				SortName: "Fallback",
			},
		},
		{
			name: "error: missing uid and id",
			parsedData: map[string]any{
				"group_name": "Some List",
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
			assert.Equal(t, tt.expectedBody.ObjectID, body.ObjectID)
			assert.Equal(t, tt.expectedBody.SortName, body.SortName)
			if tt.expectedBody.NameAndAliases != nil {
				assert.Equal(t, tt.expectedBody.NameAndAliases, body.NameAndAliases)
			}
		})
	}
}
