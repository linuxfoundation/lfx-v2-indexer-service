// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// TestAuditProjectionRepository_AtomicUpdateRequest checks script inputs,
// bootstrap authorization and opt-in refresh waiting without copying credentials.
func TestAuditProjectionRepository_AtomicUpdateRequest(t *testing.T) {
	for _, tt := range []struct {
		name, origin       string
		bootstrap, waitFor bool
	}{
		{"mutation", string(types.AuditOriginMutation), false, false},
		{"backfill", string(types.AuditOriginBackfill), true, false},
		{"mutation waits for refresh", string(types.AuditOriginMutation), false, true},
		{"backfill waits for refresh", string(types.AuditOriginBackfill), true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/resources/_update/fixture_resource:fixture-resource", r.URL.Path)
				require.Equal(t, "8", r.URL.Query().Get("retry_on_conflict"))
				if tt.waitFor {
					require.Equal(t, constants.RefreshWaitFor, r.URL.Query().Get("refresh"))
				} else {
					require.Equal(t, constants.RefreshFalse, r.URL.Query().Get("refresh"))
				}
				require.Equal(t, "audit_event_key,audit_event_fingerprint,audit_mutation_key,audit_mutation_fingerprint", r.URL.Query().Get("_source_includes"))
				var request struct {
					ScriptedUpsert bool `json:"scripted_upsert"`
					Script         struct {
						Source string         `json:"source"`
						Params map[string]any `json:"params"`
					} `json:"script"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.True(t, request.ScriptedUpsert)
				require.Equal(t, auditProjectionScript, request.Script.Source)
				require.Equal(t, tt.bootstrap, request.Script.Params["bootstrap"])
				require.Equal(t, tt.bootstrap, request.Script.Params["projection_only"])
				require.Equal(t, "test-publisher:fixture-event-1", request.Script.Params["identity"])
				doc := request.Script.Params["document"].(map[string]any)
				require.Equal(t, "fixture_resource:fixture-resource", doc["object_ref"])
				require.NotContains(t, string(mustJSON(t, request)), "Bearer")
				_, _ = w.Write([]byte(`{"result":"updated","get":{"_source":{"audit_event_key":"test-publisher:fixture-event-1","audit_event_fingerprint":"fixture-fingerprint"}}}`))
			}))
			defer server.Close()
			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
			require.NoError(t, err)
			e := &types.IndexerMessageEnvelope{
				Action: constants.ActionUpdated, Data: map[string]any{"uid": "fixture-resource"},
				IndexingConfig: &types.IndexingConfig{ObjectID: "fixture-resource", AccessCheckObject: "fixture_resource:fixture-resource", AccessCheckRelation: "viewer", HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor"},
				Audit:          &types.AuditEvent{EventID: "fixture-event-1", Publisher: "test-publisher", Origin: types.AuditOrigin(tt.origin), ResourceRevision: 42, OccurredAt: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)},
			}
			ctx := context.Background()
			if tt.waitFor {
				ctx = logging.WithRefreshWaitFor(ctx)
			}
			body, applied, err := NewAuditProjectionRepository(client, "resources").ApplyIfNewer(ctx, e, "fixture_resource", "fixture-fingerprint")
			require.NoError(t, err)
			require.True(t, applied)
			require.Equal(t, "fixture_resource:fixture-resource", body.ObjectRef)
		})
	}
}

// TestAuditProjectionRepository_ReservedCharacterIDs preserves opaque references
// while escaping only the request's document-ID path segment.
func TestAuditProjectionRepository_ReservedCharacterIDs(t *testing.T) {
	for _, id := range []string{"resource?one", "resource?two", "resource/child", "resource%child", "resource%2Fchild", "resource+and&equals="} {
		t.Run(id, func(t *testing.T) {
			objectRef := "fixture_resource:" + id
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/resources/_update/"+objectRef, r.URL.Path)
				require.Equal(t, "/resources/_update/"+url.PathEscape(objectRef), r.URL.EscapedPath())
				require.Len(t, r.URL.Query(), 3)
				require.Equal(t, "8", r.URL.Query().Get("retry_on_conflict"))
				var request struct {
					Script struct {
						Params struct {
							Document struct {
								ObjectRef string `json:"object_ref"`
								ObjectID  string `json:"object_id"`
							} `json:"document"`
						} `json:"params"`
					} `json:"script"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.Equal(t, objectRef, request.Script.Params.Document.ObjectRef)
				require.Equal(t, id, request.Script.Params.Document.ObjectID)
				_, _ = w.Write([]byte(`{"result":"created","get":{"_source":{"audit_event_key":"test-publisher:reserved-id","audit_event_fingerprint":"fixture-fingerprint"}}}`))
			}))
			defer server.Close()
			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
			require.NoError(t, err)
			e := &types.IndexerMessageEnvelope{
				Action: constants.ActionCreated, Data: map[string]any{"uid": id},
				IndexingConfig: &types.IndexingConfig{ObjectID: id},
				Audit:          &types.AuditEvent{EventID: "reserved-id", Publisher: "test-publisher", Origin: types.AuditOriginMutation, ResourceRevision: 1},
			}
			body, applied, err := NewAuditProjectionRepository(client, "resources").ApplyIfNewer(context.Background(), e, "fixture_resource", "fixture-fingerprint")
			require.NoError(t, err)
			require.True(t, applied)
			require.Equal(t, objectRef, body.ObjectRef)
		})
	}
}

// TestAuditProjectionRepository_MetadataOnlyMutation distinguishes a preserved
// projection from a body replacement using the atomically returned identity.
func TestAuditProjectionRepository_MetadataOnlyMutation(t *testing.T) {
	for _, tt := range []struct {
		name, result string
		wantError    bool
	}{
		{"projection preserved", `{"result":"updated","get":{"_source":{"audit_event_key":"test-publisher:projection","audit_event_fingerprint":"projection-fingerprint","audit_mutation_key":"test-publisher:mutation","audit_mutation_fingerprint":"mutation-fingerprint"}}}`, false},
		{"lost ACK replay", `{"result":"noop"}`, false},
		{"missing source fails closed", `{"result":"updated"}`, true},
		{"missing mutation proof fails closed", `{"result":"updated","get":{"_source":{"audit_event_key":"test-publisher:projection","audit_event_fingerprint":"projection-fingerprint"}}}`, true},
		{"mismatched fingerprint fails closed", `{"result":"updated","get":{"_source":{"audit_event_key":"test-publisher:mutation","audit_event_fingerprint":"other-fingerprint"}}}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.result))
			}))
			defer server.Close()
			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
			require.NoError(t, err)
			e := &types.IndexerMessageEnvelope{
				Action: constants.ActionUpdated, Data: map[string]any{"uid": "fixture-resource"},
				IndexingConfig: &types.IndexingConfig{ObjectID: "fixture-resource"},
				Audit:          &types.AuditEvent{EventID: "mutation", Publisher: "test-publisher", Origin: types.AuditOriginMutation, ResourceRevision: 42},
			}
			body, applied, err := NewAuditProjectionRepository(client, "resources").ApplyIfNewer(context.Background(), e, "fixture_resource", "mutation-fingerprint")
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.False(t, applied)
			require.Nil(t, body)
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func TestAuditProjectionRepository_RejectsUnsafeFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"reason":"Bearer should-not-be-reflected"}}`))
	}))
	defer server.Close()
	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	e := &types.IndexerMessageEnvelope{Action: constants.ActionDeleted, Data: "fixture-resource", IndexingConfig: &types.IndexingConfig{ObjectID: "fixture-resource"}, Audit: &types.AuditEvent{Publisher: "test-publisher", EventID: "fixture-event", Origin: types.AuditOriginMutation}}
	_, _, err = NewAuditProjectionRepository(client, "resources").ApplyIfNewer(context.Background(), e, "fixture_resource", "fingerprint")
	require.ErrorContains(t, err, "status 400")
	require.NotContains(t, err.Error(), "Bearer")
}
