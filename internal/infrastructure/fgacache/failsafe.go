// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// maxConcurrentOps matches lfx-v2-fga-sync's cacheOpConcurrency
// (2 * cacheLookupConcurrency): the cache store is single-node, so every
// replica's traffic lands on one server.
const maxConcurrentOps = 128

// errRepairPending means a possibly revoked result could not be hidden. The
// store must stay down until its connect repairs it, even when the caller
// that hit it had already given up.
var errRepairPending = errors.New("a possibly revoked result could not be hidden in the cache")

// store is a cache backend that the fail-safe wrapper can (re)connect.
type store interface {
	contracts.FGACache
	connect(ctx context.Context) error
}

// failSafe makes a store safe to depend on: each call is bounded by a
// timeout, any failure turns into ErrCacheBypassed, and while the store is
// down calls bypass it immediately. A background loop retries the store once
// per retry interval and logs one WARN per failed attempt.
type failSafe struct {
	inner   store
	timeout time.Duration
	retry   time.Duration
	logger  *slog.Logger
	metrics *cacheMetrics

	sem   chan struct{}
	ready atomic.Bool
	down  chan struct{}

	// failures counts markDown calls, so a probe does not report the store
	// ready over a call that failed while the probe was running.
	mu       sync.Mutex
	failures int64

	// probeCtx is canceled by Close so shutdown never waits for a probe.
	probeCtx context.Context
	stop     context.CancelFunc
	done     chan struct{}
}

func newFailSafe(inner store, timeout, retry time.Duration, logger *slog.Logger) *failSafe {
	probeCtx, stop := context.WithCancel(context.Background())
	c := &failSafe{
		inner:    inner,
		timeout:  timeout,
		retry:    retry,
		logger:   logger,
		metrics:  defaultMetrics(),
		sem:      make(chan struct{}, maxConcurrentOps),
		down:     make(chan struct{}, 1),
		probeCtx: probeCtx,
		stop:     stop,
		done:     make(chan struct{}),
	}
	go c.run()
	return c
}

// Close stops the retry loop.
func (c *failSafe) Close() {
	c.stop()
	<-c.done
}

func (c *failSafe) isReady() bool {
	return c.ready.Load()
}

func (c *failSafe) run() {
	defer close(c.done)
	c.probe()
	for {
		if c.ready.Load() {
			select {
			case <-c.down:
			case <-c.probeCtx.Done():
				return
			}
		}
		timer := time.NewTimer(c.retry)
		select {
		case <-timer.C:
		case <-c.probeCtx.Done():
			timer.Stop()
			return
		}
		c.probe()
	}
}

func (c *failSafe) probe() {
	ctx, cancel := context.WithTimeout(c.probeCtx, c.timeout)
	defer cancel()
	c.mu.Lock()
	failures := c.failures
	c.mu.Unlock()
	if err := c.inner.connect(ctx); err != nil {
		if c.probeCtx.Err() != nil {
			return
		}
		c.logger.Warn("FGA cache unavailable, bypassing it",
			"error", err.Error(),
			"retry_in", c.retry.String())
		return
	}
	c.mu.Lock()
	connected := c.failures == failures && c.ready.CompareAndSwap(false, true)
	c.mu.Unlock()
	if connected {
		c.logger.Info("FGA cache connected")
	}
}

func (c *failSafe) markDown(err error) {
	c.mu.Lock()
	c.failures++
	wasReady := c.ready.Swap(false)
	c.mu.Unlock()
	if !wasReady {
		return
	}
	c.logger.Warn("FGA cache call failed, bypassing it until it recovers",
		"error", err.Error(),
		"retry_in", c.retry.String())
	select {
	case c.down <- struct{}{}:
	default:
	}
}

// call runs fn against the store, or returns ErrCacheBypassed without
// running it.
func (c *failSafe) call(ctx context.Context, fn func(context.Context) error) error {
	if !c.ready.Load() {
		c.metrics.bypass(ctx)
		return contracts.ErrCacheBypassed
	}
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-callCtx.Done():
		// Saturation is not an outage; in-flight calls decide that.
		c.metrics.bypass(ctx)
		return contracts.ErrCacheBypassed
	}
	// A call that held a slot may have marked the store down while this one
	// waited.
	if !c.ready.Load() {
		c.metrics.bypass(ctx)
		return contracts.ErrCacheBypassed
	}

	return c.result(ctx, fn(callCtx))
}

func (c *failSafe) result(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	c.metrics.storeError(ctx)
	if ctx.Err() == nil || errors.Is(err, errRepairPending) {
		c.markDown(err)
	}
	return contracts.ErrCacheBypassed
}

func (c *failSafe) CurrentMarker(ctx context.Context) (contracts.InvalidationMarker, error) {
	var m contracts.InvalidationMarker
	err := c.call(ctx, func(ctx context.Context) error {
		var err error
		m, err = c.inner.CurrentMarker(ctx)
		return err
	})
	return m, err
}

func (c *failSafe) Get(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) (map[string]bool, error) {
	var found map[string]bool
	err := c.call(ctx, func(ctx context.Context) error {
		var err error
		found, err = c.inner.Get(ctx, relationKeys, m)
		return err
	})
	if err != nil {
		return nil, err
	}
	hits := 0
	for _, relationKey := range relationKeys {
		if _, ok := found[relationKey]; ok {
			hits++
		}
	}
	c.metrics.lookup(ctx, len(relationKeys), hits)
	return found, nil
}

func (c *failSafe) Set(ctx context.Context, results map[string]bool, m contracts.InvalidationMarker) error {
	return c.call(ctx, func(ctx context.Context) error {
		return c.inner.Set(ctx, results, m)
	})
}

// Invalidate always reaches the store, even while this replica believes it is
// down and without waiting for the budget: the store is shared with
// lfx-v2-fga-sync, so a skipped invalidation would leave its entries fresh.
func (c *failSafe) Invalidate(
	ctx context.Context, before contracts.InvalidationMarker,
) (contracts.InvalidationMarker, bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	m, onlyMine, err := c.inner.Invalidate(callCtx, before)
	if err := c.result(ctx, err); err != nil {
		return contracts.InvalidationMarker{}, false, err
	}
	if !onlyMine {
		c.metrics.overtaken(ctx)
	}
	return m, onlyMine, nil
}

// Seed counts a seed skipped because a marker moved during the write as an
// overtaken invalidation; the store is healthy, so it is not an error.
func (c *failSafe) Seed(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) error {
	return c.call(ctx, func(ctx context.Context) error {
		err := c.inner.Seed(ctx, relationKeys, m)
		if errors.Is(err, errSeedOvertaken) {
			c.metrics.overtaken(ctx)
			return nil
		}
		return err
	})
}

func (c *failSafe) HealthCheck(ctx context.Context) error {
	return c.call(ctx, c.inner.HealthCheck)
}
