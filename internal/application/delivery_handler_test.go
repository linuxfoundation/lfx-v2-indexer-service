// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

const deliverySubject = constants.IndexerIndexPrefix + "fixture_resource"

// fakeFGAWriter records operations and answers with a fixed result.
type fakeFGAWriter struct {
	mu      sync.Mutex
	result  contracts.FGAWriteResult
	err     error
	delay   time.Duration
	applied []string
	// during, when set, runs inside Apply, as another writer acting while
	// this write is in flight.
	during func()
}

func (w *fakeFGAWriter) Apply(ctx context.Context, objectType string, op json.RawMessage) (contracts.FGAWriteResult, error) {
	if w.during != nil {
		w.during()
	}
	if w.delay > 0 {
		select {
		case <-time.After(w.delay):
		case <-ctx.Done():
			return contracts.FGAWriteResult{}, ctx.Err()
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.applied = append(w.applied, objectType+" "+string(op))
	return w.result, w.err
}

func (w *fakeFGAWriter) calls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.applied...)
}

// fakeIndex records what the index half received.
type fakeIndex struct {
	mu      sync.Mutex
	err     error
	delay   time.Duration
	bodies  []string
	subject string
	refresh []bool
}

func (f *fakeIndex) ProcessIndexingMessage(ctx context.Context, data []byte, subject string) error {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies = append(f.bodies, string(data))
	f.subject = subject
	f.refresh = append(f.refresh, logging.NeedsRefreshWaitFor(ctx))
	return f.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestDeliveryHandler(index indexProcessor, fga contracts.FGAWriter, cache contracts.FGACache) *DeliveryHandler {
	return NewDeliveryHandler(index, fga, cache, 5*time.Second, discardLogger())
}

// deliver parses and handles one request, returning its reply.
func deliver(t *testing.T, h *DeliveryHandler, subject string, payload []byte) string {
	t.Helper()
	var reply string
	capture := func(body []byte) error {
		reply = string(body)
		return nil
	}
	req, failure := parseDeliveryRequest(payload, subject)
	if failure != nil {
		h.Reject(context.Background(), subject, failure, capture)
		return reply
	}
	h.Handle(context.Background(), req, capture)
	return reply
}

func indexRequest(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	request := map[string]any{
		"action":  "updated",
		"headers": map[string]string{"authorization": "Bearer fixture-token"},
		"data":    map[string]any{"uid": "fixture-1", "name": "Fixture"},
		"indexing_config": map[string]any{
			"object_id":              "fixture-1",
			"access_check_object":    "fixture_resource:fixture-1",
			"access_check_relation":  "viewer",
			"history_check_object":   "fixture_resource:fixture-1",
			"history_check_relation": "auditor",
		},
	}
	if mutate != nil {
		mutate(request)
	}
	payload, err := json.Marshal(request)
	require.NoError(t, err)
	return payload
}

func fgaOperation(uid string) map[string]any {
	return map[string]any{"object_type": "fixture_resource", "operation": "update_access", "data": map[string]any{"uid": uid, "public": false}}
}

func withFGA(request map[string]any) { request["fga"] = fgaOperation("fixture-1") }

func fgaOnly(request map[string]any) {
	delete(request, "indexing_config")
	delete(request, "data")
	request["fga"] = fgaOperation("fixture-1")
}

func TestDeliveryHandler_HalvesPresentReplyOK(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(map[string]any)
		wantIndex int
		wantFGA   int
	}{
		{"index only", nil, 1, 0},
		{"fga only", fgaOnly, 0, 1},
		{"combined", withFGA, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index, writer := &fakeIndex{}, &fakeFGAWriter{}
			h := newTestDeliveryHandler(index, writer, nil)

			require.Equal(t, "OK", deliver(t, h, deliverySubject, indexRequest(t, tt.mutate)))
			require.Len(t, index.bodies, tt.wantIndex)
			require.Len(t, writer.calls(), tt.wantFGA)
		})
	}
}

func TestDeliveryHandler_IndexHalfReceivesEnvelopeWithoutFGAOrSync(t *testing.T) {
	index, writer := &fakeIndex{}, &fakeFGAWriter{}
	h := newTestDeliveryHandler(index, writer, nil)

	payload := indexRequest(t, func(r map[string]any) {
		withFGA(r)
		r["sync"] = true
	})
	require.Equal(t, "OK", deliver(t, h, deliverySubject, payload))

	require.Equal(t, deliverySubject, index.subject)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(index.bodies[0]), &body))
	require.NotContains(t, body, "fga")
	require.NotContains(t, body, "sync")
	require.Contains(t, body, "indexing_config")
	require.Equal(t, []string{`fixture_resource {"data":{"public":false,"uid":"fixture-1"},"object_type":"fixture_resource","operation":"update_access"}`}, writer.calls())
}

func TestDeliveryHandler_SyncControlsRefreshWaitFor(t *testing.T) {
	for _, sync := range []bool{true, false} {
		index := &fakeIndex{}
		h := newTestDeliveryHandler(index, &fakeFGAWriter{}, nil)
		payload := indexRequest(t, func(r map[string]any) {
			if sync {
				r["sync"] = true
			}
		})
		require.Equal(t, "OK", deliver(t, h, deliverySubject, payload))
		require.Equal(t, []bool{sync}, index.refresh, "sync=%v", sync)
	}
}

// The real index path, through the JetStream-path processor, still emits the
// post-indexing events subscribers rely on.
func TestDeliveryHandler_CombinedRequestEmitsPostIndexingEvents(t *testing.T) {
	for _, objectType := range []string{"committee", "committee_member", "committee_settings"} {
		t.Run(objectType, func(t *testing.T) {
			mp, messaging, storage := setupTestMessageProcessor()
			storage.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
			messaging.On("ParsePrincipals", mock.Anything, mock.Anything).Return([]contracts.Principal{{Principal: "fixture-principal"}}, nil)
			messaging.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			h := newTestDeliveryHandler(mp, &fakeFGAWriter{result: contracts.FGAWriteResult{Changed: true}}, nil)

			payload := indexRequest(t, func(r map[string]any) {
				withFGA(r)
				r["action"] = "created"
			})
			require.Equal(t, "OK", deliver(t, h, constants.IndexerIndexPrefix+objectType, payload))
			messaging.AssertCalled(t, "Publish", mock.Anything, "lfx."+objectType+".created", mock.Anything)
		})
	}
}

// The request/reply subject goes through the audit workflow and its
// required-types fence like lfx.index.*: only a mutation adds history.
func TestDeliveryHandler_AuditedRequests(t *testing.T) {
	for _, tt := range []struct {
		origin      types.AuditOrigin
		wantHistory int
	}{{types.AuditOriginMutation, 1}, {types.AuditOriginReindex, 0}} {
		t.Run(string(tt.origin), func(t *testing.T) {
			w, history, current, _ := workflowFixture(t)
			mp := &MessageProcessor{logger: discardLogger()}
			require.NoError(t, mp.SetAuditWorkflow(w, []string{"fixture_resource"}))
			h := newTestDeliveryHandler(mp, &fakeFGAWriter{}, nil)

			payload := auditedRequest(t, tt.origin)
			require.Equal(t, "OK", deliver(t, h, deliverySubject, payload))
			require.Len(t, history.Records, tt.wantHistory)
			require.Equal(t, 1, current.Writes)
		})
	}
}

func TestDeliveryHandler_AuditRequiredTypeRejectsUnauditedRequest(t *testing.T) {
	w, _, current, _ := workflowFixture(t)
	mp := &MessageProcessor{logger: discardLogger()}
	require.NoError(t, mp.SetAuditWorkflow(w, []string{"fixture_resource"}))
	h := newTestDeliveryHandler(mp, &fakeFGAWriter{}, nil)

	reply := deliver(t, h, deliverySubject, indexRequest(t, nil))
	require.Equal(t, "ERROR: rejected: index: audited resource type requires schema_version 2", reply)
	require.Zero(t, current.Writes)
}

func TestDeliveryHandler_AuditHistoryFailureNamesAuditHalf(t *testing.T) {
	w, history, _, _ := workflowFixture(t)
	history.Fail = true
	mp := &MessageProcessor{logger: discardLogger()}
	require.NoError(t, mp.SetAuditWorkflow(w, []string{"fixture_resource"}))
	h := newTestDeliveryHandler(mp, &fakeFGAWriter{}, nil)

	reply := deliver(t, h, deliverySubject, auditedRequest(t, types.AuditOriginMutation))
	require.Regexp(t, `^ERROR: transient: audit: verify audit history: `, reply)
}

func auditedRequest(t *testing.T, origin types.AuditOrigin) []byte {
	t.Helper()
	var envelope types.IndexerMessageEnvelope
	require.NoError(t, json.Unmarshal(workflowEnvelope(constants.ActionUpdated, "delivery-"+string(origin), 2), &envelope))
	envelope.Audit.Origin = origin
	if origin != types.AuditOriginMutation {
		envelope.Audit.Content = nil
	}
	payload, err := json.Marshal(types.IndexRequest{IndexerMessageEnvelope: envelope, Sync: true})
	require.NoError(t, err)
	return payload
}

func TestDeliveryHandler_FailedHalfIsNeverOK(t *testing.T) {
	tests := []struct {
		name  string
		index error
		fga   error
		want  string
	}{
		{"fga transient, index succeeds", nil, errors.New("openfga unavailable"), "ERROR: transient: fga: openfga unavailable"},
		{"fga not configured", nil, contracts.ErrFGANotConfigured, "ERROR: transient: fga: fga writer is not configured"},
		{"fga rejected", nil, &contracts.FGARejectedError{Cause: errors.New("uid is required")}, "ERROR: rejected: fga: uid is required"},
		{"index transient, fga succeeds", errors.New("opensearch unavailable"), nil, "ERROR: transient: index: opensearch unavailable"},
		{"rejected fga wins over transient index", errors.New("opensearch unavailable"), &contracts.FGARejectedError{Cause: errors.New("unknown operation")}, "ERROR: rejected: fga: unknown operation"},
		{"index wins between transients", errors.New("opensearch unavailable"), errors.New("openfga unavailable"), "ERROR: transient: index: opensearch unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index, writer := &fakeIndex{err: tt.index}, &fakeFGAWriter{err: tt.fga}
			h := newTestDeliveryHandler(index, writer, nil)
			require.Equal(t, tt.want, deliver(t, h, deliverySubject, indexRequest(t, withFGA)))
			require.Len(t, index.bodies, 1, "a failed FGA half must not cancel the index half")
			require.Len(t, writer.calls(), 1, "a failed index half must not cancel the FGA half")
		})
	}
}

// A public-to-private change whose FGA half fails must not reply OK, and a
// replay with a working writer converges.
func TestDeliveryHandler_ReplayAfterFGAFailureConverges(t *testing.T) {
	index, writer := &fakeIndex{}, &fakeFGAWriter{err: errors.New("openfga unavailable")}
	h := newTestDeliveryHandler(index, writer, nil)
	payload := indexRequest(t, func(r map[string]any) {
		r["indexing_config"].(map[string]any)["public"] = false
		withFGA(r)
	})

	require.Equal(t, "ERROR: transient: fga: openfga unavailable", deliver(t, h, deliverySubject, payload))
	writer.err = nil
	require.Equal(t, "OK", deliver(t, h, deliverySubject, payload))
	require.Len(t, writer.calls(), 2)
	require.Equal(t, index.bodies[0], index.bodies[1], "a replay must send the same index body")
}

func TestDeliveryHandler_IdenticalReplaysAllReplyOK(t *testing.T) {
	index, writer := &fakeIndex{}, &fakeFGAWriter{}
	h := newTestDeliveryHandler(index, writer, nil)
	payload := indexRequest(t, withFGA)
	for range 10 {
		require.Equal(t, "OK", deliver(t, h, deliverySubject, payload))
	}
	require.Len(t, index.bodies, 10)
	for _, body := range index.bodies {
		require.Equal(t, index.bodies[0], body)
	}
	require.Len(t, writer.calls(), 10)
}

func TestDeliveryHandler_RejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		payload []byte
		want    string
	}{
		{"malformed JSON", deliverySubject, []byte(`{"action":`), "ERROR: rejected: index: invalid request JSON"},
		{"JSON that is not an object", deliverySubject, []byte(`[1]`), "ERROR: rejected: index: invalid request JSON"},
		{"neither block", deliverySubject, []byte(`{"action":"updated","data":{}}`), "ERROR: rejected: index: neither indexing_config nor fga is present"},
		{"null fga and no indexing_config", deliverySubject, []byte(`{"fga":null}`), "ERROR: rejected: index: neither indexing_config nor fga is present"},
		{"index object id missing", deliverySubject, indexRequest(t, func(r map[string]any) { r["indexing_config"].(map[string]any)["object_id"] = "" }), "ERROR: rejected: index: indexing_config.object_id is required"},
		{"fga uid missing", deliverySubject, []byte(`{"fga":{"object_type":"fixture_resource","operation":"delete_access","data":{}}}`), "ERROR: rejected: fga: fga.data.uid is required"},
		{"sync is not a boolean", deliverySubject, indexRequest(t, func(r map[string]any) { r["sync"] = "yes" }), "ERROR: rejected: index: invalid request: json: cannot unmarshal string into Go struct field deliveryFields.sync of type bool"},
		{"no object type", constants.IndexerIndexPrefix, indexRequest(t, nil), "ERROR: rejected: index: invalid subject"},
		{"invalid action", deliverySubject, indexRequest(t, func(r map[string]any) { r["action"] = "" }), "ERROR: rejected: index: failed to create transaction: failed to create V2 transaction: missing or invalid action in message data"},
		{"unknown action", deliverySubject, indexRequest(t, func(r map[string]any) { r["action"] = "archived" }), "ERROR: rejected: index: failed to process transaction: invalid transaction action: invalid V2 transaction action: archived"},
		{"duplicate top-level key", deliverySubject, []byte(`{"action":"updated","action":"deleted","indexing_config":{"object_id":"fixture-1"}}`), "ERROR: rejected: index: invalid request JSON: duplicate key"},
		{"top-level keys differing by case", deliverySubject, []byte(`{"indexing_config":{"object_id":"a"},"Indexing_Config":{"object_id":"b"}}`), "ERROR: rejected: index: invalid request JSON: duplicate key"},
		{"top-level keys equal under Unicode folding", deliverySubject, []byte(`{"action":"updated","sync":false,"ſync":true,"indexing_config":{"object_id":"a"}}`), "ERROR: rejected: index: invalid request JSON: duplicate key"},
		{"indexing_config keys differing by case", deliverySubject, []byte(`{"action":"updated","indexing_config":{"object_id":"a","Object_ID":"b"}}`), "ERROR: rejected: index: invalid request JSON: duplicate key"},
		{"case-variant indexing_config with keys differing by case", deliverySubject, []byte(`{"action":"updated","Indexing_Config":{"object_id":"a","Object_ID":"b"}}`), "ERROR: rejected: index: invalid request JSON: duplicate key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp, _, _ := setupTestMessageProcessor()
			writer := &fakeFGAWriter{}
			h := newTestDeliveryHandler(mp, writer, nil)
			require.Equal(t, tt.want, deliver(t, h, tt.subject, tt.payload))
			require.Empty(t, writer.calls())
		})
	}
}

// Producers send deletes with the bare UID as data and no indexing_config;
// the document delete must still run alongside delete_access.
func TestDeliveryHandler_UIDOnlyDeleteIsIndexWork(t *testing.T) {
	index, writer := &fakeIndex{}, &fakeFGAWriter{}
	h := newTestDeliveryHandler(index, writer, nil)
	payload := []byte(`{"action":"deleted","headers":{"authorization":"Bearer fixture-token"},"data":"fixture-1",` +
		`"fga":{"object_type":"fixture_resource","operation":"delete_access","data":{"uid":"fixture-1"}}}`)

	req, failure := parseDeliveryRequest(payload, deliverySubject)
	require.Nil(t, failure)
	require.Equal(t, "fixture_resource:fixture-1", req.objectRef)

	require.Equal(t, "OK", deliver(t, h, deliverySubject, payload))
	require.Len(t, index.bodies, 1)
	require.NotContains(t, index.bodies[0], "delete_access")
	require.Len(t, writer.calls(), 1)
}

// The tombstone is written under data's UID even when indexing_config names
// another id, so the delete is ordered with other requests for that UID.
func TestDeliveryHandler_DeleteOrderedByDataUID(t *testing.T) {
	payload := []byte(`{"action":"deleted","data":"fixture-a","indexing_config":{"object_id":"fixture-b"}}`)
	req, failure := parseDeliveryRequest(payload, deliverySubject)
	require.Nil(t, failure)
	require.Equal(t, "fixture_resource:fixture-a", req.objectRef)
	require.NotNil(t, req.indexBody)
}

// A delete whose data is not the bare UID still runs the index half, which
// rejects it, so the reply is never OK after only the FGA half.
func TestDeliveryHandler_DeleteWithObjectDataIsRejected(t *testing.T) {
	mp, _, _ := setupTestMessageProcessor()
	h := newTestDeliveryHandler(mp, &fakeFGAWriter{}, nil)
	payload := []byte(`{"action":"deleted","headers":{"authorization":"Bearer fixture-token"},"data":{"uid":"fixture-1"},` +
		`"fga":{"object_type":"fixture_resource","operation":"delete_access","data":{"uid":"fixture-1"}}}`)

	require.Equal(t, "ERROR: rejected: index: failed to process transaction: invalid transaction data: data must be a string (object ID) for deleted actions",
		deliver(t, h, deliverySubject, payload))
}

func TestDeliveryHandler_UpdateWithDataMissingConfigIsRejected(t *testing.T) {
	mp, _, _ := setupTestMessageProcessor()
	h := newTestDeliveryHandler(mp, &fakeFGAWriter{}, nil)
	payload := indexRequest(t, func(request map[string]any) {
		delete(request, "indexing_config")
		request["fga"] = fgaOperation("fixture-1")
	})

	require.Equal(t, `ERROR: rejected: index: failed to process transaction: indexing_config is required for object_type "fixture_resource"`,
		deliver(t, h, deliverySubject, payload))
}

// encoding/json matches field names with Unicode case folding, which is why
// the duplicate check folds keys the same way.
func TestDeliveryHandler_JSONFieldMatchingFoldsCase(t *testing.T) {
	var envelope deliveryFields
	require.NoError(t, json.Unmarshal([]byte(`{"ſync":true}`), &envelope))
	require.True(t, envelope.Sync)
}

func TestDeliveryHandler_IndexBodyDropsFGAAndSyncInAnyCase(t *testing.T) {
	payload := []byte(`{"action":"updated","indexing_config":{"object_id":"fixture-1"},` +
		`"FGA":{"object_type":"fixture_resource","operation":"update_access","data":{"uid":"fixture-1"}},"Sync":true}`)
	req, failure := parseDeliveryRequest(payload, deliverySubject)
	require.Nil(t, failure)
	require.NotNil(t, req.fga)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(req.indexBody, &body))
	require.ElementsMatch(t, []string{"action", "indexing_config"}, slices.Collect(maps.Keys(body)))
}

func TestFoldKeyMatchesEqualFold(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if f := unicode.SimpleFold(r); foldKey(string(r)) != foldKey(string(f)) {
			t.Fatalf("foldKey(%q) != foldKey(%q)", r, f)
		}
	}
	require.NotEqual(t, foldKey("sync"), foldKey("synd"))
	require.Equal(t, foldKey("Object_ID"), foldKey("object_id"))
}

// A request whose timeout ran out while it was queued replies without
// writing: its producer has already given up on it.
func TestDeliveryHandler_ExpiredWhileQueuedDoesNotWrite(t *testing.T) {
	index, writer := &fakeIndex{}, &fakeFGAWriter{}
	h := NewDeliveryHandler(index, writer, nil, 100*time.Millisecond, discardLogger())
	req, failure := parseDeliveryRequest(indexRequest(t, withFGA), deliverySubject)
	require.Nil(t, failure)
	req.arrived = time.Now().Add(-time.Second)

	var reply string
	h.Handle(context.Background(), req, func(body []byte) error {
		reply = string(body)
		return nil
	})
	require.Equal(t, "ERROR: transient: index: timeout", reply)
	require.Empty(t, index.bodies)
	require.Empty(t, writer.calls())
}

func TestDeliveryHandler_TimeoutNamesRunningHalf(t *testing.T) {
	tests := []struct {
		name       string
		indexDelay time.Duration
		fgaDelay   time.Duration
		want       string
	}{
		{"fga still running", 0, time.Minute, "ERROR: transient: fga: timeout"},
		{"index still running", time.Minute, 0, "ERROR: transient: index: timeout"},
		{"both running", time.Minute, time.Minute, "ERROR: transient: index: timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index, writer := &fakeIndex{delay: tt.indexDelay}, &fakeFGAWriter{delay: tt.fgaDelay}
			h := NewDeliveryHandler(index, writer, nil, 100*time.Millisecond, discardLogger())
			done := make(chan string, 1)
			go func() { done <- deliver(t, h, deliverySubject, indexRequest(t, withFGA)) }()
			select {
			case reply := <-done:
				require.Equal(t, tt.want, reply)
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not reply after its timeout")
			}
		})
	}
}

// Handle returns only once the halves stop, so a timed-out request cannot
// overlap the next request for the same object.
func TestDeliveryHandler_HandleWaitsForHalvesAfterTimeout(t *testing.T) {
	release := make(chan struct{})
	index := &blockingIndex{release: release}
	h := NewDeliveryHandler(index, &fakeFGAWriter{}, nil, 50*time.Millisecond, discardLogger())
	req, failure := parseDeliveryRequest(indexRequest(t, nil), deliverySubject)
	require.Nil(t, failure)

	replied, returned := make(chan string, 1), make(chan struct{})
	go func() {
		h.Handle(context.Background(), req, func(body []byte) error {
			replied <- string(body)
			return nil
		})
		close(returned)
	}()
	require.Equal(t, "ERROR: transient: index: timeout", <-replied)
	select {
	case <-returned:
		t.Fatal("Handle returned while the index half was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-returned
}

// blockingIndex ignores cancellation until released, like a storage call
// that does not honor its context.
type blockingIndex struct{ release chan struct{} }

func (b *blockingIndex) ProcessIndexingMessage(context.Context, []byte, string) error {
	<-b.release
	return nil
}
