// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// FGA cache backends selectable with CACHE_BACKEND.
const (
	CacheBackendNone   = "none"
	CacheBackendNATSKV = "natskv"
	CacheBackendValkey = "valkey"
)

// CacheConfig selects and tunes the FGA check cache.
//
// It is deliberately not part of AppConfig.Validate: a bad cache setting must
// never stop the indexer, so Normalize turns the cache off instead.
type CacheConfig struct {
	Backend       string        `json:"backend"`
	Timeout       time.Duration `json:"timeout"`
	RetryInterval time.Duration `json:"retry_interval"`
	Bucket        string        `json:"bucket"`
	TTL           time.Duration `json:"ttl"`

	ValkeyAddr     string `json:"valkey_addr"`
	ValkeyTLS      bool   `json:"valkey_tls"`
	ValkeyUsername string `json:"-"`
	ValkeyPassword string `json:"-"`

	// malformed names a setting that did not parse or could not be read. The
	// shared env helpers would silently use the default; for the cache that
	// must turn it off.
	malformed string
}

// loadCacheConfig reads the CACHE_* and VALKEY_* settings.
func loadCacheConfig() CacheConfig {
	c := CacheConfig{
		Backend:    os.Getenv("CACHE_BACKEND"),
		Bucket:     os.Getenv("CACHE_BUCKET"),
		ValkeyAddr: os.Getenv("VALKEY_ADDR"),
	}
	if c.Backend == "" {
		c.Backend = CacheBackendNone
	}
	if c.Bucket == "" {
		c.Bucket = "fga-sync-cache"
	}
	c.Timeout = c.duration("CACHE_TIMEOUT", 5*time.Second)
	c.RetryInterval = c.duration("CACHE_RETRY_INTERVAL", 30*time.Second)
	c.TTL = c.duration("CACHE_TTL", 3*time.Hour)
	c.ValkeyTLS = c.boolean("VALKEY_TLS", true)
	c.ValkeyUsername = c.secretFile("VALKEY_USERNAME_FILE")
	c.ValkeyPassword = c.secretFile("VALKEY_PASSWORD_FILE")
	return c
}

func (c *CacheConfig) setMalformed(format string, args ...any) {
	if c.malformed == "" {
		c.malformed = fmt.Sprintf(format, args...)
	}
}

func (c *CacheConfig) duration(key string, defaultValue time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		c.setMalformed("%s is not a valid duration: %q", key, value)
		return defaultValue
	}
	return d
}

func (c *CacheConfig) boolean(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		c.setMalformed("%s is not a valid boolean: %q", key, value)
		return defaultValue
	}
	return b
}

// secretFile returns the trimmed contents of the file named by key, or ""
// when key is unset. The error names the setting, never the contents.
func (c *CacheConfig) secretFile(key string) string {
	path := os.Getenv(key)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the path is operator configuration
	if err != nil {
		c.setMalformed("%s could not be read: %v", key, err)
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Normalize returns the settings to run with. When a setting is invalid it
// returns the cache turned off and a reason for the caller to log.
func (c CacheConfig) Normalize() (CacheConfig, string) {
	if c.Backend == CacheBackendNone || c.Backend == "" {
		c.Backend = CacheBackendNone
		return c, ""
	}

	var reason string
	switch {
	case c.Backend != CacheBackendNATSKV && c.Backend != CacheBackendValkey:
		reason = fmt.Sprintf("unknown CACHE_BACKEND %q (want natskv, valkey or none)", c.Backend)
	case c.malformed != "":
		reason = c.malformed
	case c.Timeout <= 0:
		reason = fmt.Sprintf("CACHE_TIMEOUT must be positive, got %v", c.Timeout)
	case c.RetryInterval <= 0:
		reason = fmt.Sprintf("CACHE_RETRY_INTERVAL must be positive, got %v", c.RetryInterval)
	case c.Backend == CacheBackendNATSKV && c.Bucket == "":
		reason = "CACHE_BUCKET is required for the natskv backend"
	case c.Backend == CacheBackendValkey && c.ValkeyAddr == "":
		reason = "VALKEY_ADDR is required for the valkey backend"
	case c.Backend == CacheBackendValkey && c.TTL < time.Millisecond:
		reason = fmt.Sprintf("CACHE_TTL must be at least 1ms, got %v", c.TTL)
	case c.Backend == CacheBackendValkey:
		addr, err := valkeyHostPort(c.ValkeyAddr, c.ValkeyTLS)
		if err != nil {
			reason = err.Error()
			break
		}
		c.ValkeyAddr = addr
		return c, ""
	default:
		return c, ""
	}

	c.Backend = CacheBackendNone
	return c, reason
}

// valkeyHostPort accepts host:port or a redis:// or rediss:// URL, the form
// deployed endpoints are published in, and returns host:port. Errors never
// quote the value, which could carry a password in its user info.
func valkeyHostPort(addr string, useTLS bool) (string, error) {
	if !strings.Contains(addr, "://") {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || host == "" || port == "" {
			return "", errors.New("VALKEY_ADDR must be host:port or a redis:// or rediss:// URL")
		}
		return addr, nil
	}
	u, err := url.Parse(addr)
	if err != nil {
		return "", errors.New("VALKEY_ADDR is not a valid URL")
	}
	switch {
	case u.Scheme != "redis" && u.Scheme != "rediss":
		return "", fmt.Errorf("VALKEY_ADDR scheme %q is not redis or rediss", u.Scheme)
	case u.User != nil:
		return "", errors.New("VALKEY_ADDR must not contain credentials; use VALKEY_USERNAME_FILE and VALKEY_PASSWORD_FILE")
	case u.Hostname() == "" || u.Port() == "":
		return "", errors.New("VALKEY_ADDR URL must include a host and port")
	case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "":
		return "", errors.New("VALKEY_ADDR URL must not have a path, query or fragment")
	case (u.Scheme == "rediss") != useTLS:
		return "", fmt.Errorf("VALKEY_ADDR scheme %q conflicts with VALKEY_TLS=%t", u.Scheme, useTLS)
	}
	return u.Host, nil
}
