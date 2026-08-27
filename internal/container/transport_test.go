// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package container

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	opensearchgo "github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// TestOpenSearchTransport_ResponseHeaderTimeout verifies that the configured
// ResponseHeaderTimeout fires when the server never sends response headers,
// preventing indefinite hangs on stale connections.
func TestOpenSearchTransport_ResponseHeaderTimeout(t *testing.T) {
	// Server that accepts the connection but never writes response headers.
	hangServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hangServer.Close()

	timeout := 150 * time.Millisecond
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = timeout

	client, err := opensearchgo.NewClient(opensearchgo.Config{
		Addresses: []string{hangServer.URL},
		Transport: otelhttp.NewTransport(transport),
	})
	require.NoError(t, err)

	start := time.Now()
	_, err = client.Info()
	elapsed := time.Since(start)

	assert.Error(t, err, "expected timeout error from hanging server")
	assert.Less(t, elapsed, 2*timeout, "request should have timed out within 2× the configured timeout")
}
