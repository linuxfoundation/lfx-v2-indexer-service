// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func TestIndexingHandler_AuditAdmission(t *testing.T) {
	for _, subject := range []string{constants.ProjectSubject, constants.V1ProjectSubject} {
		t.Run(subject, func(t *testing.T) {
			processor, messaging, storage := setupTestMessageProcessor()
			handler := indexingHandler{useCase: processor}
			var reply string
			err := handler.HandleWithReply(context.Background(), []byte(`{
				"schema_version":2,
				"audit":{"event_id":"fixture-event-1"},
				"action":"deleted",
				"data":"fixture-resource"
			}`), subject, func(data []byte) error { reply = string(data); return nil })
			require.Error(t, err)
			require.True(t, strings.HasPrefix(reply, "ERROR:"))
			storage.AssertNumberOfCalls(t, "Index", 0)
			messaging.AssertNumberOfCalls(t, "Publish", 0)
			messaging.AssertNumberOfCalls(t, "ParsePrincipals", 0)
		})
	}
}

func TestProcessIndexingMessage_LegacyNestedAuditFields(t *testing.T) {
	processor, messaging, storage := setupTestMessageProcessor()
	messaging.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{}, nil)
	storage.On("Index", mock.Anything, constants.DefaultIndex, "project:fixture-resource", mock.Anything).Return(nil)
	messaging.On("Publish", mock.Anything, constants.BuildEventSubject("project", constants.ActionCreated), mock.Anything).Return(nil)
	payload, err := json.Marshal(map[string]any{
		"action":  constants.ActionCreated,
		"headers": map[string]string{constants.AuthorizationHeader: "Bearer <fixture-token>"},
		"data": map[string]any{
			"uid": "fixture-resource", "audit": map[string]any{"schema_version": 2}, "schema_version": 7,
		},
		"indexing_config": map[string]string{
			"object_id": "fixture-resource", "access_check_object": "project:fixture-resource", "access_check_relation": "viewer",
			"history_check_object": "project:fixture-resource", "history_check_relation": "auditor",
		},
	})
	require.NoError(t, err)
	require.NoError(t, processor.ProcessIndexingMessage(context.Background(), payload, constants.ProjectSubject))
	storage.AssertExpectations(t)
	messaging.AssertExpectations(t)
}
