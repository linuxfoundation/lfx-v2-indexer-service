// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package cleanup

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

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/storage"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// TestAuditCleanup_RealOpenSearchCanonicalWins verifies the guarded canonical
// document outranks a stale legacy tombstone during real-index reconciliation.
func TestAuditCleanup_RealOpenSearchCanonicalWins(t *testing.T) {
	endpoint := os.Getenv("TEST_OPENSEARCH_URL")
	if endpoint == "" || os.Getenv("TEST_OPENSEARCH_ISOLATED") != "true" {
		t.Skip("requires an explicitly isolated disposable OpenSearch instance")
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	index := fmt.Sprintf("audit-cleanup-fixture-%d", time.Now().UnixNano())
	httpClient := &http.Client{Timeout: 15 * time.Second}
	request := func(method, path string, body []byte, expected int) map[string]any {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), method, endpoint+"/"+index+path, bytes.NewReader(body))
		require.NoError(t, err)
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := httpClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		require.Equal(t, expected, res.StatusCode) // never echo OpenSearch bodies with resource values
		if method == http.MethodGet {
			var value map[string]any
			require.NoError(t, json.NewDecoder(res.Body).Decode(&value))
			return value
		}
		return nil
	}
	request(http.MethodPut, "", []byte(`{"mappings":{"properties":{"object_ref":{"type":"keyword"},"latest":{"type":"boolean"},"deleted_at":{"type":"date"},"updated_at":{"type":"date"},"audit_source_revision":{"type":"long","index":false}}}}`), http.StatusOK)
	t.Cleanup(func() { request(http.MethodDelete, "", nil, http.StatusOK) })
	objectRef := "fixture_resource:fixture-resource"
	request(http.MethodPut, "/_doc/legacy-tombstone?refresh=wait_for", []byte(`{"object_ref":"fixture_resource:fixture-resource","latest":true,"deleted_at":"2026-11-05T12:00:00Z"}`), http.StatusCreated)
	request(http.MethodPut, "/_doc/fixture_resource:fixture-resource?refresh=wait_for", []byte(`{"object_ref":"fixture_resource:fixture-resource","latest":true,"updated_at":"2026-11-05T11:00:00Z","audit_source_revision":7}`), http.StatusCreated)
	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{endpoint}})
	require.NoError(t, err)
	logger, _ := logging.TestLogger(t)
	janitor := NewCleanupRepository(storage.NewStorageRepository(client, logger), logger, index)
	require.Equal(t, "conflict_resolved", janitor.processItem(context.Background(), &objectRef))
	legacy := request(http.MethodGet, "/_doc/legacy-tombstone", nil, http.StatusOK)
	canonical := request(http.MethodGet, "/_doc/fixture_resource:fixture-resource", nil, http.StatusOK)
	require.Equal(t, false, legacy["_source"].(map[string]any)["latest"])
	require.Equal(t, true, canonical["_source"].(map[string]any)["latest"])
}

// TestAuditCleanup_UnrefreshedCanonicalRetries disables automatic refresh to
// deterministically expose the legacy-only search window after a guarded delete.
func TestAuditCleanup_UnrefreshedCanonicalRetries(t *testing.T) {
	for _, tt := range []struct{ name, id string }{
		{"plain ID", "fixture-resource"},
		{"quoted ID", `resource"child`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			testAuditCleanupUnrefreshedCanonical(t, tt.id)
		})
	}
}

func testAuditCleanupUnrefreshedCanonical(t *testing.T, objectID string) {
	t.Helper()
	endpoint := os.Getenv("TEST_OPENSEARCH_URL")
	if endpoint == "" || os.Getenv("TEST_OPENSEARCH_ISOLATED") != "true" {
		t.Skip("requires an explicitly isolated disposable OpenSearch instance")
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	index := fmt.Sprintf("audit-cleanup-refresh-fixture-%d", time.Now().UnixNano())
	httpClient := &http.Client{Timeout: 15 * time.Second}
	request := func(method, path string, body []byte, expected int) map[string]any {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), method, endpoint+"/"+index+path, bytes.NewReader(body))
		require.NoError(t, err)
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := httpClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		require.Equal(t, expected, res.StatusCode) // never print resource payloads
		if method == http.MethodGet {
			var value map[string]any
			require.NoError(t, json.NewDecoder(res.Body).Decode(&value))
			return value
		}
		return nil
	}
	request(http.MethodPut, "", []byte(`{"settings":{"index":{"refresh_interval":"-1"}},"mappings":{"properties":{"object_ref":{"type":"keyword"},"latest":{"type":"boolean"},"deleted_at":{"type":"date"},"updated_at":{"type":"date"},"audit_source_revision":{"type":"long","index":false}}}}`), http.StatusOK)
	t.Cleanup(func() { request(http.MethodDelete, "", nil, http.StatusOK) })
	ref := "fixture_resource:" + objectID
	legacyBody, err := json.Marshal(map[string]any{"object_ref": ref, "latest": true, "updated_at": "2026-11-05T11:00:00Z"})
	require.NoError(t, err)
	request(http.MethodPut, "/_doc/legacy-live?refresh=true", legacyBody, http.StatusCreated)
	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{endpoint}})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	e := &types.IndexerMessageEnvelope{
		Action: constants.ActionDeleted, Data: objectID,
		IndexingConfig: &types.IndexingConfig{ObjectID: objectID, HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor"},
		Audit: &types.AuditEvent{EventID: "refresh-delete", Publisher: "test-publisher", Origin: types.AuditOriginMutation, ResourceRevision: 2,
			OccurredAt: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC), Attribution: types.AuditAttribution{ActorRef: "service:fixture", EffectiveActorRef: "service:fixture"}},
	}
	_, applied, err := storage.NewAuditProjectionRepository(client, index).ApplyIfNewer(ctx, e, "fixture_resource", strings.Repeat("a", 64))
	require.NoError(t, err)
	require.True(t, applied)
	logger, _ := logging.TestLogger(t)
	janitor := NewCleanupRepository(storage.NewStorageRepository(client, logger), logger, index)
	t.Cleanup(func() { cancel(); janitor.retryWG.Wait() })
	for len(globalJanitorChan) > 0 {
		<-globalJanitorChan
	}
	janitor.CheckAuditedItem(ref)
	queued := <-globalJanitorChan
	require.True(t, queued.requireAuditedCanonical)
	require.Equal(t, "retry_scheduled", janitor.processItemWithPolicy(ctx, queued))
	legacy := request(http.MethodGet, "/_doc/legacy-live", nil, http.StatusOK)
	require.Equal(t, true, legacy["_source"].(map[string]any)["latest"])
	canonical := request(http.MethodGet, "/_doc/"+url.PathEscape(ref), nil, http.StatusOK)
	require.NotEmpty(t, canonical["_source"].(map[string]any)["deleted_at"])
	// Make the canonical tombstone visible only after the first cleanup attempt.
	request(http.MethodPost, "/_refresh", nil, http.StatusOK)
	select {
	case retry := <-globalJanitorChan:
		require.Equal(t, ref, retry.objectRef)
		require.True(t, retry.requireAuditedCanonical)
		require.Equal(t, 1, retry.visibilityAttempts)
		require.Equal(t, "conflict_resolved", janitor.processItemWithPolicy(ctx, retry))
	case <-ctx.Done():
		t.Fatal("audited cleanup was not requeued before its deadline")
	}
	legacy = request(http.MethodGet, "/_doc/legacy-live", nil, http.StatusOK)
	require.Equal(t, false, legacy["_source"].(map[string]any)["latest"])
	request(http.MethodPost, "/_refresh", nil, http.StatusOK)
	query, err := json.Marshal(map[string]any{"query": map[string]any{"bool": map[string]any{"filter": []map[string]any{
		{"term": map[string]any{"object_ref": ref}}, {"term": map[string]any{"latest": true}},
	}}}})
	require.NoError(t, err)
	visible := request(http.MethodGet, "/_search", query, http.StatusOK)
	hits := visible["hits"].(map[string]any)["hits"].([]any)
	require.Len(t, hits, 1)
	require.Equal(t, ref, hits[0].(map[string]any)["_id"])
}
