// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// foundStore answers every Get with a fixed result.
type foundStore struct {
	*fakeStore
	found map[string]bool
}

func (s foundStore) Get(context.Context, []string, contracts.InvalidationMarker) (map[string]bool, error) {
	return s.found, nil
}

func newMeteredFailSafe(t *testing.T, inner store) (*failSafe, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	logger, _ := newTestLogger()
	c := newFailSafe(inner, time.Second, time.Hour, logger)
	t.Cleanup(c.Close)
	c.metrics = newCacheMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	waitReady(t, c)
	return c, reader
}

func counterValue(t *testing.T, reader *sdkmetric.ManualReader, name string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var total int64
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "%s is not an int64 sum", name)
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
		}
	}
	return total
}

func TestMetrics_HitsAndMissesCountRelationKeys(t *testing.T) {
	inner := foundStore{fakeStore: newFakeStore(), found: map[string]bool{"obj:1#viewer@user:a": true, "obj:1#viewer@user:b": false}}
	c, reader := newMeteredFailSafe(t, inner)

	_, err := c.Get(context.Background(), []string{"obj:1#viewer@user:a", "obj:1#viewer@user:b", "obj:1#viewer@user:c"}, contracts.InvalidationMarker{})
	require.NoError(t, err)

	assert.Equal(t, int64(2), counterValue(t, reader, "fga_cache.hits"))
	assert.Equal(t, int64(1), counterValue(t, reader, "fga_cache.misses"), "an absent or stale key is a miss")
	assert.Zero(t, counterValue(t, reader, "fga_cache.errors"))
	assert.Zero(t, counterValue(t, reader, "fga_cache.bypasses"))
}

func TestMetrics_StoreErrorThenBypass(t *testing.T) {
	inner := newFakeStore()
	c, reader := newMeteredFailSafe(t, inner)

	inner.setGetErr(errors.New("store down"))
	_, err := c.Get(context.Background(), []string{"obj:1#viewer@user:a"}, contracts.InvalidationMarker{})
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	assert.Equal(t, int64(1), counterValue(t, reader, "fga_cache.errors"))
	assert.Zero(t, counterValue(t, reader, "fga_cache.bypasses"))

	calls := inner.calls.Load()
	_, err = c.Get(context.Background(), []string{"obj:1#viewer@user:a"}, contracts.InvalidationMarker{})
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	require.Equal(t, calls, inner.calls.Load(), "the store was not called")
	assert.Equal(t, int64(1), counterValue(t, reader, "fga_cache.bypasses"))
	assert.Equal(t, int64(1), counterValue(t, reader, "fga_cache.errors"), "a bypass is not a store error")
	assert.Zero(t, counterValue(t, reader, "fga_cache.hits"))
	assert.Zero(t, counterValue(t, reader, "fga_cache.misses"))
}

func TestMetrics_FailedInvalidateIsAnError(t *testing.T) {
	inner := &failingInvalidateStore{fakeStore: newFakeStore()}
	c, reader := newMeteredFailSafe(t, inner)

	_, err := c.Invalidate(context.Background())
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	assert.Equal(t, int64(1), counterValue(t, reader, "fga_cache.errors"))
	assert.Zero(t, counterValue(t, reader, "fga_cache.bypasses"))
}

type failingInvalidateStore struct {
	*fakeStore
}

func (s *failingInvalidateStore) Invalidate(context.Context) (contracts.InvalidationMarker, error) {
	return contracts.InvalidationMarker{}, errors.New("marker write failed")
}
