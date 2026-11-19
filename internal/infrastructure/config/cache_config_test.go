// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearCacheEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CACHE_BACKEND", "CACHE_TIMEOUT", "CACHE_RETRY_INTERVAL", "CACHE_BUCKET", "CACHE_TTL",
		"VALKEY_ADDR", "VALKEY_TLS", "VALKEY_USERNAME_FILE", "VALKEY_PASSWORD_FILE",
	} {
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
		TTL:           3 * time.Hour,
		ValkeyTLS:     true,
	}, cfg.Cache)
}

func TestLoadConfig_CacheFromEnv(t *testing.T) {
	clearCacheEnv(t)
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
		TTL:           3 * time.Hour,
		ValkeyTLS:     true,
	}, cfg.Cache)
}

func writeSecretFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func TestLoadConfig_ValkeyFromEnv(t *testing.T) {
	clearCacheEnv(t)
	t.Setenv("CACHE_BACKEND", "valkey")
	t.Setenv("CACHE_TTL", "90m")
	t.Setenv("VALKEY_ADDR", "valkey.example.test:6379")
	t.Setenv("VALKEY_TLS", "false")
	t.Setenv("VALKEY_USERNAME_FILE", writeSecretFile(t, "example-user\n"))
	t.Setenv("VALKEY_PASSWORD_FILE", writeSecretFile(t, "  example-password \n"))

	cfg, err := LoadConfig()
	assert.NoError(t, err)
	got, reason := cfg.Cache.Normalize()
	assert.Empty(t, reason)
	assert.Equal(t, CacheBackendValkey, got.Backend)
	assert.Equal(t, 90*time.Minute, got.TTL)
	assert.Equal(t, "valkey.example.test:6379", got.ValkeyAddr)
	assert.False(t, got.ValkeyTLS)
	assert.Equal(t, "example-user", got.ValkeyUsername)
	assert.Equal(t, "example-password", got.ValkeyPassword)
}

func TestLoadConfig_ValkeyWithoutCredentialFilesHasNoAuth(t *testing.T) {
	clearCacheEnv(t)
	t.Setenv("CACHE_BACKEND", "valkey")
	t.Setenv("VALKEY_ADDR", "valkey.example.test:6379")

	cfg, err := LoadConfig()
	assert.NoError(t, err)
	got, reason := cfg.Cache.Normalize()
	assert.Empty(t, reason)
	assert.True(t, got.ValkeyTLS)
	assert.Empty(t, got.ValkeyUsername)
	assert.Empty(t, got.ValkeyPassword)
}

func TestLoadConfig_InvalidValkeySettingTurnsCacheOff(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"missing address", "VALKEY_ADDR", ""},
		{"malformed tls flag", "VALKEY_TLS", "sometimes"},
		{"unreadable password file", "VALKEY_PASSWORD_FILE", filepath.Join(os.TempDir(), "does-not-exist", "password")},
		{"unreadable username file", "VALKEY_USERNAME_FILE", filepath.Join(os.TempDir(), "does-not-exist", "username")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearCacheEnv(t)
			t.Setenv("CACHE_BACKEND", "valkey")
			t.Setenv("VALKEY_ADDR", "valkey.example.test:6379")
			t.Setenv(tc.key, tc.value)

			cfg, err := LoadConfig()
			assert.NoError(t, err)
			got, reason := cfg.Cache.Normalize()
			assert.Equal(t, CacheBackendNone, got.Backend)
			assert.Contains(t, reason, tc.key)
		})
	}
}

func TestLoadConfig_MalformedCacheDurationTurnsCacheOff(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"CACHE_TIMEOUT", "bogus"},
		{"CACHE_RETRY_INTERVAL", "30sec"},
		{"CACHE_TTL", "3hours"},
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
		TTL:           3 * time.Hour,
		ValkeyAddr:    "valkey.example.test:6379",
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
		{"valkey without an address turns the cache off", func(c *CacheConfig) {
			c.Backend = CacheBackendValkey
			c.ValkeyAddr = ""
		}, CacheBackendNone, true},
		{"valkey with a non-positive ttl turns the cache off", func(c *CacheConfig) {
			c.Backend = CacheBackendValkey
			c.TTL = 0
		}, CacheBackendNone, true},
		{"valkey with a ttl under 1ms turns the cache off", func(c *CacheConfig) {
			c.Backend = CacheBackendValkey
			c.TTL = 500 * time.Microsecond
		}, CacheBackendNone, true},
		{"valkey with a 1ms ttl is kept", func(c *CacheConfig) {
			c.Backend = CacheBackendValkey
			c.TTL = time.Millisecond
		}, CacheBackendValkey, false},
		{"natskv ignores an unset valkey address", func(c *CacheConfig) { c.ValkeyAddr = "" }, CacheBackendNATSKV, false},
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

func TestCacheConfig_NormalizeValkeyAddr(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		useTLS   bool
		wantAddr string // empty means the cache is turned off
	}{
		{"host and port", "valkey.example.test:6379", true, "valkey.example.test:6379"},
		{"rediss url with tls", "rediss://valkey.example.test:6379", true, "valkey.example.test:6379"},
		{"rediss url with trailing slash", "rediss://valkey.example.test:6379/", true, "valkey.example.test:6379"},
		{"redis url without tls", "redis://127.0.0.1:6379", false, "127.0.0.1:6379"},
		{"rediss url with tls off", "rediss://valkey.example.test:6379", false, ""},
		{"redis url with tls on", "redis://valkey.example.test:6379", true, ""},
		{"host without port", "valkey.example.test", true, ""},
		{"port without host", ":6379", true, ""},
		{"host with empty port", "valkey.example.test:", true, ""},
		{"ipv6 host and port", "[::1]:6379", false, "[::1]:6379"},
		{"url without port", "rediss://valkey.example.test", true, ""},
		{"url with credentials", "rediss://user:secret@valkey.example.test:6379", true, ""},
		{"url with database path", "rediss://valkey.example.test:6379/2", true, ""},
		{"url with query", "rediss://valkey.example.test:6379?protocol=3", true, ""},
		{"other scheme", "https://valkey.example.test:6379", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := CacheConfig{
				Backend:       CacheBackendValkey,
				Timeout:       5 * time.Second,
				RetryInterval: 30 * time.Second,
				TTL:           3 * time.Hour,
				ValkeyAddr:    tt.addr,
				ValkeyTLS:     tt.useTLS,
			}

			got, reason := cfg.Normalize()
			if tt.wantAddr == "" {
				assert.Equal(t, CacheBackendNone, got.Backend)
				assert.NotEmpty(t, reason)
				assert.NotContains(t, reason, "secret")
				return
			}
			assert.Empty(t, reason)
			assert.Equal(t, CacheBackendValkey, got.Backend)
			assert.Equal(t, tt.wantAddr, got.ValkeyAddr)
		})
	}
}
