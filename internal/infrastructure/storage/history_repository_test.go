// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// TestHistoryRepository_CreateOrVerify requires immutable read-back evidence
// for duplicate appends rather than treating every HTTP 409 as success.
func TestHistoryRepository_CreateOrVerify(t *testing.T) {
	for _, origin := range []types.AuditOrigin{types.AuditOriginMutation, types.AuditOriginHistoricalImport} {
		t.Run(string(origin), func(t *testing.T) { testHistoryCreateOrVerify(t, origin) })
	}
}

func testHistoryCreateOrVerify(t *testing.T, origin types.AuditOrigin) {
	t.Helper()
	original := &contracts.HistoryRecord{
		Publisher: "test-publisher", EventID: "fixture-event-1", ObjectType: "fixture_resource",
		ObjectID: "fixture-resource", Fingerprint: strings.Repeat("a", 64),
		IngestedAt: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		Audit:      contracts.HistoryAudit{Origin: origin},
	}
	for _, tt := range []struct {
		name       string
		existing   bool
		conflict   bool
		getMissing bool
		wantError  string
	}{
		{name: "create"},
		{name: "redelivery", existing: true},
		{name: "lost acknowledgment", existing: true},
		{name: "different immutable content", existing: true, conflict: true, wantError: "different content"},
		{name: "conflict but missing record", existing: true, getMissing: true, wantError: "status 404"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var writes, reads int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/resource-history-v1/_doc/test-publisher:fixture-event-1", r.URL.Path)
				switch r.Method {
				case http.MethodPut:
					writes++
					require.Equal(t, "create", r.URL.Query().Get("op_type"))
					if tt.existing {
						w.WriteHeader(http.StatusConflict)
					} else {
						w.WriteHeader(http.StatusCreated)
					}
				case http.MethodGet:
					reads++
					includes := r.URL.Query().Get("_source_includes")
					require.Contains(t, includes, "fingerprint")
					require.Contains(t, includes, "erased")
					require.NotContains(t, includes, "audit")
					if tt.getMissing {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					stored := *original
					if tt.conflict {
						stored.Fingerprint = strings.Repeat("b", 64)
					}
					stored.IngestedAt = original.IngestedAt.Add(-time.Hour)
					_ = json.NewEncoder(w).Encode(map[string]any{"_source": stored})
				default:
					t.Errorf("unexpected HTTP method %s", r.Method)
				}
			}))
			defer server.Close()
			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
			require.NoError(t, err)
			created, err := NewHistoryRepository(client).CreateOrVerify(context.Background(), original)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, !tt.existing, created)
			require.Equal(t, 1, writes)
			if tt.existing {
				require.Equal(t, 1, reads)
			} else {
				require.Zero(t, reads)
			}
		})
	}
}

func TestHistoryRepository_RejectProjection(t *testing.T) {
	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{"http://127.0.0.1:1"}})
	require.NoError(t, err)
	record := &contracts.HistoryRecord{Publisher: "fixture", EventID: "event", Fingerprint: strings.Repeat("a", 64), Audit: contracts.HistoryAudit{Origin: types.AuditOriginReindex}}
	_, err = NewHistoryRepository(client).CreateOrVerify(context.Background(), record)
	require.ErrorContains(t, err, "projection-only")
}

func TestHistoryRepository_VerifyExisting(t *testing.T) {
	record := &contracts.HistoryRecord{
		Publisher: "test-publisher", EventID: "fixture-event-1", ObjectType: "fixture_resource",
		ObjectID: "fixture-resource", Fingerprint: strings.Repeat("a", 64),
	}
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		exists    bool
		wantError string
	}{
		{name: "absent", status: http.StatusNotFound, body: `{"_index":"resource-history-v1","_id":"test-publisher:fixture-event-1","found":false}`},
		{name: "404 storage error", status: http.StatusNotFound, body: `{"error":{"type":"index_not_found_exception","reason":"private-response"},"status":404}`, wantError: "invalid not-found response"},
		{name: "404 empty body", status: http.StatusNotFound, wantError: "invalid not-found response"},
		{name: "404 malformed", status: http.StatusNotFound, body: `{`, wantError: "invalid not-found response"},
		{name: "404 missing found", status: http.StatusNotFound, body: `{}`, wantError: "invalid not-found response"},
		{name: "404 null found", status: http.StatusNotFound, body: `{"found":null}`, wantError: "invalid not-found response"},
		{name: "404 wrong found type", status: http.StatusNotFound, body: `{"found":"false"}`, wantError: "invalid not-found response"},
		{name: "404 claims found", status: http.StatusNotFound, body: `{"found":true}`, wantError: "invalid not-found response"},
		{name: "404 duplicate found", status: http.StatusNotFound, body: `{"found":true,"found":false}`, wantError: "invalid not-found response"},
		{name: "404 error with found false", status: http.StatusNotFound, body: `{"found":false,"error":null}`, wantError: "invalid not-found response"},
		{name: "404 source with found false", status: http.StatusNotFound, body: `{"found":false,"_source":{}}`, wantError: "invalid not-found response"},
		{name: "404 trailing JSON", status: http.StatusNotFound, body: `{"found":false}{}`, wantError: "invalid not-found response"},
		{name: "404 oversized", status: http.StatusNotFound, body: `{"found":false,"extra":"` + strings.Repeat("x", 8192) + `"}`, wantError: "invalid not-found response"},
		{name: "retained", status: http.StatusOK, exists: true},
		{name: "erased", status: http.StatusOK, body: `{"_source":{"erased":true}}`, wantError: "explicitly erased"},
		{name: "mismatch", status: http.StatusOK, body: `{"_source":{"fingerprint":"different"}}`, wantError: "different content"},
		{name: "malformed", status: http.StatusOK, body: `{`, wantError: "decode existing"},
		{name: "unavailable", status: http.StatusServiceUnavailable, body: "private-response", wantError: "status 503"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/resource-history-v1/_doc/test-publisher:fixture-event-1", r.URL.Path)
				require.NotContains(t, r.URL.Query().Get("_source_includes"), "audit")
				w.WriteHeader(tt.status)
				if tt.body != "" {
					_, _ = w.Write([]byte(tt.body))
				} else if tt.exists {
					_ = json.NewEncoder(w).Encode(map[string]any{"_source": record})
				}
			}))
			defer server.Close()
			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}, DisableRetry: true})
			require.NoError(t, err)
			exists, err := NewHistoryRepository(client).VerifyExisting(context.Background(), record)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.NotContains(t, err.Error(), "private-response")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.exists, exists)
		})
	}
}

// TestHistoryRepository_ErasureFencesReplay verifies targeted replacement removes
// the private audit payload and permanently blocks recreation of that event ID.
func TestHistoryRepository_ErasureFencesReplay(t *testing.T) {
	var stored map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/resource-history-v1/_doc/test-publisher:fixture-event-1", r.URL.Path)
		switch r.Method {
		case http.MethodPut:
			if r.URL.Query().Get("op_type") == "create" && stored != nil {
				w.WriteHeader(http.StatusConflict)
				return
			}
			stored = nil
			require.NoError(t, json.NewDecoder(r.Body).Decode(&stored))
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			if stored == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"_source": stored}))
		default:
			t.Errorf("unexpected HTTP method %s", r.Method)
		}
	}))
	defer server.Close()
	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	repo := NewHistoryRepository(client)
	record := &contracts.HistoryRecord{Publisher: "test-publisher", EventID: "fixture-event-1", Fingerprint: strings.Repeat("a", 64), Audit: contracts.HistoryAudit{Origin: types.AuditOriginMutation}}
	_, err = repo.CreateOrVerify(context.Background(), record)
	require.NoError(t, err)
	err = repo.Erase(context.Background(), record.Publisher, record.EventID, time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, true, stored["erased"])
	require.NotContains(t, stored, "audit")
	require.NotContains(t, stored, "fingerprint")
	_, err = repo.CreateOrVerify(context.Background(), record)
	require.ErrorContains(t, err, "explicitly erased")
}
