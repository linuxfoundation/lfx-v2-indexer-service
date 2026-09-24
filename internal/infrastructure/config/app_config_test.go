// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseValidConfig() *AppConfig {
	return &AppConfig{
		Server: ServerConfig{
			Port:            8080,
			ReadTimeout:     5 * time.Second,
			WriteTimeout:    5 * time.Second,
			ShutdownTimeout: 10 * time.Second,
		},
		NATS: NATSConfig{
			URL:               "nats://nats:4222",
			MaxReconnects:     -1,
			ReconnectWait:     2 * time.Second,
			ConnectionTimeout: 10 * time.Second,
			IndexingSubject:   "lfx.index.>",
			V1IndexingSubject: "lfx.v1.index.>",
			Queue:             "lfx.indexer.queue",
			DrainTimeout:      55 * time.Second,
			PendingMsgLimit:   1024,
			PendingBytesLimit: 1024 * 1024,
			WorkerCount:       10,
			AckWait:           40 * time.Second,
		},
		OpenSearch: OpenSearchConfig{
			URL:          "http://opensearch:9200",
			Index:        "resources",
			Timeout:      30 * time.Second,
			BatchMaxSize: 50,
			BatchMaxWait: 200 * time.Millisecond,
		},
		JWT: JWTConfig{
			Issuer:    "heimdall",
			Audiences: []string{"projects-api"},
			JWKSURL:   "http://heimdall:4457/.well-known/jwks",
			ClockSkew: 6 * time.Hour,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Janitor: JanitorConfig{Enabled: true},
		Health: HealthConfig{
			CheckTimeout:  5 * time.Second,
			CacheDuration: 5 * time.Second,
		},
	}
}

func TestValidateNATS_MaxReconnects(t *testing.T) {
	cases := []struct {
		name      string
		value     int
		wantError bool
	}{
		{"below sentinel is rejected", -2, true},
		{"sentinel -1 (infinite) is allowed", -1, false},
		{"zero is allowed", 0, false},
		{"positive is allowed", 10, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseValidConfig()
			cfg.NATS.MaxReconnects = tc.value
			err := cfg.validateNATS()
			if tc.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateNATS_AckWait(t *testing.T) {
	cases := []struct {
		name      string
		ackWait   time.Duration
		wantError bool
	}{
		{"ack wait below opensearch timeout is rejected", 20 * time.Second, true},
		{"ack wait equal to opensearch timeout is rejected", 30 * time.Second, true},
		{"ack wait above opensearch timeout is allowed", 40 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseValidConfig()
			cfg.NATS.AckWait = tc.ackWait
			err := cfg.validateNATS()
			if tc.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateNATS_AckWait_AccountsForBatchMaxWait(t *testing.T) {
	// baseValidConfig has OpenSearch.Timeout=30s; with BatchMaxWait raised
	// to 5s, an AckWait of 34s clears the old Timeout-only bound but not
	// the combined Timeout+BatchMaxWait bound, and must still be rejected.
	cfg := baseValidConfig()
	cfg.OpenSearch.BatchMaxWait = 5 * time.Second
	cfg.NATS.AckWait = 34 * time.Second

	err := cfg.validateNATS()
	assert.Error(t, err, "ack wait must exceed OpenSearch timeout plus batch max wait, not just the timeout")

	cfg.NATS.AckWait = 36 * time.Second
	assert.NoError(t, cfg.validateNATS())
}

func TestValidateOpenSearch_Timeout(t *testing.T) {
	cases := []struct {
		name      string
		value     time.Duration
		wantError bool
	}{
		{"zero timeout is rejected", 0, true},
		{"negative timeout is rejected", -1 * time.Second, true},
		{"positive timeout is allowed", 30 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseValidConfig()
			cfg.OpenSearch.Timeout = tc.value
			err := cfg.validateOpenSearch()
			if tc.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateOpenSearch_BatchMaxSize(t *testing.T) {
	cases := []struct {
		name      string
		value     int
		wantError bool
	}{
		{"zero is rejected", 0, true},
		{"negative is rejected", -1, true},
		{"positive is allowed", 50, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseValidConfig()
			cfg.OpenSearch.BatchMaxSize = tc.value
			err := cfg.validateOpenSearch()
			if tc.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateOpenSearch_BatchMaxWait(t *testing.T) {
	cases := []struct {
		name      string
		value     time.Duration
		wantError bool
	}{
		{"zero is rejected", 0, true},
		{"negative is rejected", -1 * time.Millisecond, true},
		{"positive is allowed", 200 * time.Millisecond, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseValidConfig()
			cfg.OpenSearch.BatchMaxWait = tc.value
			err := cfg.validateOpenSearch()
			if tc.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_FullConfig(t *testing.T) {
	cfg := baseValidConfig()
	require.NoError(t, cfg.Validate(), "base valid config should pass full validation")
}

func TestLoadConfig_NATSMaxReconnectsDefault(t *testing.T) {
	t.Setenv("NATS_MAX_RECONNECTS", "")
	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, -1, cfg.NATS.MaxReconnects,
		"NATS_MAX_RECONNECTS should default to -1 (infinite) when unset")
}

func TestLoadConfig_AckWaitDefaultIncludesBatchMaxWait(t *testing.T) {
	t.Setenv("NATS_ACK_WAIT", "")
	t.Setenv("OPENSEARCH_TIMEOUT", "30s")
	t.Setenv("OPENSEARCH_BATCH_MAX_WAIT", "5s")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, 45*time.Second, cfg.NATS.AckWait,
		"default ack wait should be OpenSearch.Timeout + BatchMaxWait + 10s margin")
}
