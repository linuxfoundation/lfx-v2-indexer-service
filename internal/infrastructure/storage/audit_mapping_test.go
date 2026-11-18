// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/require"
)

// TestVerifyAuditMappings checks private storage/source requirements and rejects
// current-resource targets whose revision guards span multiple backing indices.
func TestVerifyAuditMappings(t *testing.T) {
	history, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/opensearch-resource-history-v1.json")
	require.NoError(t, err)
	projection, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/opensearch-audit-projection-mapping.json")
	require.NoError(t, err)
	deliveries, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/opensearch-audit-deliveries-v1.json")
	require.NoError(t, err)
	yes, no := true, false
	for _, tt := range []struct {
		name                                                  string
		badHistory, badProjection                             bool
		badHistorySource, badDeliverySource, badCurrentSource bool
		currentAlias, multipleCurrentIndices                  bool
		revisionDocValues                                     *bool
		wantError                                             string
	}{
		{name: "valid"},
		{name: "revision doc values explicitly enabled", revisionDocValues: &yes},
		{name: "revision doc values disabled", revisionDocValues: &no, wantError: "doc values must be enabled"},
		{name: "single backing index alias", currentAlias: true},
		{name: "multiple current backing indices", multipleCurrentIndices: true, wantError: "current-resource index must resolve to one backing index"},
		{name: "history is not private", badHistory: true, wantError: "not strict"},
		{name: "projection fields missing", badProjection: true, wantError: "missing or indexed"},
		{name: "history source disabled", badHistorySource: true, wantError: "not strict"},
		{name: "delivery source filtered", badDeliverySource: true, wantError: "not strict"},
		{name: "current source filtered", badCurrentSource: true, wantError: "source storage"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var response map[string]any
				switch r.URL.Path {
				case "/resource-audit-deliveries-v1/_mapping":
					var mapping any
					require.NoError(t, json.Unmarshal(deliveries, &mapping))
					if tt.badDeliverySource {
						mapping.(map[string]any)["mappings"].(map[string]any)["_source"] = map[string]any{"excludes": []string{"fingerprint"}}
					}
					response = map[string]any{"resource-audit-deliveries-v1": mapping}
				case "/resource-history-v1/_mapping":
					var mapping any
					require.NoError(t, json.Unmarshal(history, &mapping))
					if tt.badHistory {
						mapping.(map[string]any)["mappings"].(map[string]any)["dynamic"] = "true"
					}
					if tt.badHistorySource {
						mapping.(map[string]any)["mappings"].(map[string]any)["_source"] = map[string]any{"enabled": false}
					}
					response = map[string]any{"resource-history-v1": mapping}
				case "/resources/_mapping":
					var mapping any
					require.NoError(t, json.Unmarshal(projection, &mapping))
					if tt.badProjection {
						delete(mapping.(map[string]any)["properties"].(map[string]any), "audit_source_revision")
					}
					if tt.badCurrentSource {
						mapping.(map[string]any)["_source"] = map[string]any{"includes": []string{"object_ref"}}
					}
					if tt.revisionDocValues != nil {
						mapping.(map[string]any)["properties"].(map[string]any)["audit_source_revision"].(map[string]any)["doc_values"] = *tt.revisionDocValues
					}
					response = map[string]any{"resources": map[string]any{"mappings": mapping}}
					if tt.currentAlias || tt.multipleCurrentIndices {
						response = map[string]any{"resources-v1": map[string]any{"mappings": mapping}}
					}
					if tt.multipleCurrentIndices {
						response["resources-v2"] = map[string]any{"mappings": mapping}
					}
				default:
					t.Errorf("unexpected mapping path")
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
			require.NoError(t, err)
			err = VerifyAuditMappings(context.Background(), client, "resources", time.Second)
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
		})
	}
}

// TestVerifyAuditMappings_RequiredPrivateFields mutates the provisioned fixtures
// to check every required field, nested scope and private-field mapping flag.
func TestVerifyAuditMappings_RequiredPrivateFields(t *testing.T) {
	indices := map[string]string{
		historyIndex:               "opensearch-resource-history-v1.json",
		auditDeliveryReceiptsIndex: "opensearch-audit-deliveries-v1.json",
	}
	fixtures := make(map[string][]byte)
	for index, file := range indices {
		data, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/" + file)
		require.NoError(t, err)
		fixtures[index] = data
	}
	projection, err := os.ReadFile("../../../charts/lfx-v2-indexer-service/files/opensearch-audit-projection-mapping.json")
	require.NoError(t, err)
	fixtures["resources"] = append(append([]byte(`{"mappings":`), projection...), '}')

	type testCase struct {
		index, field, change string
	}
	var cases []testCase
	for index := range indices {
		var mapping struct {
			Mappings struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"mappings"`
		}
		require.NoError(t, json.Unmarshal(fixtures[index], &mapping))
		for field := range mapping.Mappings.Properties {
			cases = append(cases, testCase{index, field, "missing"}, testCase{index, field, "type"})
		}
		cases = append(cases, testCase{index, "fingerprint", "indexed"}, testCase{index, "fingerprint", "missing index"})
	}
	for _, field := range []string{"scope.before", "scope.after"} {
		cases = append(cases, testCase{historyIndex, field, "missing"}, testCase{historyIndex, field, "type"})
	}
	cases = append(cases, testCase{historyIndex, "scope", "dynamic"}, testCase{historyIndex, "scope", "disabled"}, testCase{historyIndex, "audit", "enabled"}, testCase{historyIndex, "audit", "missing enabled"})
	for _, tt := range cases {
		t.Run(tt.index+"/"+tt.field+"/"+tt.change, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/_mapping")
				var mapping map[string]any
				require.NoError(t, json.Unmarshal(fixtures[index], &mapping))
				if index == tt.index {
					fields := mapping["mappings"].(map[string]any)["properties"].(map[string]any)
					path := strings.Split(tt.field, ".")
					for _, part := range path[:len(path)-1] {
						fields = fields[part].(map[string]any)["properties"].(map[string]any)
					}
					name := path[len(path)-1]
					field := fields[name].(map[string]any)
					switch tt.change {
					case "missing":
						delete(fields, name)
					case "type":
						field["type"] = "text"
					case "indexed":
						field["index"] = true
					case "missing index":
						delete(field, "index")
					case "dynamic":
						field["dynamic"] = "true"
					case "enabled":
						field["enabled"] = true
					case "disabled":
						field["enabled"] = false
					case "missing enabled":
						delete(field, "enabled")
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{index: mapping})
			}))
			defer server.Close()
			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
			require.NoError(t, err)
			require.ErrorContains(t, VerifyAuditMappings(context.Background(), client, "resources", time.Second), "not strict and unindexed")
		})
	}
}
