// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"log/slog"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
)

// Cache is the FGA cache selected by configuration.
type Cache struct {
	contracts.FGACache
	// Backend is the backend in use, which is "none" when the configured one
	// could not be used.
	Backend string

	close func()
}

// Close stops the cache's background retry loop.
func (c *Cache) Close() {
	c.close()
}

// New builds the cache selected by cfg. It never fails: an invalid setting
// turns the cache off with a WARN, because a cache problem must not stop the
// indexer.
func New(cfg config.CacheConfig, nc *natsgo.Conn, logger *slog.Logger) *Cache {
	cfg, reason := cfg.Normalize()
	if reason != "" {
		logger.Warn("FGA cache turned off by an invalid setting", "reason", reason)
	}

	switch cfg.Backend {
	case config.CacheBackendNATSKV:
		js, err := jetstream.New(nc)
		if err != nil {
			logger.Warn("FGA cache turned off: JetStream unavailable", "error", err.Error())
			return off()
		}
		c := newFailSafe(newNATSKVStore(js, cfg.Bucket), cfg.Timeout, cfg.RetryInterval, logger)
		return &Cache{FGACache: c, Backend: cfg.Backend, close: c.Close}
	case config.CacheBackendValkey:
		logger.Warn("FGA cache turned off: the valkey backend is not available in this version")
		return off()
	case config.CacheBackendNone:
		return off()
	default:
		logger.Warn("FGA cache turned off: unknown backend", "backend", cfg.Backend)
		return off()
	}
}

func off() *Cache {
	return &Cache{FGACache: noneCache{}, Backend: config.CacheBackendNone, close: func() {}}
}
