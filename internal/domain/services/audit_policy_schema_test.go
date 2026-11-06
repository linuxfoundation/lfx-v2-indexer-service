// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testPolicyManifest = `{
  "publishers": [{
    "publisher": "test-publisher", "principal": "fixture@clients", "can_attest_actors": true,
    "resources": {"fixture_resource": {
      "origins": ["mutation", "reindex"],
      "schemas": {"fixture.audit.v1": {"fields": {
        "name": {"type":"string","required":true},
        "count": {"type":"integer"},
        "details": {"type":"object","fields":{"flag":{"type":"boolean","nullable":true}}},
        "labels": {"type":"array","items":{"type":"string"}}
      }}}
    }}
  }]
}`

// TestDecodeAuditPolicies_ClosedSnapshotSchemas checks exact recursive field sets,
// required values, scalar types and explicit nullability on admitted snapshots.
func TestDecodeAuditPolicies_ClosedSnapshotSchemas(t *testing.T) {
	policies, err := DecodeAuditPolicies([]byte(testPolicyManifest))
	require.NoError(t, err)
	a, err := NewAuditAdmission(newTestAuditAuth("fixture@clients"), policies)
	require.NoError(t, err)
	for _, tt := range []struct {
		name, snapshot string
		valid          bool
	}{
		{"approved", `{"name":"Test Resource","count":9007199254740993,"details":{"flag":null},"labels":["one","two"]}`, true},
		{"unknown field", `{"name":"Test Resource","extra":"value"}`, false},
		{"nested unknown field", `{"name":"Test Resource","details":{"secret":"value"}}`, false},
		{"wrong type", `{"name":"Test Resource","count":1.5}`, false},
		{"quoted number", `{"name":"Test Resource","count":"1"}`, false},
		{"missing required", `{"count":1}`, false},
		{"unexpected null", `{"name":null}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := auditAdmissionFixture()
			e.Audit.Content.After = json.RawMessage(tt.snapshot)
			payload, err := json.Marshal(e)
			require.NoError(t, err)
			_, err = a.Admit(context.Background(), payload, "fixture_resource")
			if tt.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestDecodeAuditPolicies_SnapshotDepthBoundary keeps startup and runtime depth
// accounting aligned for object, array and mixed-container schemas.
func TestDecodeAuditPolicies_SnapshotDepthBoundary(t *testing.T) {
	for _, shape := range []string{"objects", "arrays", "mixed"} {
		// A root field is at depth one. Fifteen containers put the scalar
		// leaf at depth sixteen; sixteen containers must fail policy loading.
		for _, containers := range []int{0, 14, 15, 16, 17} {
			t.Run(fmt.Sprintf("%s/%d-containers", shape, containers), func(t *testing.T) {
				rule := AuditFieldRule{Type: "string", Required: true}
				snapshot := `"fixture-value"`
				for level := range containers {
					if shape == "objects" || (shape == "mixed" && level%2 == 0) {
						rule = AuditFieldRule{Type: "object", Required: true, Fields: map[string]AuditFieldRule{"value": rule}}
						snapshot = `{"value":` + snapshot + `}`
					} else {
						item := rule
						rule = AuditFieldRule{Type: "array", Required: true, Items: &item}
						snapshot = `[` + snapshot + `]`
					}
				}
				fields := map[string]AuditFieldRule{"value": rule}
				payload := json.RawMessage(`{"value":` + snapshot + `}`)
				var manifest AuditPolicyManifest
				require.NoError(t, json.Unmarshal([]byte(testPolicyManifest), &manifest))
				manifest.Publishers[0].Resources["fixture_resource"].Schemas["fixture.audit.v1"] = AuditSnapshotRule{Fields: fields}
				encoded, err := json.Marshal(manifest)
				require.NoError(t, err)
				policies, policyErr := DecodeAuditPolicies(encoded)
				snapshotErr := validateAuditObject(payload, fields, 0)
				if containers >= 16 {
					require.ErrorContains(t, policyErr, "schema depth")
					require.ErrorContains(t, snapshotErr, "schema depth")
				} else {
					require.NoError(t, policyErr)
					require.NoError(t, snapshotErr)
					validate := policies[0].Resources["fixture_resource"].Schemas["fixture.audit.v1"]
					require.NoError(t, validate(payload, payload))
				}
			})
		}
	}
}

// TestDecodeAuditPolicies_FailClosed rejects empty, unknown, prohibited and
// trailing manifest content rather than installing a partial registry.
func TestDecodeAuditPolicies_FailClosed(t *testing.T) {
	for _, text := range []string{
		`{"publishers":[]}`,
		`{"publishers":[],"unknown":true}`,
		`{"publishers":[{"publisher":"test-publisher","principal":"service:fixture","can_attest_actors":true,"resources":{"fixture_resource":{"origins":["mutation"],"schemas":{}}}}]}`,
		`{"publishers":[{"publisher":"test-publisher","principal":"service:fixture","can_attest_actors":true,"resources":{"fixture_resource":{"origins":["mutation"],"schemas":{"fixture.audit.v1":{"fields":{"api_token":{"type":"string"}}}}}}}]}`,
	} {
		_, err := DecodeAuditPolicies([]byte(text))
		require.Error(t, err)
	}
	_, err := DecodeAuditPolicies([]byte(testPolicyManifest + ` {}`))
	require.Error(t, err)
	_, err = DecodeAuditPolicies(nil)
	require.Error(t, err)
}

// TestDecodeAuditPolicies_UsesWireSchemaNameGrammar prevents policy/wire drift
// for valid versioned schema tokens and malformed names.
func TestDecodeAuditPolicies_UsesWireSchemaNameGrammar(t *testing.T) {
	for _, name := range []string{"committee-audit.v1", "2Committee.V1"} {
		policy := strings.Replace(testPolicyManifest, "fixture.audit.v1", name, 1)
		policies, err := DecodeAuditPolicies([]byte(policy))
		require.NoError(t, err)
		require.Contains(t, policies[0].Resources["fixture_resource"].Schemas, name)
	}
	invalid := strings.Replace(testPolicyManifest, "fixture.audit.v1", "not valid", 1)
	_, err := DecodeAuditPolicies([]byte(invalid))
	require.ErrorContains(t, err, "invalid audit snapshot schema")
}
