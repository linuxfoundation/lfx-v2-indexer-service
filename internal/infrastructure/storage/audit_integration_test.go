// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// These tests create and delete indices. Only run against an explicitly
// isolated disposable OpenSearch instance, never an existing shared cluster.
func auditIntegrationClient(t *testing.T) (*opensearch.Client, string) {
	t.Helper()
	endpoint := os.Getenv("TEST_OPENSEARCH_URL")
	if endpoint == "" || os.Getenv("TEST_OPENSEARCH_ISOLATED") != "true" {
		t.Skip("requires an explicitly isolated disposable OpenSearch instance")
	}
	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{endpoint}})
	require.NoError(t, err)
	return client, strings.TrimSuffix(endpoint, "/")
}

// auditIndexRequest provisions only isolated fixture indices and reports HTTP
// status mismatches without printing OpenSearch payloads.
func auditIndexRequest(t *testing.T, method, url string, body []byte, expected int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(body))
	require.NoError(t, err)
	if len(body) != 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != expected {
		// Do not echo server error bodies: they can contain resource values.
		t.Fatalf("OpenSearch index request returned status %d, want %d", res.StatusCode, expected)
	}
}

func setupAuditIntegration(t *testing.T) (*opensearch.Client, string) {
	t.Helper()
	client, endpoint := auditIntegrationClient(t)
	currentIndex := fmt.Sprintf("audit-current-fixture-%d", time.Now().UnixNano())
	// The history repository intentionally targets one fixed private index.
	// Refuse to touch an existing one even on a declared isolated instance.
	auditIndexRequest(t, http.MethodHead, endpoint+"/"+historyIndex, nil, http.StatusNotFound)
	historyMapping, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/opensearch-resource-history-v1.json")
	require.NoError(t, err)
	auditIndexRequest(t, http.MethodPut, endpoint+"/"+historyIndex, historyMapping, http.StatusOK)
	t.Cleanup(func() {
		auditIndexRequest(t, http.MethodDelete, endpoint+"/"+historyIndex, nil, http.StatusOK)
	})
	auditIndexRequest(t, http.MethodHead, endpoint+"/"+auditDeliveryReceiptsIndex, nil, http.StatusNotFound)
	deliveryMapping, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/opensearch-audit-deliveries-v1.json")
	require.NoError(t, err)
	auditIndexRequest(t, http.MethodPut, endpoint+"/"+auditDeliveryReceiptsIndex, deliveryMapping, http.StatusOK)
	t.Cleanup(func() {
		auditIndexRequest(t, http.MethodDelete, endpoint+"/"+auditDeliveryReceiptsIndex, nil, http.StatusOK)
	})
	projectionMapping, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/opensearch-audit-projection-mapping.json")
	require.NoError(t, err)
	var mapping json.RawMessage
	require.NoError(t, json.Unmarshal(projectionMapping, &mapping))
	currentMapping := append([]byte(`{"mappings":`), mapping...)
	currentMapping = append(currentMapping, '}')
	auditIndexRequest(t, http.MethodPut, endpoint+"/"+currentIndex, currentMapping, http.StatusOK)
	t.Cleanup(func() {
		auditIndexRequest(t, http.MethodDelete, endpoint+"/"+currentIndex, nil, http.StatusOK)
	})
	require.NoError(t, VerifyAuditMappings(context.Background(), client, currentIndex, 10*time.Second))
	return client, currentIndex
}

// TestAuditOpenSearch_RevisionAndHistoryIntegration checks retained identity,
// ordering, erasure and opaque document IDs against the actual OpenSearch APIs.
func TestAuditOpenSearch_RevisionAndHistoryIntegration(t *testing.T) {
	client, currentIndex := setupAuditIntegration(t)
	projection := NewAuditProjectionRepository(client, currentIndex)
	history := NewHistoryRepository(client)
	receipts := NewAuditDeliveryReceiptRepository(client, 10*time.Second)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	fixture := func(eventID string, revision uint64, action constants.MessageAction) *types.IndexerMessageEnvelope {
		e := &types.IndexerMessageEnvelope{
			Action: action, Data: map[string]any{"uid": "fixture-resource"},
			IndexingConfig: &types.IndexingConfig{ObjectID: "fixture-resource", AccessCheckObject: "fixture_resource:fixture-resource", AccessCheckRelation: "viewer", HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor"},
			Audit: &types.AuditEvent{EventID: eventID, Publisher: "test-publisher", Origin: types.AuditOriginMutation, ResourceRevision: revision, OccurredAt: now,
				Attribution: types.AuditAttribution{ActorRef: "service:fixture", EffectiveActorRef: "service:fixture"}},
		}
		if action == constants.ActionDeleted {
			e.Data = "fixture-resource"
		}
		return e
	}
	apply := func(t *testing.T, e *types.IndexerMessageEnvelope, wantApplied bool) {
		t.Helper()
		fingerprint, fingerprintErr := contracts.AuditFingerprint(e, "fixture_resource")
		require.NoError(t, fingerprintErr)
		require.NoError(t, receipts.CreateOrVerify(context.Background(), contracts.AuditDeliveryReceipt{
			Publisher: e.Audit.Publisher, EventID: e.Audit.EventID,
			Origin: e.Audit.Origin, Fingerprint: fingerprint,
		}))
		_, applied, applyErr := projection.ApplyIfNewer(context.Background(), e, "fixture_resource", fingerprint)
		require.NoError(t, applyErr)
		require.Equal(t, wantApplied, applied)
	}

	created := fixture("created-1", 1, constants.ActionCreated)
	record := &contracts.HistoryRecord{Publisher: "test-publisher", EventID: "created-1", ObjectType: "fixture_resource", ObjectID: "fixture-resource", Action: constants.ActionCreated, ResourceRevision: 1, OccurredAt: now, IngestedAt: now, Fingerprint: strings.Repeat("a", 64), Audit: *created.Audit}
	createdHistory, err := history.CreateOrVerify(context.Background(), record)
	require.NoError(t, err)
	require.True(t, createdHistory)
	createdHistory, err = history.CreateOrVerify(context.Background(), record)
	require.NoError(t, err)
	require.False(t, createdHistory)
	// An approved snapshot can fit the 1 MiB source envelope while HTML
	// escaping expands the stored _source beyond 2 MiB. Duplicate verification
	// must fetch only the small identity fields, not that whole payload.
	large := *record
	large.EventID = "large-snapshot"
	large.Audit = record.Audit
	large.Audit.EventID = large.EventID
	large.Audit.Content = &types.AuditContent{Schema: "fixture.audit.v1", Representation: types.AuditRepresentationSnapshots,
		After: json.RawMessage(`{"name":"` + strings.Repeat("<", 400000) + `"}`)}
	createdHistory, err = history.CreateOrVerify(context.Background(), &large)
	require.NoError(t, err)
	require.True(t, createdHistory)
	createdHistory, err = history.CreateOrVerify(context.Background(), &large)
	require.NoError(t, err)
	require.False(t, createdHistory)
	apply(t, created, true)
	apply(t, created, false) // lost ACK
	updated := fixture("updated-2", 2, constants.ActionUpdated)
	apply(t, updated, true)
	apply(t, created, false) // late event cannot regress the current document
	conflict := fixture("other-2", 2, constants.ActionUpdated)
	fingerprint, err := contracts.AuditFingerprint(conflict, "fixture_resource")
	require.NoError(t, err)
	_, _, err = projection.ApplyIfNewer(context.Background(), conflict, "fixture_resource", fingerprint)
	require.ErrorContains(t, err, "status 400")
	deleted := fixture("deleted-3", 3, constants.ActionDeleted)
	apply(t, deleted, true)
	apply(t, deleted, false)
	// Distinct projection operations at one source revision have durable
	// identities. Replaying A after B cannot roll the current document back.
	projectionA := fixture("projection-a", 4, constants.ActionUpdated)
	projectionA.Audit.Origin = types.AuditOriginReindex
	projectionA.Audit.OccurredAt = now.Add(time.Second)
	projectionB := fixture("projection-b", 4, constants.ActionUpdated)
	projectionB.Audit.Origin = types.AuditOriginReindex
	projectionB.Audit.OccurredAt = now.Add(2 * time.Second)
	apply(t, projectionA, true)
	apply(t, projectionB, true)
	apply(t, projectionA, false)
	apply(t, projectionB, false)
	reused := fixture("projection-a", 4, constants.ActionUpdated)
	reused.Audit.Origin = types.AuditOriginReindex
	reused.Audit.OccurredAt = now.Add(3 * time.Second)
	fingerprint, err = contracts.AuditFingerprint(reused, "fixture_resource")
	require.NoError(t, err)
	require.ErrorContains(t, receipts.CreateOrVerify(context.Background(), contracts.AuditDeliveryReceipt{
		Publisher: reused.Audit.Publisher, EventID: reused.Audit.EventID,
		Origin: reused.Audit.Origin, Fingerprint: fingerprint,
	}), "identity reused")

	for _, action := range []constants.MessageAction{constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted} {
		for _, projectionFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("same-revision/%s/projection-first-%t", action, projectionFirst), func(t *testing.T) {
				resourceID := fmt.Sprintf("arrival-%s-%t", action, projectionFirst)
				makeEnvelope := func(eventID string) *types.IndexerMessageEnvelope {
					e := fixture(eventID+"-"+resourceID, 10, action)
					e.IndexingConfig.ObjectID = resourceID
					e.IndexingConfig.AccessCheckObject = "fixture_resource:" + resourceID
					if action == constants.ActionDeleted {
						e.Data = resourceID
					} else {
						e.Data = map[string]any{"uid": resourceID, "name": "original"}
					}
					return e
				}
				mutation := makeEnvelope("mutation")
				rebuilt := makeEnvelope("projection")
				rebuilt.Audit.Origin = types.AuditOriginReindex
				rebuilt.Audit.OccurredAt = now.Add(time.Second)
				rebuilt.Tags = []string{"rebuilt"}
				if action != constants.ActionDeleted {
					rebuilt.Data = map[string]any{"uid": resourceID, "name": "rebuilt"}
					rebuilt.IndexingConfig.SortName = "rebuilt"
				}
				if projectionFirst {
					apply(t, rebuilt, true)
					apply(t, mutation, false) // identity-only update must not notify
				} else {
					apply(t, mutation, true)
					apply(t, rebuilt, true)
				}
				apply(t, mutation, false)
				apply(t, rebuilt, false)
				res, err := client.Get(currentIndex, "fixture_resource:"+resourceID)
				require.NoError(t, err)
				defer func() { _ = res.Body.Close() }()
				require.Equal(t, http.StatusOK, res.StatusCode)
				var stored struct {
					Source map[string]any `json:"_source"`
				}
				require.NoError(t, json.NewDecoder(res.Body).Decode(&stored))
				require.Equal(t, "test-publisher:"+rebuilt.Audit.EventID, stored.Source["audit_event_key"])
				require.Equal(t, "test-publisher:"+mutation.Audit.EventID, stored.Source["audit_mutation_key"])
				mutationFingerprint, err := contracts.AuditFingerprint(mutation, "fixture_resource")
				require.NoError(t, err)
				require.Equal(t, mutationFingerprint, stored.Source["audit_mutation_fingerprint"])
				require.Equal(t, []any{"rebuilt"}, stored.Source["tags"])
				if action != constants.ActionDeleted {
					require.Equal(t, "rebuilt", stored.Source["sort_name"])
					require.Equal(t, "rebuilt", stored.Source["data"].(map[string]any)["name"])
				}
				conflict := makeEnvelope("conflicting-mutation")
				conflictFingerprint, err := contracts.AuditFingerprint(conflict, "fixture_resource")
				require.NoError(t, err)
				_, _, err = projection.ApplyIfNewer(context.Background(), conflict, "fixture_resource", conflictFingerprint)
				require.ErrorContains(t, err, "status 400")
			})
		}
	}

	t.Run("reserved-character-ids", func(t *testing.T) {
		for i, resourceID := range []string{"reserved?one", "reserved?two", "reserved/child", "reserved%child", "reserved%2Fchild", "reserved+and&equals="} {
			e := fixture(fmt.Sprintf("reserved-id-%d", i), 1, constants.ActionCreated)
			e.IndexingConfig.ObjectID = resourceID
			e.IndexingConfig.AccessCheckObject = "fixture_resource:" + resourceID
			e.Data = map[string]any{"uid": resourceID}
			apply(t, e, true)
			apply(t, e, false)
			res, err := client.Get(currentIndex, url.PathEscape("fixture_resource:"+resourceID))
			require.NoError(t, err)
			var stored struct {
				ID     string `json:"_id"`
				Source struct {
					ObjectID  string `json:"object_id"`
					ObjectRef string `json:"object_ref"`
				} `json:"_source"`
			}
			decodeErr := json.NewDecoder(res.Body).Decode(&stored)
			_ = res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.NoError(t, decodeErr)
			require.Equal(t, "fixture_resource:"+resourceID, stored.ID)
			require.Equal(t, stored.ID, stored.Source.ObjectRef)
			require.Equal(t, resourceID, stored.Source.ObjectID)
		}
	})

	require.NoError(t, history.Erase(context.Background(), "test-publisher", "created-1", now.Add(time.Hour)))
	_, err = history.CreateOrVerify(context.Background(), record)
	require.ErrorContains(t, err, "explicitly erased")
	// Do not print the retained payload; the fixture validates the index path
	// and erasure fence without exposing user fields to general test logs.
}

func TestAuditOpenSearch_CurrentAliasMapping(t *testing.T) {
	client, currentIndex := setupAuditIntegration(t)
	endpoint := strings.TrimSuffix(os.Getenv("TEST_OPENSEARCH_URL"), "/")
	alias := currentIndex + "-alias"
	aliasActions := func(actions ...map[string]any) {
		t.Helper()
		auditIndexRequest(t, http.MethodPost, endpoint+"/_aliases", mustJSON(t, map[string]any{"actions": actions}), http.StatusOK)
	}
	aliasActions(map[string]any{"add": map[string]any{"index": currentIndex, "alias": alias, "is_write_index": true}})
	require.NoError(t, VerifyAuditMappings(context.Background(), client, alias, 10*time.Second))

	// Revision N lives in the original backing index before rollover.
	e := concurrentAuditEnvelope("alias-fixture", "alias-fixture-event", 2, constants.ActionUpdated)
	fingerprint, err := contracts.AuditFingerprint(e, "fixture_resource")
	require.NoError(t, err)
	_, applied, err := NewAuditProjectionRepository(client, alias).ApplyIfNewer(context.Background(), e, "fixture_resource", fingerprint)
	require.NoError(t, err)
	require.True(t, applied)
	stored := readAuditSource(t, client, currentIndex, "fixture_resource:alias-fixture")
	require.Equal(t, float64(2), stored["audit_source_revision"])

	secondIndex := currentIndex + "-rollover"
	projectionMapping, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/opensearch-audit-projection-mapping.json")
	require.NoError(t, err)
	body := append(append([]byte(`{"mappings":`), projectionMapping...), '}')
	auditIndexRequest(t, http.MethodPut, endpoint+"/"+secondIndex, body, http.StatusOK)
	t.Cleanup(func() {
		// Deleting these fixture indices also removes their temporary alias.
		auditIndexRequest(t, http.MethodDelete, endpoint+"/"+secondIndex, nil, http.StatusOK)
	})
	aliasActions(
		map[string]any{"add": map[string]any{"index": currentIndex, "alias": alias, "is_write_index": false}},
		map[string]any{"add": map[string]any{"index": secondIndex, "alias": alias, "is_write_index": true}},
	)
	// Both mappings are individually valid. Startup must nevertheless reject
	// this alias before a stale event can create a second guarded document.
	require.ErrorContains(t, VerifyAuditMappings(context.Background(), client, alias, 10*time.Second), "current-resource index must resolve to one backing index")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := client.Get(secondIndex, "fixture_resource:alias-fixture", client.Get.WithContext(ctx))
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

// TestAuditOpenSearch_RevisionDocValuesMapping checks actual mapping metadata
// preserves the janitor sort requirement without rejecting default doc values.
func TestAuditOpenSearch_RevisionDocValuesMapping(t *testing.T) {
	client, currentIndex := setupAuditIntegration(t) // omitted/default doc values pass
	endpoint := strings.TrimSuffix(os.Getenv("TEST_OPENSEARCH_URL"), "/")
	fixture, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/opensearch-audit-projection-mapping.json")
	require.NoError(t, err)
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit-%t", enabled), func(t *testing.T) {
			index := fmt.Sprintf("%s-doc-values-%t", currentIndex, enabled)
			var mapping map[string]any
			require.NoError(t, json.Unmarshal(fixture, &mapping))
			mapping["properties"].(map[string]any)["audit_source_revision"].(map[string]any)["doc_values"] = enabled
			body, err := json.Marshal(map[string]any{"mappings": mapping})
			require.NoError(t, err)
			auditIndexRequest(t, http.MethodPut, endpoint+"/"+index, body, http.StatusOK)
			t.Cleanup(func() { auditIndexRequest(t, http.MethodDelete, endpoint+"/"+index, nil, http.StatusOK) })
			err = VerifyAuditMappings(context.Background(), client, index, 10*time.Second)
			if enabled {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "doc values must be enabled")
			}
		})
	}
}

// TestAuditOpenSearch_LegacyBootstrap executes the legacy rejection and backfill
// branches on an existing canonical document, not a freshly created audited one.
func TestAuditOpenSearch_LegacyBootstrap(t *testing.T) {
	client, currentIndex := setupAuditIntegration(t)
	endpoint := strings.TrimSuffix(os.Getenv("TEST_OPENSEARCH_URL"), "/")
	ref := "fixture_resource:legacy-bootstrap"
	auditIndexRequest(t, http.MethodPut, endpoint+"/"+currentIndex+"/_doc/"+url.PathEscape(ref),
		[]byte(`{"object_ref":"fixture_resource:legacy-bootstrap","object_id":"legacy-bootstrap","latest":true,"data":{"uid":"legacy-bootstrap","name":"legacy"}}`), http.StatusCreated)
	original := readAuditSource(t, client, currentIndex, ref)
	projection := NewAuditProjectionRepository(client, currentIndex)
	for _, origin := range []types.AuditOrigin{types.AuditOriginMutation, types.AuditOriginReindex} {
		e := concurrentAuditEnvelope("legacy-bootstrap", "bootstrap-rejected-"+string(origin), 3, constants.ActionUpdated)
		e.Audit.Origin = origin
		if origin != types.AuditOriginMutation {
			e.Audit.Content = nil
		}
		fingerprint, err := contracts.AuditFingerprint(e, "fixture_resource")
		require.NoError(t, err)
		_, applied, err := projection.ApplyIfNewer(context.Background(), e, "fixture_resource", fingerprint)
		require.ErrorContains(t, err, "status 400")
		require.False(t, applied)
		require.Equal(t, original, readAuditSource(t, client, currentIndex, ref))
	}
	backfill := concurrentAuditEnvelope("legacy-bootstrap", "bootstrap-backfill", 3, constants.ActionUpdated)
	backfill.Audit.Origin, backfill.Audit.Content = types.AuditOriginBackfill, nil
	fingerprint, err := contracts.AuditFingerprint(backfill, "fixture_resource")
	require.NoError(t, err)
	_, applied, err := projection.ApplyIfNewer(context.Background(), backfill, "fixture_resource", fingerprint)
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, float64(3), readAuditSource(t, client, currentIndex, ref)["audit_source_revision"])
	mutation := concurrentAuditEnvelope("legacy-bootstrap", "bootstrap-following-mutation", 4, constants.ActionUpdated)
	fingerprint, err = contracts.AuditFingerprint(mutation, "fixture_resource")
	require.NoError(t, err)
	_, applied, err = projection.ApplyIfNewer(context.Background(), mutation, "fixture_resource", fingerprint)
	require.NoError(t, err)
	require.True(t, applied)
	current := readAuditSource(t, client, currentIndex, ref)
	require.Equal(t, float64(4), current["audit_source_revision"])
	require.Equal(t, "test-publisher:bootstrap-following-mutation", current["audit_mutation_key"])
}
