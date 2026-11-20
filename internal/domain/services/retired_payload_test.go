// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

func TestIndexerService_RetiredPayloadNotEmitted(t *testing.T) {
	storage, messaging := mocks.NewMockStorageRepository(), mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)
	svc := NewIndexerService(storage, messaging, logger)
	message := map[string]any{
		"action": string(constants.ActionCreated), "data": map[string]any{"uid": "fixture-resource"},
		"headers":         map[string]any{constants.AuthorizationHeader: "Bearer fixture-token"},
		"v1_data":         map[string]any{"obsolete": "retired-record-marker"},
		"indexing_config": map[string]any{"object_id": "fixture-resource", "access_check_object": "project:fixture-resource", "access_check_relation": "viewer", "history_check_object": "project:fixture-resource", "history_check_relation": "auditor"},
	}
	transaction, err := svc.CreateTransactionFromMessage(message, "v1_meeting")
	require.NoError(t, err)
	result, err := svc.ProcessTransaction(context.Background(), transaction, "resources")
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Len(t, storage.IndexCalls, 1)
	require.Len(t, messaging.PublishCalls, 1)
	for _, payload := range []string{storage.IndexCalls[0].Body, string(messaging.PublishCalls[0].Data)} {
		require.NotContains(t, payload, "v1_data")
		require.NotContains(t, payload, "retired-record-marker")
	}
}
