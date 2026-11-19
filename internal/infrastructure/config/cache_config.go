// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
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
	case c.Timeout <= 0:
		reason = fmt.Sprintf("CACHE_TIMEOUT must be positive, got %v", c.Timeout)
	case c.RetryInterval <= 0:
		reason = fmt.Sprintf("CACHE_RETRY_INTERVAL must be positive, got %v", c.RetryInterval)
	case c.Backend == CacheBackendNATSKV && c.Bucket == "":
		reason = "CACHE_BUCKET is required for the natskv backend"
	default:
		return c, ""
	}

	c.Backend = CacheBackendNone
	return c, reason
}
