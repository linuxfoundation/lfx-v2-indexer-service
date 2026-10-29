// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

func TestCreateTransactionFromMessage_EnvelopeAdmission(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]any
		valid  bool
	}{
		{name: "unversioned_legacy", valid: true},
		{name: "unversioned_null_audit", fields: map[string]any{"audit": nil}},
		{name: "unversioned_audit", fields: map[string]any{"audit": map[string]any{}}},
		{name: "version_2", fields: map[string]any{"schema_version": 2, "audit": map[string]any{}}},
		{name: "version_2_json_number", fields: map[string]any{"schema_version": json.Number("2"), "audit": map[string]any{}}},
		{name: "version_2_missing_audit", fields: map[string]any{"schema_version": 2}},
		{name: "version_2_null_audit", fields: map[string]any{"schema_version": 2, "audit": nil}},
		{name: "unknown_version", fields: map[string]any{"schema_version": 3}},
		{name: "explicit_version_1_not_defined", fields: map[string]any{"schema_version": 1}},
		{name: "zero_version", fields: map[string]any{"schema_version": 0}},
		{name: "null_version", fields: map[string]any{"schema_version": nil}},
		{name: "string_version", fields: map[string]any{"schema_version": "2"}},
		{name: "fractional_version", fields: map[string]any{"schema_version": 2.5}},
		{name: "array_version", fields: map[string]any{"schema_version": []any{2}}},
		{name: "wrong_audit_case", fields: map[string]any{"Audit": nil}},
		{name: "wrong_version_case", fields: map[string]any{"Schema_Version": 2}},
	}
	for _, isV1 := range []bool{false, true} {
		version := "platform_v2"
		action := constants.ActionDeleted
		if isV1 {
			version, action = "platform_v1", constants.ActionDelete
		}
		for _, tt := range tests {
			t.Run(version+"/"+tt.name, func(t *testing.T) {
				storage, messaging := mocks.NewMockStorageRepository(), mocks.NewMockMessagingRepository()
				logger, _ := logging.TestLogger(t)
				service := NewIndexerService(storage, messaging, logger)
				message := map[string]any{"action": string(action), "data": "fixture-resource"}
				for k, v := range tt.fields {
					message[k] = v
				}
				txn, err := service.CreateTransactionFromMessage(message, "fixture_resource", isV1)
				if tt.valid {
					require.NoError(t, err)
					require.NotNil(t, txn)
				} else {
					require.Error(t, err)
					require.Nil(t, txn)
				}
				require.Empty(t, storage.IndexCalls)
				require.Empty(t, messaging.PublishCalls)
				require.Empty(t, messaging.AuthRepo.ParsePrincipalsCalls)
			})
		}
	}
}
