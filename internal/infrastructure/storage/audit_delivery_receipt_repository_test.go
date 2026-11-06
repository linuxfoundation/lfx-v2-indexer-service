// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// TestAuditDeliveryReceiptRepository_CreateOrVerify accepts identical retries but
// rejects publisher/event identity reuse across content or origins.
func TestAuditDeliveryReceiptRepository_CreateOrVerify(t *testing.T) {
	var stored contracts.AuditDeliveryReceipt
	var present bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/resource-audit-deliveries-v1/_doc/test-publisher:projection-1", r.URL.Path)
		switch r.Method {
		case http.MethodPut:
			require.Equal(t, "create", r.URL.Query().Get("op_type"))
			if present {
				w.WriteHeader(http.StatusConflict)
				return
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&stored))
			present = true
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			require.NotContains(t, r.URL.Query().Get("_source_includes"), "audit")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"_source": stored}))
		default:
			t.Errorf("unexpected HTTP method")
		}
	}))
	defer server.Close()
	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	repository := NewAuditDeliveryReceiptRepository(client, time.Second)
	receipt := contracts.AuditDeliveryReceipt{Publisher: "test-publisher", EventID: "projection-1", Origin: types.AuditOriginReindex,
		Fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	require.NoError(t, repository.CreateOrVerify(context.Background(), receipt))
	require.NoError(t, repository.CreateOrVerify(context.Background(), receipt))
	receipt.Fingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	require.ErrorContains(t, repository.CreateOrVerify(context.Background(), receipt), "identity reused")
	receipt.Fingerprint = stored.Fingerprint
	receipt.Origin = types.AuditOriginMutation
	require.ErrorContains(t, repository.CreateOrVerify(context.Background(), receipt), "identity reused")
}
