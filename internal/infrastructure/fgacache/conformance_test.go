// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// conformanceBackend builds the stores the shared suite runs against.
type conformanceBackend struct {
	// fresh returns a connected, empty store with no invalidation recorded.
	fresh func(t *testing.T) contracts.FGACache
	// unreachable returns a store with nothing listening behind it.
	unreachable func(t *testing.T) contracts.FGACache
}

// runConformance is the suite every backend must pass unchanged.
func runConformance(t *testing.T, b conformanceBackend) {
	t.Run("get after set returns true and false results", func(t *testing.T) {
		ctx := testContext(t)
		c := b.fresh(t)
		m, err := c.CurrentMarker(ctx)
		require.NoError(t, err)

		require.NoError(t, c.Set(ctx, map[string]bool{"obj:1#viewer@user:a": true, "obj:1#viewer@user:b": false}, m))

		got, err := c.Get(ctx, []string{"obj:1#viewer@user:a", "obj:1#viewer@user:b", "obj:1#viewer@user:c"}, m)
		require.NoError(t, err)
		assert.Equal(t, map[string]bool{"obj:1#viewer@user:a": true, "obj:1#viewer@user:b": false}, got)
	})

	t.Run("no invalidation recorded means entries are fresh", func(t *testing.T) {
		ctx := testContext(t)
		c := b.fresh(t)
		m, err := c.CurrentMarker(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Set(ctx, map[string]bool{"obj:2#writer@user:a": true}, m))

		m, err = c.CurrentMarker(ctx)
		require.NoError(t, err)
		got, err := c.Get(ctx, []string{"obj:2#writer@user:a"}, m)
		require.NoError(t, err)
		assert.Equal(t, map[string]bool{"obj:2#writer@user:a": true}, got)
	})

	t.Run("invalidation hides entries set before it", func(t *testing.T) {
		ctx := testContext(t)
		c := b.fresh(t)
		before, err := c.CurrentMarker(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Set(ctx, map[string]bool{"obj:3#writer@user:a": true}, before))

		_, err = c.Invalidate(ctx)
		require.NoError(t, err)

		after, err := c.CurrentMarker(ctx)
		require.NoError(t, err)
		got, err := c.Get(ctx, []string{"obj:3#writer@user:a"}, after)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("seed after invalidation is fresh", func(t *testing.T) {
		ctx := testContext(t)
		c := b.fresh(t)
		m, err := c.Invalidate(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Seed(ctx, []string{"obj:4#writer@user:a"}, m))

		current, err := c.CurrentMarker(ctx)
		require.NoError(t, err)
		got, err := c.Get(ctx, []string{"obj:4#writer@user:a"}, current)
		require.NoError(t, err)
		assert.Equal(t, map[string]bool{"obj:4#writer@user:a": true}, got)
	})

	t.Run("invalidation during a check makes its result stale", func(t *testing.T) {
		ctx := testContext(t)
		c := b.fresh(t)
		beforeCheck, err := c.CurrentMarker(ctx)
		require.NoError(t, err)

		// Another writer revokes access while this check is talking to OpenFGA.
		_, err = c.Invalidate(ctx)
		require.NoError(t, err)

		require.NoError(t, c.Set(ctx, map[string]bool{"obj:6#writer@user:a": true}, beforeCheck))

		current, err := c.CurrentMarker(ctx)
		require.NoError(t, err)
		got, err := c.Get(ctx, []string{"obj:6#writer@user:a"}, current)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("concurrent writers never leave an entry from before the latest invalidation fresh", func(t *testing.T) {
		ctx := testContext(t)
		c := b.fresh(t)

		const writers, perWriter, invalidators = 8, 20, 4
		var keys []string
		var mu sync.Mutex
		var wg sync.WaitGroup
		for w := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range perWriter {
					key := fmt.Sprintf("obj:%d#viewer@user:%d", w, i)
					m, err := c.CurrentMarker(ctx)
					if !assert.NoError(t, err) {
						return
					}
					assert.NoError(t, c.Set(ctx, map[string]bool{key: i%2 == 0}, m))
					mu.Lock()
					keys = append(keys, key)
					mu.Unlock()
				}
			}()
		}
		for range invalidators {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 5 {
					_, err := c.Invalidate(ctx)
					assert.NoError(t, err)
				}
			}()
		}
		wg.Wait()

		latest, err := c.Invalidate(ctx)
		require.NoError(t, err)
		got, err := c.Get(ctx, keys, latest)
		require.NoError(t, err)
		assert.Empty(t, got, "every entry was written before the latest invalidation")
	})

	t.Run("health check", func(t *testing.T) {
		ctx := testContext(t)
		require.NoError(t, b.fresh(t).HealthCheck(ctx))

		shortCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		assert.Error(t, b.unreachable(t).HealthCheck(shortCtx))
	})
}

func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
