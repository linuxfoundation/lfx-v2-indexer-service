// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package scripts

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// renderIndexerChart exercises real Helm rather than reproducing template math.
// CI installs a pinned binary; local contributors can provide HELM_BINARY.
func renderIndexerChart(t *testing.T, audited bool, overrides ...string) (string, error) {
	t.Helper()
	binary := os.Getenv("HELM_BINARY")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("helm")
		if err != nil {
			t.Skip("requires helm or HELM_BINARY")
		}
	}
	args := []string{"template", "fixture", "../charts/lfx-v2-indexer-service"}
	if audited {
		for _, value := range []string{
			"audit.enabled=true", "audit.requiredTypes[0]=fixture_resource", "audit.policyConfigMap=fixture-policy",
			"opensearch.auth.enabled=true", "opensearch.auth.existingSecret=fixture-runtime", "opensearch.auth.provisioningSecret=fixture-provisioner",
			"opensearch.history.indexingJob.enabled=true", "opensearch.auditDelivery.indexingJob.enabled=true", "opensearch.auditProjectionMappingJob.enabled=true",
		} {
			args = append(args, "--set", value)
		}
	}
	for _, value := range overrides {
		args = append(args, "--set-string", value)
	}
	output, err := exec.Command(binary, args...).CombinedOutput()
	return string(output), err
}

// TestAuditChartTiming rejects settings the application would reject, preserving
// automatic ACK derivation and exact millisecond/grace boundaries.
func TestAuditChartTiming(t *testing.T) {
	for _, tt := range []struct {
		name      string
		values    []string
		wantError string
	}{
		{"defaults need audit timings", nil, "audit.enabled requires nats.ackWait"},
		{"drain also required", []string{"nats.ackWait=160s"}, "audit.enabled requires nats.drainTimeout"},
		{"exact audited bounds", []string{"nats.ackWait=160s", "nats.drainTimeout=155s", "app.terminationGracePeriodSeconds=160"}, ""},
		{"compound durations", []string{"nats.ackWait=2m40s", "nats.drainTimeout=2m35s", "app.terminationGracePeriodSeconds=160"}, ""},
		{"automatic ack", []string{"nats.ackWait=0s", "nats.drainTimeout=155s", "app.terminationGracePeriodSeconds=160"}, ""},
		{"empty automatic ack", []string{"nats.ackWait=", "nats.drainTimeout=155s", "app.terminationGracePeriodSeconds=160"}, ""},
		{"legacy bound dominates", []string{"opensearch.timeout=1s", "opensearch.batchMaxWait=10s", "nats.ackWait=21s", "nats.drainTimeout=16s", "app.terminationGracePeriodSeconds=21"}, ""},
		{"legacy ack under budget", []string{"opensearch.timeout=1s", "opensearch.batchMaxWait=10s", "nats.ackWait=20s", "nats.drainTimeout=16s", "app.terminationGracePeriodSeconds=21"}, "nats.ackWait >= 21000ms"},
		{"legacy drain under budget", []string{"opensearch.timeout=1s", "opensearch.batchMaxWait=10s", "nats.ackWait=21s", "nats.drainTimeout=15s", "app.terminationGracePeriodSeconds=21"}, "nats.drainTimeout >= 16000ms"},
		{"millisecond bounds", []string{"opensearch.timeout=1500ms", "opensearch.batchMaxWait=500ms", "nats.ackWait=17500ms", "nats.drainTimeout=12500ms", "app.terminationGracePeriodSeconds=18"}, ""},
		{"grace rounds up", []string{"opensearch.timeout=1500ms", "opensearch.batchMaxWait=500ms", "nats.ackWait=17500ms", "nats.drainTimeout=12500ms", "app.terminationGracePeriodSeconds=17"}, "terminationGracePeriodSeconds"},
		{"negative ack", []string{"nats.ackWait=-1s"}, "nats.ackWait must use integer"},
		{"duration overflow", []string{"nats.ackWait=99999999h"}, "exceeds Go's duration range"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output, err := renderIndexerChart(t, true, tt.values...)
			if tt.wantError == "" {
				require.NoError(t, err, "%s", output)
			} else {
				require.Error(t, err)
				require.Contains(t, output, tt.wantError)
			}
		})
	}
	t.Run("legacy defaults", func(t *testing.T) { output, err := renderIndexerChart(t, false); require.NoError(t, err, "%s", output) })
	t.Run("legacy fractional grace", func(t *testing.T) {
		output, err := renderIndexerChart(t, false, "nats.drainTimeout=55s1ms", "app.terminationGracePeriodSeconds=60")
		require.Error(t, err)
		require.Contains(t, output, "terminationGracePeriodSeconds")
	})
}

// TestAuditChartHookSettings checks shared Job settings and decoded private-index
// bodies, without changing the pure JSON fixtures consumed by storage tests.
func TestAuditChartHookSettings(t *testing.T) {
	output, err := renderIndexerChart(t, true, "nats.ackWait=160s", "nats.drainTimeout=155s", "app.terminationGracePeriodSeconds=160",
		"opensearch.indexingJob.enabled=true", "opensearch.indexingJob.restartPolicy=Never", "opensearch.indexingJob.numberOfShards=3", "opensearch.indexingJob.numberOfReplicas=0")
	require.NoError(t, err, "%s", output)
	for _, file := range []string{"job.yaml", "history-index-job.yaml", "audit-deliveries-job.yaml", "audit-projection-mapping-job.yaml"} {
		t.Run(file, func(t *testing.T) {
			var document string
			for _, part := range strings.Split(output, "\n# Source:") {
				if strings.Contains(part, "/templates/"+file+"\n") && strings.Contains(part, "kind: Job") {
					document = part
					break
				}
			}
			require.NotEmpty(t, document)
			require.Contains(t, document, "restartPolicy: Never")
			if file == "audit-projection-mapping-job.yaml" {
				return
			}
			match := regexp.MustCompile(`echo ["']([A-Za-z0-9+/=]+)["'] \| base64 -d`).FindStringSubmatch(document)
			require.Len(t, match, 2)
			decoded, err := base64.StdEncoding.DecodeString(match[1])
			require.NoError(t, err)
			var body struct {
				Settings struct {
					Index struct {
						Shards   int `json:"number_of_shards"`
						Replicas int `json:"number_of_replicas"`
					} `json:"index"`
				} `json:"settings"`
			}
			require.NoError(t, json.Unmarshal(decoded, &body))
			require.Equal(t, 3, body.Settings.Index.Shards)
			require.Zero(t, body.Settings.Index.Replicas)
		})
	}
}
