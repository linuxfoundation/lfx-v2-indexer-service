// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

// cacheInvalidationTimeout bounds the cache invalidation and seed after a
// tuple change, as fga-sync bounds its own.
const cacheInvalidationTimeout = 5 * time.Second

// indexProcessor is the existing index path (current and audited writes).
type indexProcessor interface {
	ProcessIndexingMessage(ctx context.Context, data []byte, subject string) error
}

// deliveryRequest is one parsed lfx.indexer.index.* request.
type deliveryRequest struct {
	subject    string
	objectType string
	// objectRef is the ordering key: <object_type>:<id>.
	objectRef string
	// indexBody is the envelope without fga and sync, or nil when the
	// request has no indexing_config.
	indexBody []byte
	fga       json.RawMessage
	sync      bool
}

// DeliveryHandler applies the index and FGA halves of one request and
// replies OK only when every half present has landed.
type DeliveryHandler struct {
	index   indexProcessor
	fga     contracts.FGAWriter
	cache   contracts.FGACache
	timeout time.Duration
	metrics *deliveryMetrics
	logger  *slog.Logger
}

// NewDeliveryHandler builds the handler. A nil cache means the indexer has no
// FGA cache backend, so there is nothing to invalidate after a tuple change.
func NewDeliveryHandler(index indexProcessor, fga contracts.FGAWriter, cache contracts.FGACache, timeout time.Duration, logger *slog.Logger) *DeliveryHandler {
	return &DeliveryHandler{
		index:   index,
		fga:     fga,
		cache:   cache,
		timeout: timeout,
		metrics: defaultDeliveryMetrics(),
		logger:  logging.WithComponent(logger, "delivery_handler"),
	}
}

// Handle runs the request's halves concurrently, replies, and returns only
// after both halves have stopped, so the next request for the same object
// never overlaps this one even after a timeout reply.
func (h *DeliveryHandler) Handle(ctx context.Context, req *deliveryRequest, reply func([]byte) error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	failure, wait := h.run(ctx, req)
	h.respond(ctx, req.subject, failure, reply)
	h.metrics.elapsed(ctx, halfTotal, time.Since(start))
	wait()
}

// Reject replies to a request that could not be parsed or keyed.
func (h *DeliveryHandler) Reject(ctx context.Context, subject string, failure *deliveryFailure, reply func([]byte) error) {
	h.respond(ctx, subject, failure, reply)
}

type halfResult struct {
	slot    string
	failure *deliveryFailure
}

// run starts each half present and waits for both or the timeout. Neither
// half cancels the other, so a replay finds both as far along as possible.
func (h *DeliveryHandler) run(ctx context.Context, req *deliveryRequest) (*deliveryFailure, func()) {
	results := make(chan halfResult, 2)
	running := map[string]bool{}
	if req.indexBody != nil {
		running[halfIndex] = true
		go func() { results <- halfResult{halfIndex, h.runIndex(ctx, req)} }()
	}
	if req.fga != nil {
		running[halfFGA] = true
		go func() { results <- halfResult{halfFGA, h.runFGA(ctx, req)} }()
	}

	var failures []*deliveryFailure
	for len(running) > 0 {
		select {
		case r := <-results:
			delete(running, r.slot)
			failures = append(failures, r.failure)
		case <-ctx.Done():
			slot := halfFGA
			if running[halfIndex] {
				slot = halfIndex
			}
			pending := len(running)
			wait := func() {
				for range pending {
					<-results
				}
			}
			return selectFailure(append(failures, transient(slot, "timeout"))...), wait
		}
	}
	return selectFailure(failures...), func() {}
}

func (h *DeliveryHandler) runIndex(ctx context.Context, req *deliveryRequest) *deliveryFailure {
	start := time.Now()
	defer func() { h.metrics.elapsed(ctx, halfIndex, time.Since(start)) }()
	if req.sync {
		ctx = logging.WithRefreshWaitFor(ctx)
	}
	if err := h.index.ProcessIndexingMessage(ctx, req.indexBody, req.subject); err != nil {
		return classifyIndexError(err)
	}
	return nil
}

func (h *DeliveryHandler) runFGA(ctx context.Context, req *deliveryRequest) *deliveryFailure {
	start := time.Now()
	defer func() { h.metrics.elapsed(ctx, halfFGA, time.Since(start)) }()
	result, err := h.fga.Apply(ctx, req.objectType, req.fga)
	var failure *deliveryFailure
	if err != nil {
		failure = classifyFGAError(err)
	}
	// A failed Apply can still report Changed: an earlier batch may have
	// landed, so the cache is invalidated either way. Only a clean write seeds.
	if !result.Changed || h.cache == nil {
		return failure
	}
	seedKeys := result.SeedKeys
	if err != nil {
		seedKeys = nil
	}
	if invalidation := h.invalidate(ctx, req, seedKeys); failure == nil {
		failure = invalidation
	}
	return failure
}

// invalidate advances the shared cache marker before the reply, so no check
// answered from an entry cached before this tuple change is served again.
// Seeding only adds entries newer than the marker; a failed seed costs a
// cache miss, never a wrong answer.
func (h *DeliveryHandler) invalidate(ctx context.Context, req *deliveryRequest, seedKeys []string) *deliveryFailure {
	logger := logging.FromContext(ctx, h.logger)
	// Detached so a request that hit its deadline after tuples landed still
	// invalidates.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheInvalidationTimeout)
	defer cancel()
	marker, err := h.cache.Invalidate(ctx)
	if err != nil {
		logger.Error("FGA cache invalidation failed after a tuple change", "object_type", req.objectType, "error", err.Error())
		return transient(halfFGA, "cache invalidation failed")
	}
	if len(seedKeys) == 0 {
		return nil
	}
	if err := h.cache.Seed(ctx, seedKeys, marker); err != nil {
		logger.Warn("FGA cache seed failed", "object_type", req.objectType, "keys", len(seedKeys), "error", err.Error())
	}
	return nil
}

func (h *DeliveryHandler) respond(ctx context.Context, subject string, failure *deliveryFailure, reply func([]byte) error) {
	logger := logging.FromContext(ctx, h.logger)
	h.metrics.result(ctx, failure)
	body := []byte(replyOK)
	if failure != nil {
		body = failure.reply()
		logger.Warn("Index request failed", "subject", subject, "class", failure.class, "half", failure.half, "error", failure.text)
	}
	if reply == nil {
		return
	}
	if err := reply(body); err != nil {
		logger.Error("Failed to send index request reply", "subject", subject, "error", err.Error())
	}
}

// deliveryFields are the parts of a types.IndexRequest the handler reads.
// The rest of the envelope is left to the index half, which keeps its own
// tolerance for loosely typed fields such as headers.
type deliveryFields struct {
	IndexingConfig *struct {
		ObjectID string `json:"object_id"`
	} `json:"indexing_config"`
	FGA  json.RawMessage `json:"fga"`
	Sync bool            `json:"sync"`
}

// parseDeliveryRequest decodes a request and derives its ordering key. The
// index half receives the body without fga and sync, because the version 2
// decoder rejects unknown fields.
func parseDeliveryRequest(data []byte, subject string) (*deliveryRequest, *deliveryFailure) {
	objectType, ok := ObjectTypeFromSubject(subject)
	if !ok || objectType == "" {
		return nil, rejected(halfIndex, "invalid subject")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, rejected(halfIndex, "invalid request JSON")
	}
	var envelope deliveryFields
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, rejected(halfIndex, "invalid request: "+err.Error())
	}
	req := &deliveryRequest{subject: subject, objectType: objectType, sync: envelope.Sync}
	if present(envelope.FGA) {
		req.fga = envelope.FGA
	}
	id, failure := objectID(envelope, req.fga)
	if failure != nil {
		return nil, failure
	}
	req.objectRef = objectType + ":" + id
	if envelope.IndexingConfig != nil {
		delete(fields, "fga")
		delete(fields, "sync")
		body, err := json.Marshal(fields)
		if err != nil {
			return nil, rejected(halfIndex, "invalid request JSON")
		}
		req.indexBody = body
	}
	return req, nil
}

// objectID returns indexing_config.object_id, or fga.data.uid when the
// request has no indexing_config.
func objectID(envelope deliveryFields, fga json.RawMessage) (string, *deliveryFailure) {
	switch {
	case envelope.IndexingConfig != nil:
		if envelope.IndexingConfig.ObjectID == "" {
			return "", rejected(halfIndex, "indexing_config.object_id is required")
		}
		return envelope.IndexingConfig.ObjectID, nil
	case fga != nil:
		var operation struct {
			Data struct {
				UID string `json:"uid"`
			} `json:"data"`
		}
		if err := json.Unmarshal(fga, &operation); err != nil || operation.Data.UID == "" {
			return "", rejected(halfFGA, "fga.data.uid is required")
		}
		return operation.Data.UID, nil
	default:
		return "", rejected(halfIndex, "neither indexing_config nor fga is present")
	}
}

func present(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}
