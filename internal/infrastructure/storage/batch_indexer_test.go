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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

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

// TestBatchIndexer_TruncatedItemErrorsDefaultToFailure guards against a
// BulkIndex implementation (real or faked in a test) that returns an
// itemErrors slice shorter than ops: every op beyond the slice's length must
// be treated as failed, never silently reported as a success.
func TestBatchIndexer_TruncatedItemErrorsDefaultToFailure(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{
		itemErrorsFn: func(_ []contracts.BulkOperation) []error {
			return nil // shorter than ops — simulates a broken/mocked BulkIndex
		},
	}
	b := NewBatchIndexer(fake, logger, 1, time.Hour, 5*time.Second)

	err := b.Index(context.Background(), "test-index", "doc-1", strings.NewReader(`{}`))
	require.Error(t, err, "a missing itemErrors entry must not be reported as success")
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
	// maxBatchSize=100 and maxWait is long enough that this lone op never
	// gets claimed by a flush within the test, isolating this test to ctx
	// cancellation while the op is still sitting in the pending queue. The
	// ctx starts valid (passes Index's pre-check) and is only canceled via
	// its own short deadline after enqueue, so this exercises removeIfPending
	// rather than the pre-check added for an already-dead caller.
	b := NewBatchIndexer(fake, logger, 100, time.Hour, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := b.Index(ctx, "test-index", "doc-1", strings.NewReader(`{}`))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
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

func TestBatchIndexer_CanceledRefreshWaitForCallerDoesNotStickToLaterBatch(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	b := NewBatchIndexer(fake, logger, 2, time.Hour, 5*time.Second)

	// op1's ctx is valid at call time (unlike a pre-canceled ctx, which
	// Index would reject before ever enqueuing it) and only expires via its
	// own short deadline after it is already sitting in the pending queue
	// (maxBatchSize=2 means a lone op never gets claimed by a flush). This
	// exercises removeIfPending's actual recompute of needsRefresh, not just
	// the pre-check for an already-dead caller.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	waitCtx := logging.WithRefreshWaitFor(ctx)
	err := b.Index(waitCtx, "test-index", "doc-canceled", strings.NewReader(`{}`))
	require.Error(t, err)

	// op2 and op3 form a new batch that reaches maxBatchSize; neither needs
	// refresh=wait_for. If op1 had left any trace on BatchIndexer's
	// needsRefresh flag, this later, unrelated batch would incorrectly
	// flush with refresh=wait_for too.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = b.Index(context.Background(), "test-index", "doc-2", strings.NewReader(`{}`))
	}()
	go func() {
		defer wg.Done()
		_ = b.Index(context.Background(), "test-index", "doc-3", strings.NewReader(`{}`))
	}()
	wg.Wait()

	require.Equal(t, 1, fake.callCount())
	assert.False(t, fake.neededRefresh[0], "canceling the only wait_for caller must clear needsRefresh for the batch that follows it")
}

func TestBatchIndexer_RejectsAlreadyCanceledContextBeforeDispatch(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	// maxBatchSize=1 means this caller's own op would otherwise fill and
	// claim the batch immediately inside enqueue, before Index's select
	// ever runs. The ctx.Err() check at the top of Index must reject the
	// call before that happens, so an already-dead caller never causes a
	// real OpenSearch write.
	b := NewBatchIndexer(fake, logger, 1, time.Hour, 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := b.Index(ctx, "test-index", "doc-1", strings.NewReader(`{}`))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, fake.callCount(), "an already-canceled ctx must be rejected before it can be claimed by a flush")
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

// TestBatchIndexer_FlushLinksBackToCallerSpans verifies that flushBatch's
// span links back to each batched caller's span context, so a flush that
// coalesces several independently-traced callers remains discoverable from
// each of them despite detaching to context.Background() for the actual
// bulk request.
func TestBatchIndexer_FlushLinksBackToCallerSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(prevTP)

	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	b := NewBatchIndexer(fake, logger, 2, time.Hour, 5*time.Second)

	callerTracer := otel.Tracer("test")
	ctx1, span1 := callerTracer.Start(context.Background(), "caller-1")
	ctx2, span2 := callerTracer.Start(context.Background(), "caller-2")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer span1.End()
		_ = b.Index(ctx1, "test-index", "doc-1", strings.NewReader(`{}`))
	}()
	go func() {
		defer wg.Done()
		defer span2.End()
		_ = b.Index(ctx2, "test-index", "doc-2", strings.NewReader(`{}`))
	}()
	wg.Wait()

	require.Equal(t, 1, fake.callCount())
	require.NoError(t, tp.ForceFlush(context.Background()))

	var flushSpan *tracetest.SpanStub
	for i, s := range exporter.GetSpans() {
		if s.Name == "batch_indexer.flush" {
			flushSpan = &exporter.GetSpans()[i]
			break
		}
	}
	require.NotNil(t, flushSpan, "expected a batch_indexer.flush span")
	assert.Len(t, flushSpan.Links, 2, "flush span should link back to both batched callers' spans")

	linkedIDs := map[string]bool{}
	for _, link := range flushSpan.Links {
		linkedIDs[link.SpanContext.SpanID().String()] = true
	}
	assert.True(t, linkedIDs[span1.SpanContext().SpanID().String()], "flush span should link to caller-1's span")
	assert.True(t, linkedIDs[span2.SpanContext().SpanID().String()], "flush span should link to caller-2's span")
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
