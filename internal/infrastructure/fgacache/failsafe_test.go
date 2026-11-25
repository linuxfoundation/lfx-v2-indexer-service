// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// lockedBuffer lets a test read log output while the probe goroutine writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) warnCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Count(b.buf.String(), `"level":"WARN"`)
}

func newTestLogger() (*slog.Logger, *lockedBuffer) {
	buf := &lockedBuffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// fakeStore is a store whose connect and Get outcomes the test controls.
type fakeStore struct {
	connectErr    atomic.Value // error, or nil
	duringConnect atomic.Value // func(), run inside connect
	getErr        atomic.Value
	getDelay      atomic.Int64 // nanoseconds
	blockGet      chan struct{}
	calls         atomic.Int64
	inFlight      atomic.Int64
	maxFlight     atomic.Int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{}
}

func (f *fakeStore) setConnectErr(err error) { f.connectErr.Store(errBox{err}) }
func (f *fakeStore) setGetErr(err error)     { f.getErr.Store(errBox{err}) }

type errBox struct{ err error }

func loadErr(v *atomic.Value) error {
	if box, ok := v.Load().(errBox); ok {
		return box.err
	}
	return nil
}

func (f *fakeStore) connect(context.Context) error {
	if hook, ok := f.duringConnect.Load().(func()); ok {
		hook()
	}
	return loadErr(&f.connectErr)
}

func (f *fakeStore) CurrentMarker(context.Context) (contracts.InvalidationMarker, error) {
	f.calls.Add(1)
	return contracts.NewTimeMarker(time.Time{}), nil
}

func (f *fakeStore) Get(ctx context.Context, _ []string, _ contracts.InvalidationMarker) (map[string]bool, error) {
	f.calls.Add(1)
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxFlight.Load()
		if n <= m || f.maxFlight.CompareAndSwap(m, n) {
			break
		}
	}
	if f.blockGet != nil {
		select {
		case <-f.blockGet:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if d := time.Duration(f.getDelay.Load()); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := loadErr(&f.getErr); err != nil {
		return nil, err
	}
	return map[string]bool{}, nil
}

func (f *fakeStore) Set(context.Context, map[string]bool, contracts.InvalidationMarker) error {
	f.calls.Add(1)
	return nil
}

func (f *fakeStore) Invalidate(context.Context, contracts.InvalidationMarker) (contracts.InvalidationMarker, bool, error) {
	f.calls.Add(1)
	return contracts.NewTimeMarker(time.Now()), true, nil
}

func (f *fakeStore) Seed(context.Context, []string, contracts.InvalidationMarker) error {
	f.calls.Add(1)
	return nil
}

func (f *fakeStore) HealthCheck(context.Context) error { return nil }

func waitReady(t *testing.T, c *failSafe) {
	t.Helper()
	require.Eventually(t, c.isReady, 2*time.Second, 5*time.Millisecond, "cache never became ready")
}

func TestFailSafe_InnerErrorBypassesUntilRecovered(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	c := newFailSafe(store, time.Second, time.Hour, logger)
	defer c.Close()
	waitReady(t, c)

	store.setGetErr(errors.New("store down"))
	_, err := c.Get(context.Background(), []string{"k"}, contracts.InvalidationMarker{})
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)

	before := store.calls.Load()
	_, err = c.Get(context.Background(), []string{"k"}, contracts.InvalidationMarker{})
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	_, err = c.CurrentMarker(context.Background())
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	assert.Equal(t, before, store.calls.Load(), "calls while down must not reach the store")
}

func TestFailSafe_OneWarnPerRetryInterval(t *testing.T) {
	logger, buf := newTestLogger()
	store := newFakeStore()
	store.setConnectErr(errors.New("connection refused"))

	const retry = 50 * time.Millisecond
	start := time.Now()
	c := newFailSafe(store, time.Second, retry, logger)
	time.Sleep(260 * time.Millisecond)
	c.Close()
	elapsed := time.Since(start)

	warns := buf.warnCount()
	assert.GreaterOrEqual(t, warns, 2, "expected a WARN at startup and on later retries")
	assert.LessOrEqual(t, warns, int(elapsed/retry)+1, "at most one WARN per retry interval")
}

func TestFailSafe_ResumesAfterRetryInterval(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	store.setConnectErr(errors.New("connection refused"))
	c := newFailSafe(store, time.Second, 20*time.Millisecond, logger)
	defer c.Close()

	_, err := c.CurrentMarker(context.Background())
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)

	store.setConnectErr(nil)
	waitReady(t, c)
	_, err = c.CurrentMarker(context.Background())
	require.NoError(t, err)
}

func TestFailSafe_RecoversAfterRuntimeFailure(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	c := newFailSafe(store, time.Second, 20*time.Millisecond, logger)
	defer c.Close()
	waitReady(t, c)

	// connect fails too, so the background retry cannot recover before the
	// down-state assertion.
	store.setConnectErr(errors.New("store down"))
	store.setGetErr(errors.New("store down"))
	_, err := c.Get(context.Background(), []string{"k"}, contracts.InvalidationMarker{})
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	assert.False(t, c.isReady())

	store.setConnectErr(nil)
	store.setGetErr(nil)
	waitReady(t, c)
	_, err = c.Get(context.Background(), []string{"k"}, contracts.InvalidationMarker{})
	require.NoError(t, err)
}

func TestFailSafe_SlowCallReturnsWithinTimeout(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	store.getDelay.Store(int64(time.Hour))
	c := newFailSafe(store, 50*time.Millisecond, time.Hour, logger)
	defer c.Close()
	waitReady(t, c)

	start := time.Now()
	_, err := c.Get(context.Background(), []string{"k"}, contracts.InvalidationMarker{})
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	assert.Less(t, time.Since(start), 500*time.Millisecond)
	assert.False(t, c.isReady(), "a timeout marks the cache down")
}

func TestFailSafe_CallerCancelDoesNotMarkDown(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	store.getDelay.Store(int64(time.Hour))
	c := newFailSafe(store, time.Second, time.Hour, logger)
	defer c.Close()
	waitReady(t, c)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Get(ctx, []string{"k"}, contracts.InvalidationMarker{})
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	assert.True(t, c.isReady(), "the caller giving up says nothing about the store")
}

func TestFailSafe_BoundsConcurrentCalls(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	store.blockGet = make(chan struct{})
	c := newFailSafe(store, 5*time.Second, time.Hour, logger)
	defer c.Close()
	waitReady(t, c)

	var wg sync.WaitGroup
	for range 2 * maxConcurrentOps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(context.Background(), []string{"k"}, contracts.InvalidationMarker{})
		}()
	}
	require.Eventually(t, func() bool { return store.inFlight.Load() == maxConcurrentOps }, 2*time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int64(maxConcurrentOps), store.maxFlight.Load(), "the budget is reached and never exceeded")

	close(store.blockGet)
	wg.Wait()
}

func TestFailSafe_QueuedCallBypassesAStoreMarkedDownWhileItWaited(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	store.blockGet = make(chan struct{})
	c := newFailSafe(store, 5*time.Second, time.Hour, logger)
	defer c.Close()
	waitReady(t, c)

	var wg sync.WaitGroup
	for range maxConcurrentOps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(context.Background(), []string{"k"}, contracts.InvalidationMarker{})
		}()
	}
	require.Eventually(t, func() bool { return store.inFlight.Load() == maxConcurrentOps }, 2*time.Second, time.Millisecond)

	queued := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), []string{"k"}, contracts.InvalidationMarker{})
		queued <- err
	}()
	time.Sleep(20 * time.Millisecond)

	c.markDown(errors.New("slot holder failed"))
	close(store.blockGet)
	wg.Wait()

	assert.ErrorIs(t, <-queued, contracts.ErrCacheBypassed)
	assert.Equal(t, int64(maxConcurrentOps), store.calls.Load(), "the queued call never reached the store")
}

func TestFailSafe_InvalidateIsAttemptedWhileDown(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	store.setConnectErr(errors.New("connection refused"))
	c := newFailSafe(store, time.Second, time.Hour, logger)
	defer c.Close()

	// The bucket is shared with lfx-v2-fga-sync, so an invalidation must reach
	// the store even when this replica believes it is down.
	_, err := invalidate(context.Background(), c)
	require.NoError(t, err)
	assert.Equal(t, int64(1), store.calls.Load())
}

func TestFailSafe_InvalidateSkipsTheBudget(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	store.blockGet = make(chan struct{})
	c := newFailSafe(store, 5*time.Second, time.Hour, logger)
	defer c.Close()
	waitReady(t, c)

	var wg sync.WaitGroup
	for range maxConcurrentOps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(context.Background(), []string{"k"}, contracts.InvalidationMarker{})
		}()
	}
	require.Eventually(t, func() bool { return store.inFlight.Load() == maxConcurrentOps }, 2*time.Second, time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := invalidate(context.Background(), c)
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Invalidate waited behind a saturated cache")
	}

	close(store.blockGet)
	wg.Wait()
}

func TestFailSafe_PendingRepairMarksDownEvenAfterCallerGaveUp(t *testing.T) {
	logger, _ := newTestLogger()
	c := newFailSafe(newFakeStore(), time.Second, time.Hour, logger)
	defer c.Close()
	waitReady(t, c)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	assert.ErrorIs(t, c.result(canceled, errors.New("caller gave up")), contracts.ErrCacheBypassed)
	assert.True(t, c.isReady(), "a canceled caller is not an outage")

	assert.ErrorIs(t, c.result(canceled, errRepairPending), contracts.ErrCacheBypassed)
	assert.False(t, c.isReady())
}

func TestFailSafe_ProbeDoesNotRecoverOverACallThatFailedDuringIt(t *testing.T) {
	logger, _ := newTestLogger()
	store := newFakeStore()
	var c *failSafe
	var failDuringProbe atomic.Bool
	failDuringProbe.Store(true)
	ready := make(chan struct{})
	store.duringConnect.Store(func() {
		<-ready
		if failDuringProbe.Load() {
			c.markDown(errRepairPending)
		}
	})
	c = newFailSafe(store, time.Second, 20*time.Millisecond, logger)
	defer c.Close()
	close(ready)

	assert.Never(t, c.isReady, 200*time.Millisecond, 5*time.Millisecond)
	failDuringProbe.Store(false)
	waitReady(t, c)
}

func TestNoneCache_AlwaysBypasses(t *testing.T) {
	ctx := context.Background()
	var c contracts.FGACache = noneCache{}
	m := contracts.InvalidationMarker{}

	_, err := c.CurrentMarker(ctx)
	assert.ErrorIs(t, err, contracts.ErrCacheBypassed)
	_, err = c.Get(ctx, []string{"k"}, m)
	assert.ErrorIs(t, err, contracts.ErrCacheBypassed)
	assert.ErrorIs(t, c.Set(ctx, map[string]bool{"k": true}, m), contracts.ErrCacheBypassed)
	_, err = invalidate(ctx, c)
	assert.ErrorIs(t, err, contracts.ErrCacheBypassed)
	assert.ErrorIs(t, c.Seed(ctx, []string{"k"}, m), contracts.ErrCacheBypassed)
	assert.ErrorIs(t, c.HealthCheck(ctx), contracts.ErrCacheBypassed)
}
