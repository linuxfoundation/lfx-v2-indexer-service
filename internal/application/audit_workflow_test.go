// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// TestMessageProcessor_RequiredTypeGrammar keeps direct workflow installation
// aligned with startup policy and wire validation for audited resource types.
func TestMessageProcessor_RequiredTypeGrammar(t *testing.T) {
	w, _, _, _ := workflowFixture(t)
	for _, name := range []string{"fixture_resource", "a" + strings.Repeat("b", 63), "index", "a" + strings.Repeat("b", 64)} {
		t.Run(name, func(t *testing.T) {
			mp := &MessageProcessor{}
			err := mp.SetAuditWorkflow(w, []string{name})
			if name == "index" || len(name) > 64 {
				require.ErrorContains(t, err, "invalid audit-required object type")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestAuditWorkflow_MetadataOnlyMutationDoesNotNotify checks identity-only updates
// retain history and schedule cleanup without publishing an obsolete body.
func TestAuditWorkflow_MetadataOnlyMutationDoesNotNotify(t *testing.T) {
	w, history, _, notifier := workflowFixture(t)
	w.current = &mocks.MockAuditProjection{MetadataOnly: true}
	mutation := workflowEnvelope(constants.ActionUpdated, "metadata-only", 42)
	for range 2 {
		require.NoError(t, w.Process(context.Background(), mutation, constants.IndexPrefix+"fixture_resource"))
	}
	require.Len(t, history.Records, 1)
	require.Zero(t, notifier.Events)
	require.Len(t, w.cleanup.(*mocks.MockAuditCleanup).Refs, 2)
}

// workflowEnvelope builds complete synthetic mutation envelopes with the action's
// required snapshot sides and historical authorization scope.
func workflowEnvelope(action constants.MessageAction, event string, revision uint64) []byte {
	e := &types.IndexerMessageEnvelope{
		SchemaVersion: types.AuditSchemaVersion, Action: action,
		Headers:        map[string]string{constants.AuthorizationHeader: "Bearer fixture-credential"},
		Data:           map[string]any{"uid": "fixture-resource"},
		IndexingConfig: &types.IndexingConfig{ObjectID: "fixture-resource", AccessCheckObject: "fixture_resource:fixture-resource", AccessCheckRelation: "viewer", HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor"},
		Audit: &types.AuditEvent{EventID: event, Publisher: "test-publisher", ResourceRevision: revision, Origin: types.AuditOriginMutation,
			OccurredAt:  time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
			Attribution: types.AuditAttribution{ActorRef: "service:fixture", EffectiveActorRef: "service:fixture"},
			Scope:       types.AuditScope{Before: []string{"project:fixture-project"}, After: []string{"project:fixture-project"}},
			Content: &types.AuditContent{Schema: "fixture.audit.v1", Representation: types.AuditRepresentationSnapshots,
				Before: json.RawMessage(`{"name":"Before"}`), After: json.RawMessage(`{"name":"After"}`)},
		},
	}
	if action == constants.ActionCreated {
		e.Audit.Scope.Before = nil
		e.Audit.Content.Before = nil
	}
	if action == constants.ActionDeleted {
		e.Data = "fixture-resource"
		e.Audit.Scope.After = nil
		e.Audit.Content.After = nil
	}
	data, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	return data
}

func workflowFixture(t *testing.T) (*AuditWorkflow, *mocks.MockAuditHistoryRepository, *mocks.MockAuditProjection, *mocks.MockAuditNotifier) {
	t.Helper()
	policy := services.AuditPublisherPolicy{Publisher: "test-publisher", Principal: "fixture@clients", CanAttestActors: true,
		Resources: map[string]services.AuditResourcePolicy{"fixture_resource": {
			Origins: map[types.AuditOrigin]bool{
				types.AuditOriginMutation: true, types.AuditOriginReindex: true,
				types.AuditOriginBackfill: true, types.AuditOriginSyncEcho: true,
			},
			Schemas: map[string]func(before, after json.RawMessage) error{"fixture.audit.v1": func(_, _ json.RawMessage) error { return nil }},
		}},
	}
	auth := mocks.NewMockAuthRepository()
	auth.ValidateTokenResponse = &contracts.Principal{Principal: "fixture@clients"}
	a, err := services.NewAuditAdmission(auth, []services.AuditPublisherPolicy{policy})
	require.NoError(t, err)
	h, c := &mocks.MockAuditHistoryRepository{Records: map[string]string{}}, &mocks.MockAuditProjection{}
	n := &mocks.MockAuditNotifier{}
	receipts := &mocks.MockAuditReceipts{}
	w, err := NewAuditWorkflow(a, h, receipts, c, n, &mocks.MockAuditCleanup{})
	require.NoError(t, err)
	return w, h, c, n
}

// TestAuditWorkflow_LifecycleAndRecovery checks retained mutations, partial-write
// retry, late/duplicate delivery and repair without regressing current state.
func TestAuditWorkflow_LifecycleAndRecovery(t *testing.T) {
	ctx := context.Background()
	w, h, current, notifier := workflowFixture(t)
	created := workflowEnvelope(constants.ActionCreated, "event-1", 1)
	updated := workflowEnvelope(constants.ActionUpdated, "event-2", 2)
	updatedAgain := workflowEnvelope(constants.ActionUpdated, "event-3", 3)
	deleted := workflowEnvelope(constants.ActionDeleted, "event-4", 4)
	subject := constants.IndexPrefix + "fixture_resource"

	current.Failure = true
	require.ErrorContains(t, w.Process(ctx, created, subject), "current unavailable")
	require.Len(t, h.Records, 1) // history-first partial write
	current.Failure = false
	require.NoError(t, w.Process(ctx, created, subject))
	require.NoError(t, w.Process(ctx, updated, subject))
	require.NoError(t, w.Process(ctx, updatedAgain, subject))
	require.NoError(t, w.Process(ctx, deleted, subject))
	require.Len(t, h.Records, 4)
	require.Equal(t, uint64(4), current.Revision)
	require.Equal(t, 4, current.Writes)
	require.Equal(t, 4, notifier.Events)
	require.Len(t, w.cleanup.(*mocks.MockAuditCleanup).Refs, 4)
	var last contracts.IndexingEvent
	require.NoError(t, json.Unmarshal(notifier.Last, &last))
	require.Equal(t, "test-publisher:event-4", last.EventID)

	// An ACK-loss redelivery and late mutation keep history but do not regress
	// the current projection. A source revision with another ID conflicts.
	require.NoError(t, w.Process(ctx, updated, subject))
	require.NoError(t, w.Process(ctx, deleted, subject))
	require.Equal(t, 4, current.Writes)
	require.Equal(t, 4, notifier.Events)
	require.Len(t, w.cleanup.(*mocks.MockAuditCleanup).Refs, 6) // no-op replays still reconcile old duplicates
	conflict := workflowEnvelope(constants.ActionUpdated, "event-5", 4)
	require.ErrorContains(t, w.Process(ctx, conflict, subject), "equal revision conflict")
	require.Equal(t, 4, current.Writes)
	require.Equal(t, 4, notifier.Events)

	// If current was written but history needs reconstruction, repair only
	// from the original authenticated source event.
	delete(h.Records, "test-publisher:event-4")
	require.NoError(t, w.Process(ctx, deleted, subject))
	require.Len(t, h.Records, 5) // includes conflicting event-5 history
	require.Equal(t, 4, current.Writes)
	require.Equal(t, 4, notifier.Events) // history repair via replay is a current no-op

	h.Fail = true
	require.ErrorContains(t, w.Process(ctx, created, subject), "history unavailable")
	require.Equal(t, 4, current.Writes)
}

// TestAuditWorkflow_ProjectionDoesNotCreateHistory keeps authorized rebuild and
// sync origins out of the domain-mutation history index.
func TestAuditWorkflow_ProjectionDoesNotCreateHistory(t *testing.T) {
	for _, origin := range []types.AuditOrigin{types.AuditOriginReindex, types.AuditOriginBackfill, types.AuditOriginSyncEcho} {
		t.Run(string(origin), func(t *testing.T) {
			w, h, current, _ := workflowFixture(t)
			payload := workflowEnvelope(constants.ActionUpdated, "projection-1", 3)
			var e types.IndexerMessageEnvelope
			require.NoError(t, json.Unmarshal(payload, &e))
			e.Audit.Origin = origin
			e.Audit.Content = nil
			payload, err := json.Marshal(e)
			require.NoError(t, err)
			require.NoError(t, w.Process(context.Background(), payload, constants.IndexPrefix+"fixture_resource"))
			require.Empty(t, h.Records)
			require.Equal(t, uint64(3), current.Revision)
		})
	}
}

func TestAuditWorkflow_ProjectionIdentityCannotReuseMutationID(t *testing.T) {
	w, history, current, _ := workflowFixture(t)
	subject := constants.IndexPrefix + "fixture_resource"
	require.NoError(t, w.Process(context.Background(), workflowEnvelope(constants.ActionCreated, "shared-id", 1), subject))
	projection := workflowEnvelope(constants.ActionUpdated, "shared-id", 2)
	var e types.IndexerMessageEnvelope
	require.NoError(t, json.Unmarshal(projection, &e))
	e.Audit.Origin = types.AuditOriginReindex
	e.Audit.Content = nil
	projection, err := json.Marshal(e)
	require.NoError(t, err)
	require.ErrorContains(t, w.Process(context.Background(), projection, subject), "identity conflict")
	require.Len(t, history.Records, 1)
	require.Equal(t, 1, current.Writes)
}

// TestAuditWorkflow_ErasedIdentityCannotDriveCurrentWrites checks erased-ID reuse
// fails closed while a separately authorized projection can reconcile a tombstone.
func TestAuditWorkflow_ErasedIdentityCannotDriveCurrentWrites(t *testing.T) {
	w, h, current, _ := workflowFixture(t)
	h.Erased = map[string]bool{"test-publisher:erased-delete": true, "test-publisher:erased-update": true}
	subject := constants.IndexPrefix + "fixture_resource"
	err := w.Process(context.Background(), workflowEnvelope(constants.ActionUpdated, "erased-update", 1), subject)
	require.ErrorContains(t, err, "explicitly erased")
	require.Zero(t, current.Writes)
	// An erased update ID repurposed as a delete (even for another resource)
	// must not bypass the missing immutable history proof.
	err = w.Process(context.Background(), workflowEnvelope(constants.ActionDeleted, "erased-update", 2), subject)
	require.ErrorContains(t, err, "identity conflict")
	err = w.Process(context.Background(), workflowEnvelope(constants.ActionDeleted, "erased-delete", 2), subject)
	require.ErrorContains(t, err, "explicitly erased")
	require.Zero(t, current.Writes)
	// A separate authenticated projection operation can reconcile the current
	// tombstone without reusing an erased mutation ID or inventing history.
	projection := workflowEnvelope(constants.ActionDeleted, "repair-delete", 2)
	var e types.IndexerMessageEnvelope
	require.NoError(t, json.Unmarshal(projection, &e))
	e.Audit.Origin = types.AuditOriginBackfill
	e.Audit.Content = nil
	projection, err = json.Marshal(e)
	require.NoError(t, err)
	require.NoError(t, w.Process(context.Background(), projection, subject))
	require.Equal(t, uint64(2), current.Revision)
	require.Empty(t, h.Records)
}

func TestAuditWorkflow_NotificationFailureDoesNotLoseCurrentWrite(t *testing.T) {
	w, history, current, notifier := workflowFixture(t)
	notifier.Failure = true
	err := w.Process(context.Background(), workflowEnvelope(constants.ActionCreated, "event-1", 1), constants.IndexPrefix+"fixture_resource")
	require.NoError(t, err)
	require.Len(t, history.Records, 1)
	require.Equal(t, 1, current.Writes)
	require.Equal(t, 1, notifier.Events)
}

func TestMessageProcessor_AuditRoutingAndLegacyGuard(t *testing.T) {
	w, history, current, _ := workflowFixture(t)
	mp := &MessageProcessor{auditWorkflow: w, auditRequiredTypes: map[string]bool{"fixture_resource": true}, logger: slog.Default()}
	subject := constants.IndexPrefix + "fixture_resource"
	require.NoError(t, mp.ProcessIndexingMessage(context.Background(), workflowEnvelope(constants.ActionCreated, "event-1", 1), subject))
	require.Len(t, history.Records, 1)
	require.Equal(t, 1, current.Writes)
	legacy := []byte(`{"action":"updated","data":{"uid":"fixture-resource"}}`)
	require.ErrorContains(t, mp.ProcessIndexingMessage(context.Background(), legacy, subject), "requires schema_version 2")
	require.ErrorContains(t, mp.ProcessV1IndexingMessage(context.Background(), legacy, constants.FromV1Prefix+"fixture_resource"), "rejects platform V1")
	require.Equal(t, 1, current.Writes)
	invalid := []byte(`{"schema_version":3,"action":"updated"}`)
	require.Error(t, mp.ProcessIndexingMessage(context.Background(), invalid, subject))
	require.Equal(t, 1, current.Writes)
}

func TestMessageProcessor_RejectsMutationOutsideRequiredTypes(t *testing.T) {
	w, _, _, _ := workflowFixture(t)
	mp := &MessageProcessor{auditWorkflow: w, auditRequiredTypes: map[string]bool{}, logger: slog.Default()}
	subject := constants.IndexPrefix + "fixture_resource"
	require.ErrorContains(t, mp.ProcessIndexingMessage(context.Background(), workflowEnvelope(constants.ActionCreated, "event-1", 1), subject), "not audit-required")
}

// TestMessageProcessor_UnfencedProjectionOrigins keeps direct routing aligned
// with startup coverage: only approved backfill may prepare an unfenced type.
func TestMessageProcessor_UnfencedProjectionOrigins(t *testing.T) {
	for _, origin := range []types.AuditOrigin{types.AuditOriginBackfill, types.AuditOriginReindex, types.AuditOriginSyncEcho} {
		t.Run(string(origin), func(t *testing.T) {
			w, _, current, _ := workflowFixture(t)
			mp := &MessageProcessor{auditWorkflow: w, auditRequiredTypes: map[string]bool{}, logger: slog.Default()}
			var e types.IndexerMessageEnvelope
			require.NoError(t, json.Unmarshal(workflowEnvelope(constants.ActionUpdated, "unfenced-projection", 1), &e))
			e.Audit.Origin, e.Audit.Content = origin, nil
			payload, err := json.Marshal(e)
			require.NoError(t, err)
			err = mp.ProcessIndexingMessage(context.Background(), payload, constants.IndexPrefix+"fixture_resource")
			if origin == types.AuditOriginBackfill {
				require.NoError(t, err)
				require.Equal(t, 1, current.Writes)
			} else {
				require.ErrorContains(t, err, "not audit-required")
				require.Zero(t, current.Writes)
			}
		})
	}
}
