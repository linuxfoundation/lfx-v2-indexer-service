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
)

// fakeBulkIndexer is a test double for bulkIndexer that records every call
// and lets tests control the returned itemErrors/err per call.
type fakeBulkIndexer struct {
	mu      sync.Mutex
	calls   [][]contracts.BulkOperation
	ctxErrs []error // ctx.Err() captured at call time, before flush's defer cancel() fires

	itemErrorsFn func(ops []contracts.BulkOperation) []error
	err          error
}

func (f *fakeBulkIndexer) BulkIndex(ctx context.Context, operations []contracts.BulkOperation) ([]error, error) {
	f.mu.Lock()
	f.calls = append(f.calls, operations)
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	f.mu.Unlock()

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
}

func TestBatchIndexer_FlushUsesTimeoutNotCallerContext(t *testing.T) {
	logger := setupTestLogger(t)
	fake := &fakeBulkIndexer{}
	b := NewBatchIndexer(fake, logger, 1, time.Hour, 5*time.Second)

	// The caller's own context is already canceled, but the batch still
	// flushes and succeeds because the flush uses its own bounded timeout
	// rather than inheriting from any single caller.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = b.Index(ctx, "test-index", "doc-1", strings.NewReader(`{}`))

	require.Equal(t, 1, fake.callCount())
	assert.NoError(t, fake.ctxErrs[0], "flush's context should not be the caller's canceled context")
}
