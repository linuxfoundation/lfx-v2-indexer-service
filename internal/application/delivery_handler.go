// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
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
	// arrived starts the request timeout, so time spent queued behind
	// earlier requests counts against the producer's wait.
	arrived time.Time
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
// never overlaps this one even after a timeout reply. A request whose
// timeout ran out while queued replies transient without writing: its
// producer has already given up and may have sent a newer request.
func (h *DeliveryHandler) Handle(ctx context.Context, req *deliveryRequest, reply func([]byte) error) {
	start := req.arrived
	if start.IsZero() {
		start = time.Now()
	}
	ctx, cancel := context.WithDeadline(ctx, start.Add(h.timeout))
	defer cancel()
	if ctx.Err() != nil {
		slot := halfFGA
		if req.indexBody != nil {
			slot = halfIndex
		}
		h.respond(ctx, req.subject, transient(slot, "timeout"), reply)
		return
	}

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
	// A successful Apply invalidates even when it changed nothing: it may be
	// the replay of a request whose tuples landed but whose invalidation
	// failed, and that replay must not reply OK over a stale cache. A failed
	// Apply invalidates only if an earlier batch landed. Only a clean write
	// seeds.
	if h.cache == nil || (err != nil && !result.Changed) {
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
// answered from an entry cached before this request is served again.
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
		logger.Error("FGA cache invalidation failed after an FGA write", "object_type", req.objectType, "error", err.Error())
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
	Action         string          `json:"action"`
	Data           json.RawMessage `json:"data"`
	IndexingConfig *struct {
		ObjectID string `json:"object_id"`
	} `json:"indexing_config"`
	FGA  json.RawMessage `json:"fga"`
	Sync bool            `json:"sync"`
}

func (f deliveryFields) isDelete() bool {
	return f.Action == string(constants.ActionDeleted)
}

// deletedUID returns the UID of a delete whose data is the bare UID string.
// The tombstone is written under that UID even when indexing_config names
// another id, so a delete is ordered by it.
func (f deliveryFields) deletedUID() string {
	if !f.isDelete() {
		return ""
	}
	var uid string
	if err := json.Unmarshal(f.Data, &uid); err != nil {
		return ""
	}
	return uid
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
	// The index body is re-encoded from fields, which keeps only the last of
	// a repeated key; the audit decoder must still see that ambiguity.
	// encoding/json matches field names with strings.EqualFold, so keys equal
	// under that folding are repeats too. The index half reads indexing_config
	// by exact key while the ordering key comes from that folding, so it must
	// not hold repeats either.
	if hasDuplicateKey(data) || hasDuplicateKey(foldedField(fields, "indexing_config")) {
		return nil, rejected(halfIndex, "invalid request JSON: duplicate key")
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
	// Every delete and every request carrying resource data is index work, so
	// the index half rejects a malformed one (such as an update missing
	// indexing_config) instead of the request replying OK after only the FGA
	// half.
	if envelope.IndexingConfig != nil || envelope.isDelete() || present(envelope.Data) {
		for key := range fields {
			if strings.EqualFold(key, "fga") || strings.EqualFold(key, "sync") {
				delete(fields, key)
			}
		}
		body, err := json.Marshal(fields)
		if err != nil {
			return nil, rejected(halfIndex, "invalid request JSON")
		}
		req.indexBody = body
	}
	return req, nil
}

// objectID returns the UID of a delete, indexing_config.object_id, or
// fga.data.uid when neither is present.
func objectID(envelope deliveryFields, fga json.RawMessage) (string, *deliveryFailure) {
	switch {
	case envelope.deletedUID() != "":
		return envelope.deletedUID(), nil
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

func hasDuplicateKey(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		key, _ := token.(string)
		folded := foldKey(key)
		if seen[folded] {
			return true
		}
		seen[folded] = true
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			return false
		}
	}
	return false
}

// foldedField returns the value of the key equal to name under
// strings.EqualFold; the duplicate check leaves at most one.
func foldedField(fields map[string]json.RawMessage, name string) json.RawMessage {
	for key, value := range fields {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return nil
}

// foldKey maps each rune to the lowest rune of its simple case-folding orbit,
// so two keys have the same foldKey exactly when strings.EqualFold holds.
func foldKey(s string) string {
	return strings.Map(func(r rune) rune {
		lowest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			lowest = min(lowest, f)
		}
		return lowest
	}, s)
}

func present(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}
