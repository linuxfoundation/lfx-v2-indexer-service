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
		},
		OpenSearch: OpenSearchConfig{
			URL:     "http://opensearch:9200",
			Index:   "resources",
			Timeout: 30 * time.Second,
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

func TestValidate_FullConfig(t *testing.T) {
	cfg := baseValidConfig()
	require.NoError(t, cfg.Validate(), "base valid config should pass full validation")
}
