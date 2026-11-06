// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func auditResponseWriters() map[string]func(context.Context, *opensearch.Client, time.Duration) error {
	record := &contracts.HistoryRecord{Publisher: "test-publisher", EventID: "fixture-event", Fingerprint: strings.Repeat("a", 64), Audit: types.AuditEvent{Origin: types.AuditOriginMutation}}
	receipt := contracts.AuditDeliveryReceipt{Publisher: record.Publisher, EventID: record.EventID, Fingerprint: record.Fingerprint, Origin: types.AuditOriginMutation}
	return map[string]func(context.Context, *opensearch.Client, time.Duration) error{
		"history": func(ctx context.Context, client *opensearch.Client, timeout time.Duration) error {
			_, err := NewHistoryRepositoryWithTimeout(client, timeout).CreateOrVerify(ctx, record)
			return err
		},
		"receipt": func(ctx context.Context, client *opensearch.Client, timeout time.Duration) error {
			return NewAuditDeliveryReceiptRepository(client, timeout).CreateOrVerify(ctx, receipt)
		},
		"erasure": func(ctx context.Context, client *opensearch.Client, timeout time.Duration) error {
			return NewHistoryRepositoryWithTimeout(client, timeout).Erase(ctx, record.Publisher, record.EventID, time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
		},
	}
}

// TestAuditWriteResponses_ReuseHTTPConnection verifies nonempty write responses
// release their single HTTP/1.1 connection before returning or verifying a conflict.
func TestAuditWriteResponses_ReuseHTTPConnection(t *testing.T) {
	for name, write := range auditResponseWriters() {
		for _, status := range []int{http.StatusCreated, http.StatusConflict, http.StatusBadRequest} {
			t.Run(name+"/"+http.StatusText(status), func(t *testing.T) {
				var connections atomic.Int32
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if r.Method == http.MethodGet {
						_ = json.NewEncoder(w).Encode(map[string]any{"_source": map[string]any{
							"publisher": "test-publisher", "event_id": "fixture-event", "fingerprint": strings.Repeat("a", 64), "origin": types.AuditOriginMutation,
						}})
						return
					}
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"private-marker":"`+strings.Repeat("x", 64<<10)+`"}`)
				}))
				server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
					if state == http.StateNew {
						connections.Add(1)
					}
				}
				server.Start()
				defer server.Close()
				transport := http.DefaultTransport.(*http.Transport).Clone()
				transport.MaxConnsPerHost = 1
				defer transport.CloseIdleConnections()
				client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}, Transport: transport, DisableRetry: true})
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				for range 2 {
					err := write(ctx, client, time.Second)
					if status == http.StatusBadRequest || (status == http.StatusConflict && name == "erasure") {
						require.ErrorContains(t, err, "OpenSearch status")
						require.NotContains(t, err.Error(), "private-marker")
					} else {
						require.NoError(t, err)
					}
				}
				require.Equal(t, int32(1), connections.Load(), "write and conflict-verification responses must permit reuse")
			})
		}
	}
}

// TestAuditWriteResponses_DrainUsesRequestTimeout bounds stalled response drains
// with the existing request deadline and checks failures do not expose payloads.
func TestAuditWriteResponses_DrainUsesRequestTimeout(t *testing.T) {
	for name, write := range auditResponseWriters() {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"private-marker":"`)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}, DisableRetry: true})
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = write(ctx, client, 50*time.Millisecond)
			require.ErrorContains(t, err, "read audit write response: failed")
			require.NotContains(t, err.Error(), "private-marker")
		})
	}
}
