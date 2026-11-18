// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAuditConfig_RequiredTypeGrammar rejects required types that no valid wire
// envelope can target, while accepting the exact 64-character boundary.
func TestAuditConfig_RequiredTypeGrammar(t *testing.T) {
	for _, name := range []string{"fixture_resource", "a" + strings.Repeat("b", 63), "index", "a" + strings.Repeat("b", 64)} {
		t.Run(name, func(t *testing.T) {
			cfg := baseValidConfig()
			cfg.Audit.Enabled, cfg.Audit.PolicyFile, cfg.Audit.RequiredTypes = true, "/fixtures/policy.json", []string{name}
			cfg.OpenSearch.Username, cfg.OpenSearch.Password = "fixture", "fixture"
			err := cfg.validateAudit()
			if name == "index" || len(name) > 64 {
				require.ErrorContains(t, err, "invalid AUDIT_REQUIRED_TYPES")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestAuditConfig_FailClosed requires explicit publisher policy and private
// credentials before audited ingestion can be enabled.
func TestAuditConfig_FailClosed(t *testing.T) {
	for _, tt := range []struct {
		name      string
		env       map[string]string
		wantError string
		wantTypes int
	}{
		{name: "disabled by default"},
		{name: "missing policy", env: map[string]string{"AUDIT_ENABLED": "true", "AUDIT_REQUIRED_TYPES": "fixture_resource", "OPENSEARCH_USERNAME": "fixture", "OPENSEARCH_PASSWORD": "fixture"}, wantError: "AUDIT_POLICY_FILE", wantTypes: 1},
		{name: "missing credentials", env: map[string]string{"AUDIT_ENABLED": "true", "AUDIT_REQUIRED_TYPES": "fixture_resource", "AUDIT_POLICY_FILE": "/fixtures/policy.json"}, wantError: "authenticated OpenSearch credentials", wantTypes: 1},
		{name: "valid", env: map[string]string{"AUDIT_ENABLED": "true", "AUDIT_REQUIRED_TYPES": "fixture_resource", "AUDIT_POLICY_FILE": "/fixtures/policy.json", "OPENSEARCH_USERNAME": "fixture", "OPENSEARCH_PASSWORD": "fixture"}, wantTypes: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			cfg, err := LoadConfig()
			require.NoError(t, err)
			err = cfg.validateAudit()
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, cfg.Audit.RequiredTypes, tt.wantTypes)
		})
	}
}

func TestAuditConfig_JetStreamTimeoutBudget(t *testing.T) {
	t.Setenv("AUDIT_ENABLED", "true")
	t.Setenv("AUDIT_REQUIRED_TYPES", "fixture_resource")
	t.Setenv("AUDIT_POLICY_FILE", "/fixtures/policy.json")
	t.Setenv("OPENSEARCH_USERNAME", "fixture")
	t.Setenv("OPENSEARCH_PASSWORD", "fixture")
	t.Setenv("NATS_ACK_WAIT", "0s")
	t.Setenv("NATS_DRAIN_TIMEOUT", "160s")
	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, auditMaxOpenSearchRequests*cfg.OpenSearch.Timeout+ackWaitSafetyMargin, cfg.NATS.AckWait)
	require.NoError(t, cfg.validateNATS())
	cfg.NATS.AckWait = 45 * time.Second
	require.ErrorContains(t, cfg.validateNATS(), "NATS ack wait")
}

// TestAuditConfig_DrainCoversLegacyAndAuditedWork checks both timeout bounds,
// including configurations where legacy batching is the slower path.
func TestAuditConfig_DrainCoversLegacyAndAuditedWork(t *testing.T) {
	for _, tt := range []struct {
		name               string
		audit              bool
		batchWait, minimum time.Duration
	}{
		{"legacy", false, 180 * time.Second, 215 * time.Second},
		{"audited bound dominates", true, time.Second, 155 * time.Second},
		{"legacy bound dominates with audit enabled", true, 180 * time.Second, 215 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseValidConfig()
			cfg.Audit.Enabled = tt.audit
			cfg.OpenSearch.BatchMaxWait = tt.batchWait
			cfg.NATS.AckWait = cfg.minimumAckWait()
			cfg.NATS.DrainTimeout = tt.minimum - time.Nanosecond
			require.ErrorContains(t, cfg.validateNATS(), "NATS drain timeout")
			cfg.NATS.DrainTimeout = tt.minimum
			require.NoError(t, cfg.validateNATS())
			cfg.NATS.DrainTimeout = tt.minimum + time.Second
			require.NoError(t, cfg.validateNATS())
		})
	}
}
