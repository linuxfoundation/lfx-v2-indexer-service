// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func historicalEnvelope(t *testing.T, action constants.MessageAction, event string, revision uint64) []byte {
	t.Helper()
	var envelope types.IndexerMessageEnvelope
	require.NoError(t, json.Unmarshal(workflowEnvelope(action, event, revision), &envelope))
	envelope.Audit.Origin = types.AuditOriginHistoricalImport
	payload, err := json.Marshal(envelope)
	require.NoError(t, err)
	return payload
}

func TestAuditWorkflow_HistoricalImportHasNoCurrentSideEffects(t *testing.T) {
	for _, action := range []constants.MessageAction{constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted} {
		for _, currentRevision := range []uint64{0, 1, 42, 100} {
			t.Run(fmt.Sprintf("%s/revision-%d", action, currentRevision), func(t *testing.T) {
				w, history, current, notifier := workflowFixture(t)
				current.Revision = currentRevision
				// Any accidental call must fail, including a would-be guarded no-op.
				current.Failure = true
				current.LastID, current.LastFingerprint = "existing-event", "existing-fingerprint"
				payload := historicalEnvelope(t, action, "historical-event", 42)
				firstIngested := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
				w.now = func() time.Time { return firstIngested }
				for range 2 {
					require.NoError(t, w.Process(context.Background(), payload, constants.IndexPrefix+"fixture_resource"))
					w.now = func() time.Time { return firstIngested.Add(time.Hour) }
				}
				require.Len(t, history.Records, 1)
				require.Equal(t, types.AuditOriginHistoricalImport, history.LastRecord.Audit.Origin)
				require.Equal(t, action, history.LastRecord.Action)
				require.Equal(t, firstIngested, history.LastRecord.IngestedAt)
				require.True(t, history.LastRecord.OccurredAt.Before(firstIngested))
				require.Equal(t, currentRevision, current.Revision)
				require.Equal(t, "existing-event", current.LastID)
				require.Equal(t, "existing-fingerprint", current.LastFingerprint)
				require.Zero(t, current.Writes)
				require.Zero(t, notifier.Events)
				require.Empty(t, w.cleanup.(*mocks.MockAuditCleanup).Refs)
			})
		}
	}
}

func TestAuditWorkflow_HistoricalImportRetryAfterReceipt(t *testing.T) {
	w, history, current, notifier := workflowFixture(t)
	current.Failure = true
	payload := historicalEnvelope(t, constants.ActionUpdated, "retry-import", 42)
	history.Fail = true
	require.ErrorContains(t, w.Process(context.Background(), payload, constants.IndexPrefix+"fixture_resource"), "history unavailable")
	require.Len(t, w.receipts.(*mocks.MockAuditReceipts).Identities, 1)
	require.Empty(t, history.Records)
	history.Fail = false
	require.NoError(t, w.Process(context.Background(), payload, constants.IndexPrefix+"fixture_resource"))
	require.Len(t, history.Records, 1)
	require.Zero(t, notifier.Events)
	require.Empty(t, w.cleanup.(*mocks.MockAuditCleanup).Refs)
}

func TestAuditWorkflow_HistoricalImportRetainsFirstAttribution(t *testing.T) {
	w, history, current, notifier := workflowFixture(t)
	current.Failure = true
	directory := &mocks.MockIdentityDirectory{}
	email := "first.last@example.com"
	directory.On("LookupIdentity", mock.Anything, "first-last").Return(contracts.IdentityLookup{Found: true, Email: &email}, nil).Once()
	identities, err := services.NewAuditIdentityResolver(directory, nil)
	require.NoError(t, err)
	auth := &mocks.MockAuditTokenValidator{}
	auth.On("ValidateActorToken", mock.Anything, "private-expired-evidence").Return(nil, fmt.Errorf("expired")).Once()
	auth.On("ValidateActorToken", mock.Anything, "private-refreshed-evidence").Return(&contracts.Principal{Principal: "first-last", Email: "changed@example.com"}, nil).Once()
	attribution, err := services.NewAuditAttributionService(auth, identities, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	require.NoError(t, err)
	w.SetAttributionService(attribution)
	var envelope types.IndexerMessageEnvelope
	require.NoError(t, json.Unmarshal(historicalEnvelope(t, constants.ActionUpdated, "attributed-import", 42), &envelope))
	envelope.Audit.Attribution = types.AuditAttribution{ActorRef: "user:first-last", EffectiveActorRef: "user:first-last", ActorJWT: "private-expired-evidence"}
	payload, err := json.Marshal(envelope)
	require.NoError(t, err)
	require.NoError(t, w.Process(context.Background(), payload, constants.IndexPrefix+"fixture_resource"))
	first := history.LastRecord
	require.False(t, first.ActorVerified)
	require.Equal(t, email, *first.Audit.Attribution.ActorIdentity.Email)
	envelope.Audit.Attribution.ActorJWT = "private-refreshed-evidence"
	payload, err = json.Marshal(envelope)
	require.NoError(t, err)
	require.NoError(t, w.Process(context.Background(), payload, constants.IndexPrefix+"fixture_resource"))
	require.Same(t, first, history.LastRecord)
	require.False(t, history.LastRecord.ActorVerified, "replay cannot upgrade first-append verification")
	stored, err := json.Marshal(history.LastRecord)
	require.NoError(t, err)
	require.NotContains(t, string(stored), "private-")
	require.NotContains(t, string(stored), "Bearer")
	require.NotContains(t, string(stored), "actor_jwt")
	require.Zero(t, notifier.Events)
	directory.AssertExpectations(t)
	auth.AssertExpectations(t)
}

func TestAuditWorkflow_HistoricalImportConflictsAndErasure(t *testing.T) {
	for _, scenario := range []string{"changed snapshot", "changed action", "changed origin", "erased history"} {
		t.Run(scenario, func(t *testing.T) {
			w, history, current, notifier := workflowFixture(t)
			current.Failure = true
			payload := historicalEnvelope(t, constants.ActionUpdated, "immutable-import", 42)
			require.NoError(t, w.Process(context.Background(), payload, constants.IndexPrefix+"fixture_resource"))
			first := history.LastRecord
			var envelope types.IndexerMessageEnvelope
			require.NoError(t, json.Unmarshal(payload, &envelope))
			switch scenario {
			case "changed snapshot":
				envelope.Audit.Content.After = json.RawMessage(`{"name":"Changed"}`)
			case "changed action":
				envelope.Action = constants.ActionDeleted
				envelope.Data = envelope.IndexingConfig.ObjectID
				envelope.Audit.Scope.After, envelope.Audit.Content.After = nil, nil
			case "changed origin":
				envelope.Audit.Origin = types.AuditOriginMutation
			case "erased history":
				history.Erased = map[string]bool{"test-publisher:immutable-import": true}
			}
			payload, err := json.Marshal(envelope)
			require.NoError(t, err)
			err = w.Process(context.Background(), payload, constants.IndexPrefix+"fixture_resource")
			if scenario == "erased history" {
				var erased *contracts.HistoryErasedError
				require.ErrorAs(t, err, &erased)
			} else {
				require.ErrorContains(t, err, "identity conflict")
			}
			require.Same(t, first, history.LastRecord)
			require.Zero(t, notifier.Events)
			require.Empty(t, w.cleanup.(*mocks.MockAuditCleanup).Refs)
		})
	}
}

func TestMessageProcessor_HistoricalImportDoesNotRequireCurrentAdoption(t *testing.T) {
	w, history, current, notifier := workflowFixture(t)
	current.Failure = true
	mp := &MessageProcessor{logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	require.NoError(t, mp.SetAuditWorkflow(w, nil))
	payload := historicalEnvelope(t, constants.ActionDeleted, "unfenced-history", 1)
	require.NoError(t, mp.ProcessIndexingMessage(context.Background(), payload, constants.IndexPrefix+"fixture_resource"))
	require.Len(t, history.Records, 1)
	require.Zero(t, notifier.Events)
	require.Empty(t, w.cleanup.(*mocks.MockAuditCleanup).Refs)
}
