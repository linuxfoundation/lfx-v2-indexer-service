// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

// fakeBulkIndexer is a test double for bulkIndexer that records every call
// and lets tests control the returned itemErrors/err per call.
type fakeBulkIndexer struct {
	mu            sync.Mutex
	calls         [][]contracts.BulkOperation
	ctxErrs       []error // ctx.Err() captured at call time, before flush's defer cancel() fires
	neededRefresh []bool

	itemErrorsFn func(ops []contracts.BulkOperation) []error
	err          error
	delay        time.Duration // simulates a slow round trip
}

func (f *fakeBulkIndexer) BulkIndex(ctx context.Context, operations []contracts.BulkOperation) ([]error, error) {
	f.mu.Lock()
	f.calls = append(f.calls, operations)
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	f.neededRefresh = append(f.neededRefresh, logging.NeedsRefreshWaitFor(ctx))
	delay := f.delay
	f.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	if f.err != nil {
		return nil, f.err
	}
	if f.itemErrorsFn != nil {
		return f.itemErrorsFn(operations), nil
	}
	return make([]error, len(operations)), nil
}

func (f *fakeBulkIndexer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestBatchIndexer_FlushesOnMaxBatchSize(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	b := NewBatchIndexer(fake, logger, 2, time.Hour, 5*time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := b.Index(context.Background(), "test-index", fmt.Sprintf("doc-%d", i), strings.NewReader(`{}`))
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 1, fake.callCount(), "a full batch should flush immediately without waiting for maxWait")
}

func TestBatchIndexer_FlushesOnMaxWait(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	b := NewBatchIndexer(fake, logger, 100, 20*time.Millisecond, 5*time.Second)

	err := b.Index(context.Background(), "test-index", "doc-1", strings.NewReader(`{}`))

	assert.NoError(t, err)
	assert.Equal(t, 1, fake.callCount())
}

func TestBatchIndexer_PerItemErrorRouting(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{
		itemErrorsFn: func(ops []contracts.BulkOperation) []error {
			errs := make([]error, len(ops))
			for i, op := range ops {
				if op.DocID == "doc-bad" {
					errs[i] = fmt.Errorf("simulated failure for %s", op.DocID)
				}
			}
			return errs
		},
	}
	b := NewBatchIndexer(fake, logger, 2, time.Hour, 5*time.Second)

	var wg sync.WaitGroup
	results := make(map[string]error)
	var resultsMu sync.Mutex

	for _, docID := range []string{"doc-good", "doc-bad"} {
		wg.Add(1)
		go func(docID string) {
			defer wg.Done()
			err := b.Index(context.Background(), "test-index", docID, strings.NewReader(`{}`))
			resultsMu.Lock()
			results[docID] = err
			resultsMu.Unlock()
		}(docID)
	}
	wg.Wait()

	assert.NoError(t, results["doc-good"], "each caller should only see its own item's error")
	assert.Error(t, results["doc-bad"])
}

func TestBatchIndexer_TopLevelErrorFansOutToAllCallers(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{err: fmt.Errorf("bulk request failed")}
	b := NewBatchIndexer(fake, logger, 2, time.Hour, 5*time.Second)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = b.Index(context.Background(), "test-index", fmt.Sprintf("doc-%d", i), strings.NewReader(`{}`))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.Errorf(t, err, "caller %d should receive the request-level error", i)
	}
}

func TestBatchIndexer_CallerContextCancellation(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	// maxWait is long enough that the flush never fires within the test,
	// isolating this test to ctx cancellation while the op is still queued.
	b := NewBatchIndexer(fake, logger, 100, time.Hour, 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := b.Index(ctx, "test-index", "doc-1", strings.NewReader(`{}`))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, fake.callCount(), "op was removed from the queue before dispatch, so no write should have happened")
}

func TestBatchIndexer_RefreshWaitForPropagatesToWholeBatch(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	b := NewBatchIndexer(fake, logger, 2, time.Hour, 5*time.Second)

	var wg sync.WaitGroup
	// Only one of the two batched callers needs refresh=wait_for; the whole
	// bulk request must still use it, since refresh is per-request, not per-item.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = b.Index(context.Background(), "test-index", "doc-no-wait", strings.NewReader(`{}`))
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx := logging.WithRefreshWaitFor(context.Background())
		_ = b.Index(ctx, "test-index", "doc-waits", strings.NewReader(`{}`))
	}()
	wg.Wait()

	require.Equal(t, 1, fake.callCount())
	assert.True(t, fake.neededRefresh[0], "batch containing a wait_for caller must flush with refresh=wait_for")
}

func TestBatchIndexer_NoRefreshWaitForWhenNoCallerNeedsIt(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	b := NewBatchIndexer(fake, logger, 1, time.Hour, 5*time.Second)

	_ = b.Index(context.Background(), "test-index", "doc-1", strings.NewReader(`{}`))

	require.Equal(t, 1, fake.callCount())
	assert.False(t, fake.neededRefresh[0])
}

func TestBatchIndexer_FlushUsesTimeoutNotCallerContext(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	b := NewBatchIndexer(fake, logger, 1, time.Hour, 5*time.Second)

	// The caller's own context is already canceled, but the batch still
	// flushes and succeeds because the flush uses its own bounded timeout
	// rather than inheriting from any single caller. Since this caller also
	// fills the batch, its Index call returns as soon as its own ctx is
	// done, racing ahead of the (now async) flush — so wait for the flush
	// to actually happen rather than asserting immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = b.Index(ctx, "test-index", "doc-1", strings.NewReader(`{}`))

	require.Eventually(t, func() bool { return fake.callCount() == 1 }, time.Second, time.Millisecond)
	assert.NoError(t, fake.ctxErrs[0], "flush's context should not be the caller's canceled context")
}

func TestBatchIndexer_DispatchedOpWaitsForRealResultDespiteOwnCtxCancellation(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{delay: 200 * time.Millisecond}
	b := NewBatchIndexer(fake, logger, 1, time.Hour, 5*time.Second)

	// This caller fills the batch itself (maxBatchSize=1), so enqueue claims
	// and dispatches its op to the fake OpenSearch client synchronously,
	// before Index's select even runs. Once dispatch has begun, Index must
	// not abandon the op just because its own ctx subsequently expires:
	// doing so would let a write that goes on to succeed be reported to
	// ProcessTransaction as a ctx-cancellation failure, causing it to skip
	// publishIndexingEvent for a document that was actually committed.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := b.Index(ctx, "test-index", "doc-1", strings.NewReader(`{}`))
	elapsed := time.Since(start)

	assert.NoError(t, err, "the dispatched write actually succeeded, so Index must report that outcome, not the caller's own ctx cancellation")
	assert.GreaterOrEqual(t, elapsed, fake.delay, "Index should wait for the real result once dispatch has begun")
}

// TestBatchIndexer_StaleTimerGenerationSkipsDrain verifies the fix for the
// timer/size-trigger race: if a maxWait timer's callback is still blocked on
// b.mu when a concurrent size-triggered drain claims its batch and a
// subsequent enqueue starts a new one, the stale callback must recognize
// (via the generation it captured at schedule time) that its batch is gone
// and must not drain the newer one — which hasn't waited out its own
// maxWait yet. This drives the internals directly rather than relying on
// goroutine scheduling to reproduce the race, since the interleaving is
// otherwise not reliably reproducible in a unit test.
func TestBatchIndexer_StaleTimerGenerationSkipsDrain(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	b := NewBatchIndexer(fake, logger, 100, time.Hour, 5*time.Second)

	op1 := &pendingIndexOp{
		op:     contracts.BulkOperation{Index: "test-index", DocID: "doc-1", Action: "index", Body: strings.NewReader(`{}`)},
		result: make(chan error, 1),
	}
	b.enqueue(op1, false)

	b.mu.Lock()
	staleGen := b.generation
	// Simulate a concurrent size-triggered drain claiming op1's batch before
	// the maxWait timer callback captured with staleGen gets to run.
	batch, needsRefresh := b.drainLocked()
	b.mu.Unlock()
	go b.flushBatch(batch, needsRefresh)
	require.NoError(t, <-op1.result)

	op2 := &pendingIndexOp{
		op:     contracts.BulkOperation{Index: "test-index", DocID: "doc-2", Action: "index", Body: strings.NewReader(`{}`)},
		result: make(chan error, 1),
	}
	b.enqueue(op2, false) // starts a new batch and bumps the generation

	// The stale callback for staleGen must not drain doc-2's batch.
	b.flush(staleGen)

	b.mu.Lock()
	stillPending := len(b.pending)
	currentGen := b.generation
	b.mu.Unlock()
	assert.Equal(t, 1, stillPending, "a stale-generation flush must not drain a newer batch")

	// The real callback for the current generation still drains normally.
	b.flush(currentGen)
	assert.NoError(t, <-op2.result)
}
