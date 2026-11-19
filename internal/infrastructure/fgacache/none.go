// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// noneCache is the CACHE_BACKEND=none backend: it never connects and every
// call bypasses the cache.
type noneCache struct{}

func (noneCache) CurrentMarker(context.Context) (contracts.InvalidationMarker, error) {
	return contracts.InvalidationMarker{}, contracts.ErrCacheBypassed
}

func (noneCache) Get(context.Context, []string, contracts.InvalidationMarker) (map[string]bool, error) {
	return nil, contracts.ErrCacheBypassed
}

func (noneCache) Set(context.Context, map[string]bool, contracts.InvalidationMarker) error {
	return contracts.ErrCacheBypassed
}

func (noneCache) Invalidate(context.Context) (contracts.InvalidationMarker, error) {
	return contracts.InvalidationMarker{}, contracts.ErrCacheBypassed
}

func (noneCache) Seed(context.Context, []string, contracts.InvalidationMarker) error {
	return contracts.ErrCacheBypassed
}

func (noneCache) HealthCheck(context.Context) error {
	return contracts.ErrCacheBypassed
}
