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
	original := &contracts.HistoryRecord{
		Publisher: "test-publisher", EventID: "fixture-event-1", ObjectType: "fixture_resource",
		ObjectID: "fixture-resource", Fingerprint: strings.Repeat("a", 64),
		IngestedAt: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		Audit:      types.AuditEvent{Origin: types.AuditOriginMutation},
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
	record := &contracts.HistoryRecord{Publisher: "fixture", EventID: "event", Fingerprint: strings.Repeat("a", 64), Audit: types.AuditEvent{Origin: types.AuditOriginReindex}}
	_, err = NewHistoryRepository(client).CreateOrVerify(context.Background(), record)
	require.ErrorContains(t, err, "projection-only")
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
	record := &contracts.HistoryRecord{Publisher: "test-publisher", EventID: "fixture-event-1", Fingerprint: strings.Repeat("a", 64), Audit: types.AuditEvent{Origin: types.AuditOriginMutation}}
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
