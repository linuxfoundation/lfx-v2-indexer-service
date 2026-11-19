// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package scripts

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
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
		{"drain also required", []string{"nats.ackWait=235s"}, "audit.enabled requires nats.drainTimeout"},
		{"exact audited bounds", []string{"nats.ackWait=235s", "nats.drainTimeout=230s", "app.terminationGracePeriodSeconds=235"}, ""},
		{"old five-request bound", []string{"nats.ackWait=160s", "nats.drainTimeout=155s", "app.terminationGracePeriodSeconds=160"}, "nats.ackWait >= 235000ms"},
		{"six requests without attribution", []string{"nats.ackWait=190s", "nats.drainTimeout=185s", "app.terminationGracePeriodSeconds=190"}, "nats.ackWait >= 235000ms"},
		{"compound durations", []string{"nats.ackWait=3m55s", "nats.drainTimeout=3m50s", "app.terminationGracePeriodSeconds=235"}, ""},
		{"automatic ack", []string{"nats.ackWait=0s", "nats.drainTimeout=230s", "app.terminationGracePeriodSeconds=235"}, ""},
		{"empty automatic ack", []string{"nats.ackWait=", "nats.drainTimeout=230s", "app.terminationGracePeriodSeconds=235"}, ""},
		{"legacy bound dominates", []string{"opensearch.timeout=1s", "opensearch.batchMaxWait=60s", "nats.ackWait=71s", "nats.drainTimeout=66s", "app.terminationGracePeriodSeconds=71"}, ""},
		{"legacy ack under budget", []string{"opensearch.timeout=1s", "opensearch.batchMaxWait=60s", "nats.ackWait=70s", "nats.drainTimeout=66s", "app.terminationGracePeriodSeconds=71"}, "nats.ackWait >= 71000ms"},
		{"legacy drain under budget", []string{"opensearch.timeout=1s", "opensearch.batchMaxWait=60s", "nats.ackWait=71s", "nats.drainTimeout=65s", "app.terminationGracePeriodSeconds=71"}, "nats.drainTimeout >= 66000ms"},
		{"millisecond bounds", []string{"opensearch.timeout=1500ms", "opensearch.batchMaxWait=500ms", "nats.ackWait=64000ms", "nats.drainTimeout=59000ms", "app.terminationGracePeriodSeconds=64"}, ""},
		{"grace rounds up", []string{"opensearch.timeout=1501ms", "opensearch.batchMaxWait=500ms", "nats.ackWait=64006ms", "nats.drainTimeout=59006ms", "app.terminationGracePeriodSeconds=64"}, "terminationGracePeriodSeconds"},
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
	output, err := renderIndexerChart(t, true, "nats.ackWait=235s", "nats.drainTimeout=230s", "app.terminationGracePeriodSeconds=235",
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

// TestCacheChartSettings checks that each cache backend renders only its own
// settings and that Valkey credentials arrive as mounted files.
func TestCacheChartSettings(t *testing.T) {
	valkey := []string{"cache.backend=valkey", "cache.valkey.addr=valkey.example.test:6379"}
	withSecret := append(append([]string{}, valkey...), "cache.valkey.existingSecret=example-valkey")
	for _, tt := range []struct {
		name    string
		audited bool
		values  []string
		want    []string
		absent  []string
	}{
		{"default is off", false, nil,
			[]string{"name: CACHE_BACKEND\n              value: \"none\""},
			[]string{"CACHE_BUCKET", "VALKEY_", "valkey-auth"}},
		{"natskv", false, []string{"cache.backend=natskv"},
			[]string{"name: CACHE_BUCKET\n              value: \"fga-sync-cache\""},
			[]string{"CACHE_TTL", "VALKEY_", "valkey-auth"}},
		{"valkey without auth", false, valkey,
			[]string{"name: VALKEY_ADDR\n              value: \"valkey.example.test:6379\"", "name: VALKEY_TLS\n              value: \"true\"", "name: CACHE_TTL\n              value: \"3h\""},
			[]string{"CACHE_BUCKET", "VALKEY_USERNAME_FILE", "VALKEY_PASSWORD_FILE", "valkey-auth"}},
		{"valkey with auth", false, withSecret,
			[]string{"value: \"/etc/valkey-auth/username\"", "value: \"/etc/valkey-auth/password\"", "mountPath: /etc/valkey-auth", "secretName: example-valkey\n            optional: true"},
			nil},
		{"valkey password only", false, append(append([]string{}, withSecret...), "cache.valkey.usernameKey="),
			[]string{"value: \"/etc/valkey-auth/password\""},
			[]string{"VALKEY_USERNAME_FILE"}},
		{"valkey auth alongside audit", true, append(append([]string{}, withSecret...), "nats.ackWait=235s", "nats.drainTimeout=230s", "app.terminationGracePeriodSeconds=235"),
			[]string{"mountPath: /etc/indexer\n", "mountPath: /etc/valkey-auth", "name: audit-policy", "secretName: example-valkey"},
			nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output, err := renderIndexerChart(t, tt.audited, tt.values...)
			require.NoError(t, err, "%s", output)
			for _, want := range tt.want {
				require.Contains(t, output, want)
			}
			for _, absent := range tt.absent {
				require.NotContains(t, output, absent)
			}
		})
	}
}

func TestAuditChartTiming_RuntimeParity(t *testing.T) {
	for _, tt := range []struct{ timeout, batch string }{
		{"30s", "200ms"}, {"1501ms", "500ms"}, {"1s", "60s"},
	} {
		t.Run(tt.timeout+"/"+tt.batch, func(t *testing.T) {
			t.Setenv("AUDIT_ENABLED", "true")
			t.Setenv("NATS_ACK_WAIT", "0s")
			t.Setenv("OPENSEARCH_TIMEOUT", tt.timeout)
			t.Setenv("OPENSEARCH_BATCH_MAX_WAIT", tt.batch)
			cfg, err := config.LoadConfig()
			require.NoError(t, err)
			// ACK safety is 10s; drain safety is 5s. Kubernetes adds 5s
			// after draining, rounding its grace period up to whole seconds.
			drain := cfg.NATS.AckWait - 5*time.Second
			grace := (cfg.NATS.AckWait + time.Second - 1) / time.Second
			values := []string{"opensearch.timeout=" + tt.timeout, "opensearch.batchMaxWait=" + tt.batch,
				"nats.ackWait=" + cfg.NATS.AckWait.String(), "nats.drainTimeout=" + drain.String(),
				"app.terminationGracePeriodSeconds=" + strconv.FormatInt(int64(grace), 10)}
			// Use integer milliseconds: Go's Duration.String can print fractional
			// seconds, whereas chart timing inputs use integer components.
			values[2] = "nats.ackWait=" + strconv.FormatInt(cfg.NATS.AckWait.Milliseconds(), 10) + "ms"
			values[3] = "nats.drainTimeout=" + strconv.FormatInt(drain.Milliseconds(), 10) + "ms"
			output, err := renderIndexerChart(t, true, values...)
			require.NoError(t, err, "%s", output)
			output, err = renderIndexerChart(t, true, append(values, "nats.ackWait="+strconv.FormatInt(cfg.NATS.AckWait.Milliseconds()-1, 10)+"ms")...)
			require.Error(t, err)
			require.Contains(t, output, "nats.ackWait >=")
			output, err = renderIndexerChart(t, true, append(values, "nats.drainTimeout="+strconv.FormatInt(drain.Milliseconds()-1, 10)+"ms")...)
			require.Error(t, err)
			require.Contains(t, output, "nats.drainTimeout >=")
		})
	}
}

// TestAuditRunbookTimingExample validates the actual documented example even
// when Helm is unavailable, rather than maintaining another detached fixture.
func TestAuditRunbookTimingExample(t *testing.T) {
	document, err := os.ReadFile("../docs/audit-rollout.md")
	require.NoError(t, err)
	match := regexp.MustCompile(`For example, a ([0-9]+)s OpenSearch timeout and ([0-9]+)s batch wait require at least ([0-9]+)s\s+ACK wait and ([0-9]+)s drain`).FindStringSubmatch(string(document))
	require.Len(t, match, 5)
	t.Setenv("AUDIT_ENABLED", "true")
	t.Setenv("NATS_ACK_WAIT", "0s")
	t.Setenv("OPENSEARCH_TIMEOUT", match[1]+"s")
	t.Setenv("OPENSEARCH_BATCH_MAX_WAIT", match[2]+"s")
	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	ack, err := time.ParseDuration(match[3] + "s")
	require.NoError(t, err)
	drain, err := time.ParseDuration(match[4] + "s")
	require.NoError(t, err)
	require.Equal(t, cfg.NATS.AckWait, ack)
	require.Equal(t, cfg.NATS.AckWait-5*time.Second, drain)
}

func TestAuditRunbookAttributionMapping(t *testing.T) {
	document, err := os.ReadFile("../docs/audit-rollout.md")
	require.NoError(t, err)
	match := regexp.MustCompile("(?s)```http\\s+PUT /resource-history-v1/_mapping\\s+Content-Type: application/json\\s+(.*?)\\s+```").FindStringSubmatch(string(document))
	require.Len(t, match, 2)
	var update map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(match[1]), &update))
	require.Len(t, update, 1, "mapping update must not change dynamic, _source or other settings")
	require.Len(t, update["properties"], 4)
	fixture, err := os.ReadFile("../charts/lfx-v2-indexer-service/files/opensearch-resource-history-v1.json")
	require.NoError(t, err)
	var provisioned struct {
		Mappings struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"mappings"`
	}
	require.NoError(t, json.Unmarshal(fixture, &provisioned))
	for _, field := range []string{"actor_ref", "effective_actor_ref", "actor_verified", "effective_actor_verified"} {
		require.JSONEq(t, string(provisioned.Mappings.Properties[field]), string(update["properties"][field]), field)
	}
}

func TestChart_IndexingSubjectOnly(t *testing.T) {
	for _, subject := range []string{"lfx.index.>", "lfx.index.v1_meeting"} {
		t.Run(subject, func(t *testing.T) {
			output, err := renderIndexerChart(t, false, "nats.indexingSubject="+subject)
			require.NoError(t, err, "%s", output)
			require.NotContains(t, output, "NATS_V1_INDEXING_SUBJECT")
			require.NotContains(t, output, "v1_data")
			require.NotContains(t, output, "lfx.v1.index.")
			decoder := yaml.NewDecoder(strings.NewReader(output))
			found := false
			for {
				var document struct {
					Kind string `yaml:"kind"`
					Spec struct {
						Name     string   `yaml:"name"`
						Subjects []string `yaml:"subjects"`
					} `yaml:"spec"`
				}
				err := decoder.Decode(&document)
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				if document.Kind == "Stream" && document.Spec.Name == "index-events" {
					found = true
					require.Equal(t, []string{subject}, document.Spec.Subjects)
				}
			}
			require.True(t, found, "the chart must provision the supported stream subject")
		})
	}
}
