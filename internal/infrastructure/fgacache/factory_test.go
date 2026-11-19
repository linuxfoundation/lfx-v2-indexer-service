// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
)

func cacheConfig(backend string) config.CacheConfig {
	return config.CacheConfig{
		Backend:       backend,
		Timeout:       time.Second,
		RetryInterval: 50 * time.Millisecond,
		Bucket:        newBucketName(),
	}
}

func TestNew_TurnsTheCacheOffWithoutFailing(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config.CacheConfig
		wantWarn bool
	}{
		{"none", cacheConfig(config.CacheBackendNone), false},
		{"unknown backend", cacheConfig("redis"), true},
		{"invalid timeout", func() config.CacheConfig {
			c := cacheConfig(config.CacheBackendNATSKV)
			c.Timeout = 0
			return c
		}(), true},
		{"valkey without an address", cacheConfig(config.CacheBackendValkey), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, buf := newTestLogger()
			c := New(tt.cfg, nil, logger)
			defer c.Close()

			assert.Equal(t, config.CacheBackendNone, c.Backend)
			_, err := c.CurrentMarker(testContext(t))
			assert.ErrorIs(t, err, contracts.ErrCacheBypassed)
			assert.Equal(t, tt.wantWarn, buf.warnCount() > 0)
		})
	}
}

func TestNew_NATSKV(t *testing.T) {
	s := startJetStream(t)
	nc, err := natsgo.Connect(s.ClientURL())
	require.NoError(t, err)
	defer nc.Close()
	js := connectJetStream(t, s)

	cfg := cacheConfig(config.CacheBackendNATSKV)
	createBucket(t, js, cfg.Bucket)

	logger, _ := newTestLogger()
	c := New(cfg, nc, logger)
	defer c.Close()

	assert.Equal(t, config.CacheBackendNATSKV, c.Backend)
	require.Eventually(t, func() bool {
		_, err := c.CurrentMarker(testContext(t))
		return err == nil
	}, 2*time.Second, 5*time.Millisecond)
}

func TestNew_ValkeyWithNothingListeningStillStarts(t *testing.T) {
	cfg := cacheConfig(config.CacheBackendValkey)
	cfg.TTL = time.Hour
	cfg.ValkeyAddr = unusedAddr(t)

	logger, buf := newTestLogger()
	c := New(cfg, nil, logger)
	defer c.Close()

	assert.Equal(t, config.CacheBackendValkey, c.Backend)
	_, err := c.CurrentMarker(testContext(t))
	assert.ErrorIs(t, err, contracts.ErrCacheBypassed)
	require.Eventually(t, func() bool { return buf.warnCount() > 0 }, 2*time.Second, 5*time.Millisecond)
}
