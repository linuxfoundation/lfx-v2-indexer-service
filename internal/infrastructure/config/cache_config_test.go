// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func clearCacheEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"CACHE_BACKEND", "CACHE_TIMEOUT", "CACHE_RETRY_INTERVAL", "CACHE_BUCKET"} {
		t.Setenv(key, "")
	}
}

func TestLoadConfig_CacheDefaults(t *testing.T) {
	clearCacheEnv(t)
	cfg, err := LoadConfig()
	assert.NoError(t, err)
	assert.Equal(t, CacheConfig{
		Backend:       CacheBackendNone,
		Timeout:       5 * time.Second,
		RetryInterval: 30 * time.Second,
		Bucket:        "fga-sync-cache",
	}, cfg.Cache)
}

func TestLoadConfig_CacheFromEnv(t *testing.T) {
	t.Setenv("CACHE_BACKEND", "natskv")
	t.Setenv("CACHE_TIMEOUT", "2s")
	t.Setenv("CACHE_RETRY_INTERVAL", "10s")
	t.Setenv("CACHE_BUCKET", "other-bucket")

	cfg, err := LoadConfig()
	assert.NoError(t, err)
	assert.Equal(t, CacheConfig{
		Backend:       CacheBackendNATSKV,
		Timeout:       2 * time.Second,
		RetryInterval: 10 * time.Second,
		Bucket:        "other-bucket",
	}, cfg.Cache)
}

func TestLoadConfig_MalformedCacheDurationTurnsCacheOff(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"CACHE_TIMEOUT", "bogus"},
		{"CACHE_RETRY_INTERVAL", "30sec"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			clearCacheEnv(t)
			t.Setenv("CACHE_BACKEND", "natskv")
			t.Setenv(tc.key, tc.value)

			cfg, err := LoadConfig()
			assert.NoError(t, err)
			got, reason := cfg.Cache.Normalize()
			assert.Equal(t, CacheBackendNone, got.Backend)
			assert.Contains(t, reason, tc.key)
		})
	}
}

func TestCacheConfig_Normalize(t *testing.T) {
	valid := CacheConfig{
		Backend:       CacheBackendNATSKV,
		Timeout:       5 * time.Second,
		RetryInterval: 30 * time.Second,
		Bucket:        "fga-sync-cache",
	}

	tests := []struct {
		name        string
		mutate      func(*CacheConfig)
		wantBackend string
		wantReason  bool
	}{
		{"natskv is kept", func(*CacheConfig) {}, CacheBackendNATSKV, false},
		{"valkey is kept", func(c *CacheConfig) { c.Backend = CacheBackendValkey }, CacheBackendValkey, false},
		{"none is kept", func(c *CacheConfig) { c.Backend = CacheBackendNone }, CacheBackendNone, false},
		{"unset backend is off without a warning", func(c *CacheConfig) { c.Backend = "" }, CacheBackendNone, false},
		{"unknown backend turns the cache off", func(c *CacheConfig) { c.Backend = "redis" }, CacheBackendNone, true},
		{"zero timeout turns the cache off", func(c *CacheConfig) { c.Timeout = 0 }, CacheBackendNone, true},
		{"negative retry interval turns the cache off", func(c *CacheConfig) { c.RetryInterval = -time.Second }, CacheBackendNone, true},
		{"empty bucket turns natskv off", func(c *CacheConfig) { c.Bucket = "" }, CacheBackendNone, true},
		{"bad values are ignored when the cache is off", func(c *CacheConfig) {
			c.Backend = CacheBackendNone
			c.Timeout = 0
		}, CacheBackendNone, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)

			got, reason := cfg.Normalize()
			assert.Equal(t, tt.wantBackend, got.Backend)
			if tt.wantReason {
				assert.NotEmpty(t, reason)
			} else {
				assert.Empty(t, reason)
			}
		})
	}
}
