// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package container

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	opensearchgo "github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
)

// TestOpenSearchTransport_ResponseHeaderTimeout verifies that newOpenSearchTransport
// (the same function used by initializeInfrastructure) fires its ResponseHeaderTimeout
// — covering time waiting for the first response byte; body reads are not bounded.
func TestOpenSearchTransport_ResponseHeaderTimeout(t *testing.T) {
	// Server that accepts the connection but never writes response headers.
	hangServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hangServer.Close()

	timeout := 150 * time.Millisecond
	client, err := opensearchgo.NewClient(opensearchgo.Config{
		Addresses: []string{hangServer.URL},
		Transport: newOpenSearchTransport(timeout),
	})
	require.NoError(t, err)

	start := time.Now()
	_, err = client.Info()
	elapsed := time.Since(start)

	assert.Error(t, err, "expected timeout error from hanging server")
	assert.GreaterOrEqual(t, elapsed, timeout/2, "request should not have failed before the timeout fired")
	assert.Less(t, elapsed, 2*timeout, "request should have timed out within 2x the configured timeout")
	var netErr net.Error
	assert.True(t, errors.As(err, &netErr) && netErr.Timeout(), "expected a net.Error timeout, got: %v", err)
}

// TestAuditOpenSearchClient_ResponseHeaderTimeout checks the private client keeps
// the same first-byte timeout even though it deliberately omits HTTP telemetry.
func TestAuditOpenSearchClient_ResponseHeaderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	const timeout = 100 * time.Millisecond
	c := &Container{Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Config: &config.AppConfig{
		OpenSearch: config.OpenSearchConfig{URL: server.URL, Timeout: timeout}, Audit: config.AuditConfig{Enabled: true},
	}}
	require.NoError(t, c.initializeOpenSearchClients())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.AuditOpenSearchClient.Info(c.AuditOpenSearchClient.Info.WithContext(ctx))
	require.Error(t, err)
	var timeoutError net.Error
	require.ErrorAs(t, err, &timeoutError)
	require.True(t, timeoutError.Timeout())
	require.Less(t, time.Since(start), time.Second)
}

// TestAuditOpenSearchClient_DefaultOff avoids allocating the private client for
// legacy-only deployments and refuses an audit-enabled repository fallback.
func TestAuditOpenSearchClient_DefaultOff(t *testing.T) {
	c := &Container{Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Config: &config.AppConfig{
		OpenSearch: config.OpenSearchConfig{URL: "http://opensearch.invalid:9200", Timeout: time.Second},
	}}
	require.NoError(t, c.initializeOpenSearchClients())
	require.NotNil(t, c.OpenSearchClient)
	require.Nil(t, c.AuditOpenSearchClient)
	c.Config.Audit.Enabled = true
	require.ErrorContains(t, c.initializeRepositories(), "audit OpenSearch client is not initialized")
}
