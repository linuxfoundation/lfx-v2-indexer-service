// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package fga writes OpenFGA tuples for the fga half of an index request,
// with the write semantics of lfx-v2-fga-sync's generic access operations.
package fga

import (
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/env"
)

// DefaultStoreName is the OpenFGA store looked up when OPENFGA_STORE_NAME is
// not set.
const DefaultStoreName = "lfx-core"

// DefaultLockBucket is the write-lease bucket used when OPENFGA_LOCK_BUCKET is
// not set.
const DefaultLockBucket = "indexer-fga-locks"

const defaultLookupRetryInterval = 5 * time.Second

// Config selects the OpenFGA server and store.
type Config struct {
	// APIURL is the OpenFGA HTTP API URL. Empty turns the writer off.
	APIURL string
	// StoreName is the store looked up with ListStores when StoreID is empty.
	StoreName string
	// StoreID, when set, is used as is and skips the lookup.
	StoreID string
	// AuthModelID, when set, pins every write to that authorization model.
	// Empty writes against the store's latest model.
	AuthModelID string
	// LockBucket is the NATS KV bucket holding the per-object write leases.
	LockBucket string
	// LookupRetryInterval is the wait between store lookups until one
	// succeeds. Zero means five seconds.
	LookupRetryInterval time.Duration
}

// ConfigFromEnv reads OPENFGA_API_URL, OPENFGA_STORE_NAME, OPENFGA_STORE_ID,
// OPENFGA_AUTH_MODEL_ID and OPENFGA_LOCK_BUCKET.
func ConfigFromEnv() Config {
	return Config{
		APIURL:      env.GetString("OPENFGA_API_URL", ""),
		StoreName:   env.GetString("OPENFGA_STORE_NAME", DefaultStoreName),
		StoreID:     env.GetString("OPENFGA_STORE_ID", ""),
		AuthModelID: env.GetString("OPENFGA_AUTH_MODEL_ID", ""),
		LockBucket:  env.GetString("OPENFGA_LOCK_BUCKET", DefaultLockBucket),
	}
}

// Enabled reports whether an OpenFGA API URL is configured.
func (c Config) Enabled() bool {
	return c.APIURL != ""
}

func (c Config) lookupRetryInterval() time.Duration {
	if c.LookupRetryInterval <= 0 {
		return defaultLookupRetryInterval
	}
	return c.LookupRetryInterval
}
