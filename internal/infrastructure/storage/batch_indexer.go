// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

// tracer is safe to initialize at package level — otel.Tracer() returns a
// delegating tracer that forwards to whatever TracerProvider is registered at
// call time, so otel.SetTracerProvider() updates it regardless of init order.
var tracer = otel.Tracer("github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/storage")

// bulkIndexer is the subset of contracts.StorageRepository that BatchIndexer needs.
type bulkIndexer interface {
	BulkIndex(ctx context.Context, operations []contracts.BulkOperation) ([]error, error)
}

// pendingIndexOp is a single caller's queued index request, waiting on the
// result of whichever bulk flush it ends up being batched into. Callers
// always hold this by pointer so it can be located and removed from the
// pending queue by identity if its own ctx is cancelled before dispatch.
type pendingIndexOp struct {
	op           contracts.BulkOperation
	result       chan error
	needsRefresh bool // this op's own caller is waiting on refresh=wait_for; used to recompute BatchIndexer.needsRefresh if this op is canceled before dispatch
	// spanCtx is this op's caller's span context, captured while the caller's
	// own ctx is still live (flushBatch detaches to context.Background() and
	// would otherwise lose it). Used to link the flush's bulk-request span
	// back to every op batched into it.
	spanCtx trace.SpanContext
}

// BatchIndexer coalesces individual Index calls into OpenSearch bulk
// requests, so concurrent single-document indexing operations reuse one
// OpenSearch round trip instead of paying for one each. A batch flushes
// as soon as either maxBatchSize operations have queued, or maxWait has
// elapsed since the first operation in the batch queued — whichever comes
// first. Each caller blocks on Index until its own operation's result comes
// back from that flush.
type BatchIndexer struct {
	repo         bulkIndexer
	logger       *slog.Logger
	maxBatchSize int
	maxWait      time.Duration
	flushTimeout time.Duration

	mu           sync.Mutex
	pending      []*pendingIndexOp
	timer        *time.Timer
	generation   uint64 // bumped each time a new batch's timer is scheduled; lets a stale AfterFunc callback recognize its batch was already claimed elsewhere and skip draining whatever batch is current when it finally runs
	needsRefresh bool   // set if any queued op's caller is waiting on refresh=wait_for
}

// NewBatchIndexer creates a BatchIndexer wrapping repo. maxBatchSize and
// maxWait must both be positive. flushTimeout bounds each bulk request
// flush issues against repo, since a flush is detached from any single
// caller's context (it can be batching operations from several callers
// at once) and so cannot inherit a deadline from them.
func NewBatchIndexer(repo bulkIndexer, logger *slog.Logger, maxBatchSize int, maxWait time.Duration, flushTimeout time.Duration) *BatchIndexer {
	return &BatchIndexer{
		repo:         repo,
		logger:       logging.WithComponent(logger, "batch_indexer"),
		maxBatchSize: maxBatchSize,
		maxWait:      maxWait,
		flushTimeout: flushTimeout,
	}
}

// Index queues a single document for indexing as part of the next bulk
// flush and blocks until that flush completes, returning this operation's
// individual result. Matches the signature of contracts.StorageRepository.Index
// so it can be used as a drop-in replacement at call sites that index one
// document at a time.
func (b *BatchIndexer) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	// Reject an already-canceled ctx before it can ever be claimed by a
	// flush. Once enqueue claims an op (e.g. it fills maxBatchSize), the
	// write is dispatched and can no longer be abandoned safely — see the
	// comment below. Checking here, rather than only in the select below,
	// closes that window for a caller whose ctx died before Index was ever
	// called.
	if err := ctx.Err(); err != nil {
		return err
	}

	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	// logging.NeedsRefreshWaitFor must be read from the caller's own ctx here,
	// before flush replaces it with its own detached context.
	needsRefresh := logging.NeedsRefreshWaitFor(ctx)

	op := &pendingIndexOp{
		op: contracts.BulkOperation{
			Index:  index,
			DocID:  docID,
			Action: "index",
			Body:   bytes.NewReader(bodyBytes),
		},
		result:       make(chan error, 1),
		needsRefresh: needsRefresh,
		spanCtx:      trace.SpanContextFromContext(ctx),
	}
	b.enqueue(op, needsRefresh)

	select {
	case err := <-op.result:
		return err
	case <-ctx.Done():
		if b.removeIfPending(op) {
			// op was still queued and hadn't been claimed by any flush yet,
			// so no write for it has been dispatched — safe to abandon.
			return ctx.Err()
		}
		// op has already been claimed by a flush (size- or timer-triggered)
		// and its bulk request may already be in flight. Abandoning it here
		// would let a write that goes on to succeed be reported as a
		// ctx-cancellation failure, and ProcessTransaction would then skip
		// publishIndexingEvent for a document that was actually committed.
		// Wait for the real result instead — flushTimeout bounds how long
		// that dispatched request can take, so this cannot hang forever.
		return <-op.result
	}
}

// enqueue adds op to the current batch, starting the flush timer if this is
// the first operation in a new batch. If op fills the batch to maxBatchSize,
// enqueue claims the batch itself — atomically, under the same lock
// acquisition that observed the batch was full — and dispatches it on its
// own goroutine (same as a timer-triggered flush, not the goroutine of
// whichever caller happened to fill the batch). Claiming the batch here
// rather than merely signaling "go flush" prevents a race where multiple
// concurrent enqueue calls each observe a full batch and each schedule a
// flush goroutine: since none of those goroutines would otherwise claim
// their batch until they actually run, a late one can drain whatever batch
// happens to be pending by then — including a brand new one that hasn't
// reached maxBatchSize yet — flushing it prematurely and defeating both the
// size bound and the wait-based coalescing. Flushing on a separate goroutine
// (rather than inline here) still lets every caller's Index race its own
// ctx.Done() against the flush the same way, rather than the batch-filling
// caller being blocked on the round trip regardless of its own context's
// deadline. needsRefresh marks the whole batch as needing refresh=wait_for
// if this op's caller does, since the underlying bulk request's refresh mode
// is per-request, not per-item.
func (b *BatchIndexer) enqueue(op *pendingIndexOp, needsRefresh bool) {
	b.mu.Lock()
	b.pending = append(b.pending, op)
	if needsRefresh {
		b.needsRefresh = true
	}

	if len(b.pending) == 1 {
		// Starting a new batch: bump the generation and capture it in the
		// timer callback's closure. If this timer's callback is still
		// blocked on b.mu when a later enqueue starts the *next* batch (and
		// so bumps the generation again), the callback will see a stale
		// generation once it finally acquires the lock and must not drain
		// that newer batch — see flush.
		b.generation++
		gen := b.generation
		b.timer = time.AfterFunc(b.maxWait, func() { b.flush(gen) })
	}

	var batch []*pendingIndexOp
	var batchNeedsRefresh bool
	if len(b.pending) >= b.maxBatchSize {
		batch, batchNeedsRefresh = b.drainLocked()
	}
	b.mu.Unlock()

	if batch != nil {
		go b.flushBatch(batch, batchNeedsRefresh)
	}
}

// removeIfPending removes op from the current batch if it hasn't been
// claimed by a flush yet, reporting whether it did so. If op is no longer
// present, a flush (size- or timer-triggered) has already claimed the batch
// it was part of, and its result must be awaited instead of the op removed.
// b.needsRefresh is recomputed from what remains: if the canceled op was the
// only one whose caller wanted refresh=wait_for, the rest of the batch (and
// any batch started after this one empties) should not pay for it.
func (b *BatchIndexer) removeIfPending(op *pendingIndexOp) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, p := range b.pending {
		if p == op {
			b.pending = append(b.pending[:i], b.pending[i+1:]...)
			b.needsRefresh = false
			for _, remaining := range b.pending {
				if remaining.needsRefresh {
					b.needsRefresh = true
					break
				}
			}
			return true
		}
	}
	return false
}

// drainLocked stops any pending flush timer and takes ownership of the
// current batch, resetting BatchIndexer's state for the next one. b.mu must
// be held by the caller.
func (b *BatchIndexer) drainLocked() ([]*pendingIndexOp, bool) {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	batch := b.pending
	needsRefresh := b.needsRefresh
	b.pending = nil
	b.needsRefresh = false
	return batch, needsRefresh
}

// flush is the maxWait timer's callback for the batch generation captured
// as gen at schedule time. time.Timer.Stop does not guarantee that an
// AfterFunc callback hasn't already started running — if this callback was
// already past Stop's reach and is only now acquiring b.mu, a size-triggered
// drain (see enqueue) and a subsequent enqueue may already have claimed this
// batch and started a new one. Checking gen against the current generation
// under the lock detects that: if a new batch has since started, gen is
// stale and this callback must not drain it, or it would flush a batch that
// hasn't waited out its own maxWait yet, defeating maxWait coalescing.
func (b *BatchIndexer) flush(gen uint64) {
	b.mu.Lock()
	if gen != b.generation {
		b.mu.Unlock()
		return
	}
	batch, needsRefresh := b.drainLocked()
	b.mu.Unlock()
	b.flushBatch(batch, needsRefresh)
}

// flushBatch sends batch to OpenSearch as a single bulk request and delivers
// each operation's individual result back to its caller.
func (b *BatchIndexer) flushBatch(batch []*pendingIndexOp, needsRefresh bool) {
	if len(batch) == 0 {
		return
	}

	ops := make([]contracts.BulkOperation, len(batch))
	links := make([]trace.Link, 0, len(batch))
	for i, p := range batch {
		ops[i] = p.op
		if p.spanCtx.IsValid() {
			links = append(links, trace.Link{SpanContext: p.spanCtx})
		}
	}

	// ctx is detached from any single caller (context.Background()), so this
	// span cannot be a child of any one op's span. Linking every queued op's
	// spanCtx instead keeps the bulk request traceable back to each of the
	// messages that contributed to it, without re-coupling this flush's
	// lifetime to any single caller's cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), b.flushTimeout)
	defer cancel()
	ctx, span := tracer.Start(ctx, "batch_indexer.flush",
		trace.WithLinks(links...),
		trace.WithAttributes(attribute.Int("batch_size", len(batch))),
	)
	defer span.End()
	if needsRefresh {
		ctx = logging.WithRefreshWaitFor(ctx)
	}

	itemErrors, err := b.repo.BulkIndex(ctx, ops)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		b.logger.Error("Batch flush failed", "error", err.Error(), "batch_size", len(batch))
		for _, p := range batch {
			p.result <- err
		}
		return
	}
	span.SetStatus(codes.Ok, "")

	for i, p := range batch {
		// A BulkIndex implementation returning a shorter (or nil) itemErrors
		// slice than ops is a contract violation; treat the missing entries
		// as failures rather than defaulting to success, so a broken
		// implementation can't make ProcessTransaction publish events for
		// documents that were never confirmed written.
		var itemErr error
		if i < len(itemErrors) {
			itemErr = itemErrors[i]
		} else {
			itemErr = fmt.Errorf("%s: bulk index response missing result for item %d", constants.ErrBulkOperation, i)
		}
		p.result <- itemErr
	}
}
