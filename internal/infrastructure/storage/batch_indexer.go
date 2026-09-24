// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

// bulkIndexer is the subset of contracts.StorageRepository that BatchIndexer needs.
type bulkIndexer interface {
	BulkIndex(ctx context.Context, operations []contracts.BulkOperation) ([]error, error)
}

// pendingIndexOp is a single caller's queued index request, waiting on the
// result of whichever bulk flush it ends up being batched into.
type pendingIndexOp struct {
	op     contracts.BulkOperation
	result chan error
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

	mu      sync.Mutex
	pending []pendingIndexOp
	timer   *time.Timer
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
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	resultCh := make(chan error, 1)
	b.enqueue(pendingIndexOp{
		op: contracts.BulkOperation{
			Index:  index,
			DocID:  docID,
			Action: "index",
			Body:   bytes.NewReader(bodyBytes),
		},
		result: resultCh,
	})

	select {
	case err := <-resultCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// enqueue adds op to the current batch, starting the flush timer if this is
// the first operation in a new batch, and flushing immediately (off the
// caller's goroutine) if the batch is now full.
func (b *BatchIndexer) enqueue(op pendingIndexOp) {
	b.mu.Lock()
	b.pending = append(b.pending, op)

	if len(b.pending) == 1 {
		b.timer = time.AfterFunc(b.maxWait, b.flush)
	}

	full := len(b.pending) >= b.maxBatchSize
	b.mu.Unlock()

	if full {
		b.flush()
	}
}

// flush sends the current batch to OpenSearch as a single bulk request and
// delivers each operation's individual result back to its caller. Safe to
// call concurrently (from the timer and from enqueue on a full batch) —
// only the goroutine that actually drains a non-empty batch does the work.
func (b *BatchIndexer) flush() {
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	batch := b.pending
	b.pending = nil
	b.mu.Unlock()

	if len(batch) == 0 {
		return
	}

	ops := make([]contracts.BulkOperation, len(batch))
	for i, p := range batch {
		ops[i] = p.op
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.flushTimeout)
	defer cancel()

	itemErrors, err := b.repo.BulkIndex(ctx, ops)
	if err != nil {
		b.logger.Error("Batch flush failed", "error", err.Error(), "batch_size", len(batch))
		for _, p := range batch {
			p.result <- err
		}
		return
	}

	for i, p := range batch {
		var itemErr error
		if i < len(itemErrors) {
			itemErr = itemErrors[i]
		}
		p.result <- itemErr
	}
}
