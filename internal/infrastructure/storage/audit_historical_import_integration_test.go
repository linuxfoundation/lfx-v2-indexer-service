// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/application"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// TestAuditOpenSearch_HistoricalImportIntegration exercises the actual workflow,
// receipts and history adapters while comparing current documents before/after.
// Like the other audit integration tests, it requires a disposable isolated node.
func TestAuditOpenSearch_HistoricalImportIntegration(t *testing.T) {
	client, currentIndex := setupAuditIntegration(t)
	ctx := context.Background()
	auth := mocks.NewMockAuthRepository()
	auth.ValidateTokenResponse = &contracts.Principal{Principal: "fixture@clients"}
	policy := services.AuditPublisherPolicy{
		Publisher: "test-publisher", Principal: "fixture@clients", CanAttestActors: true,
		Resources: map[string]services.AuditResourcePolicy{"fixture_resource": {
			Origins: map[types.AuditOrigin]bool{types.AuditOriginHistoricalImport: true},
			Schemas: map[string]func(before, after json.RawMessage) error{"fixture.audit.v1": func(before, after json.RawMessage) error {
				for _, snapshot := range []json.RawMessage{before, after} {
					if len(snapshot) == 0 {
						continue
					}
					var value map[string]string
					if json.Unmarshal(snapshot, &value) != nil || len(value) != 1 || value["name"] == "" {
						return fmt.Errorf("unapproved fixture snapshot")
					}
				}
				return nil
			}},
		}},
	}
	admission, err := services.NewAuditAdmission(auth, []services.AuditPublisherPolicy{policy})
	require.NoError(t, err)
	history := NewHistoryRepository(client)
	projection := NewAuditProjectionRepository(client, currentIndex)
	notifier, cleanup := &mocks.MockAuditNotifier{}, &mocks.MockAuditCleanup{}
	workflow, err := application.NewAuditWorkflow(admission, history, NewAuditDeliveryReceiptRepository(client, time.Second), projection, notifier, cleanup)
	require.NoError(t, err)
	identities, err := services.NewAuditIdentityResolver(&mocks.MockIdentityDirectory{}, nil)
	require.NoError(t, err)
	attribution, err := services.NewAuditAttributionService(auth, identities, slog.Default())
	require.NoError(t, err)
	workflow.SetAttributionService(attribution)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	for _, state := range []string{"absent", "live", "tombstone"} {
		for _, action := range []constants.MessageAction{constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted} {
			for _, revision := range []uint64{1, 100} {
				t.Run(fmt.Sprintf("%s/%s/%d", state, action, revision), func(t *testing.T) {
					id := fmt.Sprintf("%s-%s-%d", state, action, revision)
					resourceEnvelope := func(event string, sourceRevision uint64, operation constants.MessageAction) *types.IndexerMessageEnvelope {
						envelope := auditIntegrationEnvelope(event, sourceRevision, operation, now)
						envelope.IndexingConfig.ObjectID = id
						envelope.IndexingConfig.AccessCheckObject = "fixture_resource:" + id
						if operation == constants.ActionDeleted {
							envelope.Data = id
						} else {
							envelope.Data = map[string]any{"uid": id}
						}
						return envelope
					}
					if state != "absent" {
						seedAction := constants.ActionCreated
						if state == "tombstone" {
							seedAction = constants.ActionDeleted
						}
						seed := resourceEnvelope("seed-"+id, 42, seedAction)
						fingerprint, fingerprintErr := contracts.AuditFingerprint(seed, "fixture_resource")
						require.NoError(t, fingerprintErr)
						_, applied, applyErr := projection.ApplyIfNewer(ctx, seed, "fixture_resource", fingerprint)
						require.NoError(t, applyErr)
						require.True(t, applied)
					}
					before := historicalImportCurrentSource(t, client, currentIndex, "fixture_resource:"+id)
					envelope := resourceEnvelope("import-"+id, revision, action)
					envelope.Audit.Origin = types.AuditOriginHistoricalImport
					envelope.Headers = map[string]string{constants.AuthorizationHeader: "Bearer fixture-credential"}
					payload, marshalErr := json.Marshal(envelope)
					require.NoError(t, marshalErr)
					for range 2 {
						require.NoError(t, workflow.Process(ctx, payload, constants.IndexPrefix+"fixture_resource"))
					}
					record, recordErr := admission.NewRecord(envelope, "fixture_resource", now)
					require.NoError(t, recordErr)
					exists, verifyErr := history.VerifyExisting(ctx, record)
					require.NoError(t, verifyErr)
					require.True(t, exists)
					after := historicalImportCurrentSource(t, client, currentIndex, "fixture_resource:"+id)
					require.Equal(t, string(before), string(after), "even a higher import revision must leave current state unchanged")
					require.Zero(t, notifier.Events)
					require.Empty(t, cleanup.Refs)
					require.NoError(t, history.Erase(ctx, record.Publisher, record.EventID, now))
					var erased *contracts.HistoryErasedError
					require.ErrorAs(t, workflow.Process(ctx, payload, constants.IndexPrefix+"fixture_resource"), &erased)
				})
			}
		}
	}
}

func historicalImportCurrentSource(t *testing.T, client *opensearch.Client, index, id string) json.RawMessage {
	t.Helper()
	response, err := client.Get(index, id)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == 404 {
		return nil
	}
	require.False(t, response.IsError())
	var result struct {
		Source json.RawMessage `json:"_source"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	return result.Source
}
