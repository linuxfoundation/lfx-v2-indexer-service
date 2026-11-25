// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

const meterName = "github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/fgacache"

// cacheMetrics counts how the cache answered. Hits and misses count relation
// keys; errors, bypasses, and overtaken invalidations count calls. A write
// whose marker read failed is passed an empty marker, so it counts as
// overtaken whenever an invalidation exists.
type cacheMetrics struct {
	hits      metric.Int64Counter
	misses    metric.Int64Counter
	errors    metric.Int64Counter
	bypasses  metric.Int64Counter
	overtakes metric.Int64Counter
}

// defaultMetrics uses the global provider, which forwards to whichever
// provider is registered later.
func defaultMetrics() *cacheMetrics {
	return newCacheMetrics(otel.GetMeterProvider())
}

func newCacheMetrics(provider metric.MeterProvider) *cacheMetrics {
	meter := provider.Meter(meterName)
	return &cacheMetrics{
		hits:     newCounter(meter, "fga_cache.hits", "{key}", "Relation keys answered from the FGA cache"),
		misses:   newCounter(meter, "fga_cache.misses", "{key}", "Relation keys absent or stale in the FGA cache"),
		errors:   newCounter(meter, "fga_cache.errors", "{call}", "FGA cache calls that failed in the store"),
		bypasses: newCounter(meter, "fga_cache.bypasses", "{call}", "FGA cache calls skipped because the cache was down or saturated"),
		overtakes: newCounter(meter, "fga_cache.invalidations_overtaken", "{call}",
			"FGA invalidations that were not the first since the write began, "+
				"or whose write began without a marker read; their seeds are skipped"),
	}
}

func newCounter(meter metric.Meter, name, unit, description string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithUnit(unit), metric.WithDescription(description))
	if err != nil {
		return noop.Int64Counter{}
	}
	return c
}

func (m *cacheMetrics) lookup(ctx context.Context, requested, found int) {
	m.hits.Add(ctx, int64(found))
	m.misses.Add(ctx, int64(requested-found))
}

func (m *cacheMetrics) storeError(ctx context.Context) {
	m.errors.Add(ctx, 1)
}

func (m *cacheMetrics) bypass(ctx context.Context) {
	m.bypasses.Add(ctx, 1)
}

func (m *cacheMetrics) overtaken(ctx context.Context) {
	m.overtakes.Add(ctx, 1)
}
