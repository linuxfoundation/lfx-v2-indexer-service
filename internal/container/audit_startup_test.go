// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package container

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

const auditFixturePolicy = `{"publishers":[{"publisher":"test-publisher","principal":"fixture@clients","can_attest_actors":true,"resources":{"fixture_resource":{"origins":["mutation"],"schemas":{"fixture.audit.v1":{"fields":{"name":{"type":"string","required":true}}}}}}}]}`

// auditMappingFixtures shares the provisioned private/current schemas across
// startup and trace-export tests without manufacturing a second mapping contract.
func auditMappingFixtures(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	mappings := make(map[string]json.RawMessage)
	for index, file := range map[string]string{
		"resource-history-v1":          "opensearch-resource-history-v1.json",
		"resource-audit-deliveries-v1": "opensearch-audit-deliveries-v1.json",
		"resources":                    "opensearch-audit-projection-mapping.json",
	} {
		data, err := os.ReadFile("../../charts/lfx-v2-indexer-service/files/" + file)
		require.NoError(t, err)
		if index == "resources" {
			data = append(append([]byte(`{"mappings":`), data...), '}')
		}
		mappings[index] = data
	}
	return mappings
}

// TestAuditStartup_FailClosed checks mapping/file/policy failures are returned
// before consumption, and a valid startup installs both platform legacy fences.
func TestAuditStartup_FailClosed(t *testing.T) {
	for _, tt := range []struct{ name, wantError string }{
		{"valid", ""}, {"mapping mismatch", "audited index mappings not ready"},
		{"missing policy", "unavailable or too large"}, {"oversized policy", "unavailable or too large"},
		{"policy is directory", "read audited publisher policy: failed"},
		{"invalid manifest", "invalid audited publisher policy"}, {"coverage mismatch", "must be audit-required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mappings := auditMappingFixtures(t)
			if tt.name == "mapping mismatch" {
				mappings["resources"] = json.RawMessage(`{"mappings":{"properties":{}}}`)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/_mapping")
				if !strings.HasSuffix(r.URL.Path, "/_mapping") || mappings[index] == nil {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]json.RawMessage{index: mappings[index]})
			}))
			defer server.Close()
			policyFile := filepath.Join(t.TempDir(), "policy.json")
			data := []byte(auditFixturePolicy)
			switch tt.name {
			case "oversized policy":
				data = bytes.Repeat([]byte(" "), types.MaxAuditEnvelopeBytes+1)
			case "invalid manifest":
				data = []byte(`{"publishers":`)
			}
			if tt.name == "policy is directory" {
				policyFile = t.TempDir()
			} else if tt.name != "missing policy" {
				require.NoError(t, os.WriteFile(policyFile, data, 0600))
			}
			c := &Container{Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Config: &config.AppConfig{
				OpenSearch: config.OpenSearchConfig{URL: server.URL, Index: "resources", Username: "fixture", Password: "fixture", Timeout: time.Second},
				Audit:      config.AuditConfig{Enabled: true, PolicyFile: policyFile, RequiredTypes: []string{"fixture_resource"}},
				JWT:        config.JWTConfig{Issuer: "https://issuer.invalid/", Audiences: []string{"fixture"}, JWKSURL: server.URL + "/jwks"},
			}}
			if tt.name == "coverage mismatch" {
				c.Config.Audit.RequiredTypes = []string{"other_resource"}
			}
			require.NoError(t, c.initializeOpenSearchClients())
			require.NoError(t, c.initializeRepositories())
			auth := mocks.NewMockAuthRepository()
			auth.ValidateTokenResponse = &contracts.Principal{Principal: "fixture@clients"}
			c.AuthRepository, c.MessagingRepository = auth, mocks.NewMockMessagingRepository()
			err := c.initializeApplication()
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.NotContains(t, err.Error(), policyFile)
				return
			}
			require.NoError(t, err)
			legacy := []byte(`{"action":"updated","data":{"uid":"fixture-resource"}}`)
			require.ErrorContains(t, c.MessageProcessor.ProcessIndexingMessage(context.Background(), legacy, constants.IndexPrefix+"fixture_resource"), "requires schema_version 2")
			require.ErrorContains(t, c.MessageProcessor.ProcessV1IndexingMessage(context.Background(), legacy, constants.FromV1Prefix+"fixture_resource"), "rejects platform V1")
		})
	}
}
