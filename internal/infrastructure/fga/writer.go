// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// Writer applies FGA operations to OpenFGA.
type Writer struct {
	store    tupleStore
	lease    Lease
	disabled error
	logger   *slog.Logger

	stop context.CancelFunc
	done chan struct{}
}

var _ contracts.FGAWriter = (*Writer)(nil)

// New builds a Writer for cfg that holds lease on each object while it
// writes. It never fails: without an API URL, or with a setting the SDK
// refuses, Apply returns contracts.ErrFGANotConfigured. When no store id is
// configured the store is looked up by name in the background until found,
// and Apply returns ErrStoreNotResolved until then.
func New(cfg Config, lease Lease, logger *slog.Logger) *Writer {
	logger = logging.WithComponent(logger, "fga_writer")
	w := &Writer{lease: lease, logger: logger, stop: func() {}, done: make(chan struct{})}
	if !cfg.Enabled() {
		w.disabled = contracts.ErrFGANotConfigured
		close(w.done)
		return w
	}
	c, err := newSDKClient(cfg.APIURL, cfg.AuthModelID)
	if err != nil {
		logger.Warn("FGA writer turned off by an invalid setting", "error", err.Error())
		w.disabled = fmt.Errorf("%w: %w", contracts.ErrFGANotConfigured, err)
		close(w.done)
		return w
	}
	w.store = tupleStore{client: c, logger: logger}
	if cfg.StoreID != "" {
		c.storeID.Store(&cfg.StoreID)
		close(w.done)
		return w
	}
	ctx, stop := context.WithCancel(context.Background())
	w.stop = stop
	go w.resolveStore(ctx, c, cfg.StoreName, cfg.lookupRetryInterval())
	return w
}

// Close stops the background store lookup.
func (w *Writer) Close() {
	w.stop()
	<-w.done
}

func (w *Writer) resolveStore(ctx context.Context, c *sdkClient, name string, interval time.Duration) {
	defer close(w.done)
	for {
		id, err := c.findStore(ctx, name)
		if err == nil {
			c.storeID.Store(&id)
			w.logger.Info("OpenFGA store found", "store_name", name, "store_id", id)
			return
		}
		w.logger.Warn("OpenFGA store lookup failed, retrying", "store_name", name, "error", err.Error())
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Apply applies one fga-sync generic access operation. A payload fga-sync
// would terminate as invalid returns *contracts.FGARejectedError; any other
// error is transient.
func (w *Writer) Apply(ctx context.Context, objectType string, operation json.RawMessage) (contracts.FGAWriteResult, error) {
	if w.disabled != nil {
		return contracts.FGAWriteResult{}, contracts.ErrFGANotConfigured
	}
	op, err := decodeOperation(operation)
	if err != nil {
		return contracts.FGAWriteResult{}, err
	}

	result, err := w.applyLeased(ctx, op)
	if err != nil {
		attrs := append([]any{"subject_object_type", objectType, "object_type", op.ObjectType,
			"operation", op.Operation, "changed", result.Changed}, errorAttrs(err)...)
		w.logger.ErrorContext(ctx, "FGA operation failed", attrs...)
		return result, replyError(err)
	}
	return result, nil
}

// leasedRun does an operation's tuple reads and writes while its object is
// leased.
type leasedRun func(ctx context.Context) (contracts.FGAWriteResult, error)

// applyLeased validates op before taking the lease on its object, so an
// invalid payload is rejected even when the lease is busy or unavailable,
// then runs its tuple I/O while holding the lease.
func (w *Writer) applyLeased(ctx context.Context, op types.FGAOperation) (contracts.FGAWriteResult, error) {
	object, run, err := w.prepare(op)
	if err != nil {
		return contracts.FGAWriteResult{}, err
	}
	ctx, release, err := w.lease.Acquire(ctx, object)
	if err != nil {
		return contracts.FGAWriteResult{}, err
	}
	defer release()
	return run(ctx)
}

// prepare decodes and validates op without I/O. It returns the object op
// writes and the run that writes it.
func (w *Writer) prepare(op types.FGAOperation) (string, leasedRun, error) {
	switch op.Operation {
	case types.FGAOperationUpdateAccess:
		return w.updateAccess(op)
	case types.FGAOperationDeleteAccess:
		return w.deleteAccess(op)
	case types.FGAOperationMemberPut:
		return w.memberPut(op)
	case types.FGAOperationMemberRemove:
		return w.memberRemove(op)
	default:
		return "", nil, rejected(errors.New("unsupported operation"))
	}
}

// errOpenFGAFailed replaces OpenFGA and transport errors in the reply: they
// can echo the store id, the API URL or tuple users to the producer.
var errOpenFGAFailed = errors.New("openfga request failed")

// replyError keeps the errors whose text is fixed by this package and
// replaces every other one with errOpenFGAFailed.
func replyError(err error) error {
	switch {
	case errors.As(err, new(*contracts.FGARejectedError)), errors.Is(err, ErrStoreNotResolved):
		return err
	case errors.Is(err, errLeaseBusy):
		return errLeaseBusy
	case errors.Is(err, errLeaseUnavailable):
		return errLeaseUnavailable
	}
	return errOpenFGAFailed
}

// errorAttrs describes err for a log line without its OpenFGA text, which
// can hold usernames from tuples. Lease errors come from NATS and keep
// their cause.
func errorAttrs(err error) []any {
	text := replyError(err).Error()
	if errors.Is(err, errLeaseUnavailable) {
		text = err.Error()
	}
	attrs := []any{"error", text}
	var status interface{ ResponseStatusCode() int }
	if errors.As(err, &status) {
		attrs = append(attrs, "http_status", status.ResponseStatusCode())
	}
	var request interface{ RequestId() string }
	if errors.As(err, &request) && request.RequestId() != "" {
		attrs = append(attrs, "request_id", request.RequestId())
	}
	return attrs
}

func rejected(err error) error {
	return &contracts.FGARejectedError{Cause: err}
}

func decodeOperation(raw json.RawMessage) (types.FGAOperation, error) {
	var op types.FGAOperation
	if err := json.Unmarshal(raw, &op); err != nil {
		return types.FGAOperation{}, rejected(err)
	}
	if op.ObjectType == "" {
		return types.FGAOperation{}, rejected(errors.New("object_type is required"))
	}
	return op, nil
}

func decodeData[T any](op types.FGAOperation) (*T, error) {
	data := new(T)
	if err := op.UnmarshalData(data); err != nil {
		return nil, rejected(err)
	}
	return data, nil
}

func buildObjectID(objectType, uid string) string {
	return objectType + ":" + uid
}
