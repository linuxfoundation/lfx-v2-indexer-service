// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func TestIndexerService_ProcessTransaction_DeleteContext(t *testing.T) {
	for _, isV1 := range []bool{false, true} {
		version := "v2"
		action := constants.ActionDeleted
		if isV1 {
			version = "v1"
			action = constants.ActionDelete
		}
		for _, withConfig := range []bool{false, true} {
			name := version + "/legacy"
			if withConfig {
				name = version + "/with_context"
			}
			t.Run(name, func(t *testing.T) {
				storage := mocks.NewMockStorageRepository()
				messaging := mocks.NewMockMessagingRepository()
				logger, _ := logging.TestLogger(t)
				service := NewIndexerService(storage, messaging, logger)
				envelope := types.IndexerMessageEnvelope{
					Action:  action,
					Headers: map[string]string{constants.AuthorizationHeader: "Bearer test-token"},
					Data:    "committee-1",
					Tags:    []string{"legacy-delete-tag"},
				}
				if withConfig {
					public := true
					envelope.IndexingConfig = &types.IndexingConfig{
						ObjectID:             "committee-1",
						AccessCheckObject:    "committee:committee-1",
						AccessCheckRelation:  "viewer",
						HistoryCheckObject:   "project:project-1",
						HistoryCheckRelation: "auditor",
						ParentRefs:           []string{"project:project-1"},
						// These must not turn a tombstone into a searchable/public resource.
						Public:         &public,
						SortName:       "Test Committee",
						NameAndAliases: []string{"Test Committee"},
						Tags:           []string{"not-retained"},
						Fulltext:       "not-retained",
						Contacts:       []types.ContactBody{{Name: "Test User"}},
					}
				}
				payload, err := json.Marshal(envelope)
				require.NoError(t, err)
				var message map[string]any
				require.NoError(t, json.Unmarshal(payload, &message))
				if isV1 {
					message["v1_data"] = map[string]any{"legacy_field": "fixture-value"}
				}
				transaction, err := service.CreateTransactionFromMessage(message, "committee", isV1)
				require.NoError(t, err)

				result, err := service.ProcessTransaction(context.Background(), transaction, "resources")
				require.NoError(t, err)
				assert.True(t, result.Success)
				require.Len(t, storage.IndexCalls, 1)
				assert.Equal(t, "resources", storage.IndexCalls[0].Index)
				assert.Equal(t, "committee:committee-1", storage.IndexCalls[0].DocID)
				var body contracts.TransactionBody
				require.NoError(t, json.Unmarshal([]byte(storage.IndexCalls[0].Body), &body))
				assert.Equal(t, "committee-1", body.ObjectID)
				assert.Equal(t, "committee:committee-1", body.ObjectRef)
				require.NotNil(t, body.DeletedAt)
				assert.True(t, transaction.Timestamp.Equal(*body.DeletedAt))
				require.NotNil(t, body.Latest)
				assert.True(t, *body.Latest)
				if withConfig {
					assert.Equal(t, []string{"project:project-1"}, body.ParentRefs)
					assert.Equal(t, "project:project-1", body.HistoryCheckObject)
					assert.Equal(t, "auditor", body.HistoryCheckRelation)
					assert.Equal(t, "project:project-1#auditor", body.HistoryCheckQuery)
				} else {
					assert.Empty(t, body.ParentRefs)
					assert.Empty(t, body.HistoryCheckObject)
					assert.Empty(t, body.HistoryCheckRelation)
					assert.Empty(t, body.HistoryCheckQuery)
				}
				assert.False(t, body.Public)
				assert.Empty(t, body.AccessCheckObject)
				assert.Empty(t, body.AccessCheckRelation)
				assert.Empty(t, body.AccessCheckQuery)
				assert.Empty(t, body.Data)
				assert.Empty(t, body.Contacts)
				assert.Empty(t, body.SortName)
				assert.Empty(t, body.NameAndAliases)
				assert.Empty(t, body.Fulltext)
				assert.Equal(t, []string{"legacy-delete-tag"}, body.Tags)
				if isV1 {
					assert.Equal(t, map[string]any{"legacy_field": "fixture-value"}, body.V1Data)
				} else {
					assert.Empty(t, body.V1Data)
				}
				require.Len(t, messaging.PublishCalls, 1)
				assert.Equal(t, constants.BuildEventSubject("committee", constants.ActionDeleted), messaging.PublishCalls[0].Subject)
				var event contracts.IndexingEvent
				require.NoError(t, json.Unmarshal(messaging.PublishCalls[0].Data, &event))
				require.NotNil(t, event.Body)
				assert.Equal(t, body, *event.Body, "post-index event must preserve the same legacy payload and permitted config context")
			})
		}
	}
}

func TestIndexerService_ProcessTransaction_DeleteContextIdentityMismatch(t *testing.T) {
	for _, isV1 := range []bool{false, true} {
		version := "v2"
		action := constants.ActionDeleted
		if isV1 {
			version = "v1"
			action = constants.ActionDelete
		}
		t.Run(version, func(t *testing.T) {
			storage := mocks.NewMockStorageRepository()
			storage.IndexedDocuments["resources"] = map[string]interface{}{
				"committee:committee-1": `{"public":true,"access_check_object":"committee:committee-1","access_check_relation":"viewer","latest":true,"data":{"name":"Test Committee"}}`,
			}
			messaging := mocks.NewMockMessagingRepository()
			logger, logs := logging.TestLogger(t)
			service := NewIndexerService(storage, messaging, logger)
			transaction := &contracts.LFXTransaction{
				ObjectType: "committee",
				Action:     action,
				IsV1:       isV1,
				Headers:    map[string]string{constants.AuthorizationHeader: "Bearer test-token"},
				Data:       "committee-1",
				IndexingConfig: &types.IndexingConfig{
					ObjectID:             "committee-2",
					AccessCheckObject:    "committee:committee-2",
					AccessCheckRelation:  "viewer",
					HistoryCheckObject:   "project:project-2",
					HistoryCheckRelation: "auditor",
					ParentRefs:           []string{"project:project-2"},
				},
			}

			result, err := service.ProcessTransaction(context.Background(), transaction, "resources")
			require.NoError(t, err)
			assert.True(t, result.Success)
			require.Len(t, storage.IndexCalls, 1)
			assert.Equal(t, "committee:committee-1", storage.IndexCalls[0].DocID)
			var body contracts.TransactionBody
			stored, ok := storage.IndexedDocuments["resources"]["committee:committee-1"].(string)
			require.True(t, ok)
			require.NoError(t, json.Unmarshal([]byte(stored), &body))
			assert.Equal(t, "committee-1", body.ObjectID)
			require.NotNil(t, body.DeletedAt)
			assert.False(t, body.Public)
			assert.Empty(t, body.AccessCheckObject)
			assert.Empty(t, body.AccessCheckRelation)
			assert.Empty(t, body.AccessCheckQuery)
			assert.Empty(t, body.Data)
			assert.Empty(t, body.ParentRefs)
			assert.Empty(t, body.HistoryCheckObject)
			assert.Empty(t, body.HistoryCheckRelation)
			assert.Empty(t, body.HistoryCheckQuery)
			require.Len(t, messaging.PublishCalls, 1)
			assert.Equal(t, constants.BuildEventSubject("committee", constants.ActionDeleted), messaging.PublishCalls[0].Subject)
			var event contracts.IndexingEvent
			require.NoError(t, json.Unmarshal(messaging.PublishCalls[0].Data, &event))
			require.NotNil(t, event.Body)
			assert.Equal(t, body, *event.Body)

			var warning map[string]any
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var record map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &record))
				if record["validation_step"] == "delete_config_identity" {
					warning = record
				}
			}
			require.NotNil(t, warning, "mismatched context must not be discarded silently")
			assert.Equal(t, "WARN", warning["level"])
			assert.Equal(t, "committee", warning["object_type"])
			assert.NotEmpty(t, warning["transaction_id"])
			warningBytes, err := json.Marshal(warning)
			require.NoError(t, err)
			for _, value := range []string{"committee-1", "committee-2", "project-2", "test-token"} {
				assert.NotContains(t, string(warningBytes), value)
			}
		})
	}
}

func TestIndexerService_CreateTransactionFromMessage_IndexingConfigShape(t *testing.T) {
	validConfig := map[string]any{
		"object_id":              "committee-1",
		"access_check_object":    "committee:committee-1",
		"access_check_relation":  "viewer",
		"history_check_object":   "project:project-1",
		"history_check_relation": "auditor",
	}
	tests := []struct {
		name       string
		present    bool
		value      any
		wantError  string
		wantConfig bool
	}{
		{name: "absent"},
		{name: "null", present: true},
		{name: "string", present: true, value: "invalid", wantError: "indexing_config must be an object"},
		{name: "array", present: true, value: []any{}, wantError: "indexing_config must be an object"},
		{name: "number", present: true, value: float64(1), wantError: "indexing_config must be an object"},
		{name: "boolean", present: true, value: false, wantError: "indexing_config must be an object"},
		{name: "empty_object", present: true, value: map[string]any{}, wantError: "object_id is required"},
		{name: "valid_object", present: true, value: validConfig, wantConfig: true},
	}
	for _, version := range []struct {
		name    string
		isV1    bool
		actions []constants.MessageAction
	}{
		{name: "v2", actions: []constants.MessageAction{constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted}},
		{name: "v1", isV1: true, actions: []constants.MessageAction{constants.ActionCreate, constants.ActionUpdate, constants.ActionDelete}},
	} {
		for _, action := range version.actions {
			for _, tt := range tests {
				t.Run(version.name+"/"+string(action)+"/"+tt.name, func(t *testing.T) {
					logger, _ := logging.TestLogger(t)
					service := NewIndexerService(mocks.NewMockStorageRepository(), mocks.NewMockMessagingRepository(), logger)
					message := map[string]any{
						"action": string(action),
						"data":   "committee-1",
					}
					if tt.present {
						message["indexing_config"] = tt.value
					}
					transaction, err := service.CreateTransactionFromMessage(message, "committee", version.isV1)
					if tt.wantError != "" {
						require.ErrorContains(t, err, tt.wantError)
						assert.Nil(t, transaction)
						return
					}
					require.NoError(t, err)
					if tt.wantConfig {
						require.NotNil(t, transaction.IndexingConfig)
						assert.Equal(t, "committee-1", transaction.IndexingConfig.ObjectID)
					} else {
						assert.Nil(t, transaction.IndexingConfig)
					}
				})
			}
		}
	}
}
