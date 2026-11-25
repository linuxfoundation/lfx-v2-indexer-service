// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// recordingRunner records the order requests are handled and can hold one
// object until released.
type recordingRunner struct {
	mu       sync.Mutex
	handled  []string
	rejected []string
	hold     map[string]chan struct{}
	started  chan string
}

func newRecordingRunner() *recordingRunner {
	return &recordingRunner{hold: map[string]chan struct{}{}, started: make(chan string, 100)}
}

func (r *recordingRunner) Handle(_ context.Context, req *deliveryRequest, reply func([]byte) error) {
	r.started <- req.objectRef
	r.mu.Lock()
	gate := r.hold[req.objectRef]
	r.mu.Unlock()
	if gate != nil {
		<-gate
	}
	var body struct {
		Seq int `json:"seq"`
	}
	_ = json.Unmarshal(req.indexBody, &body)
	r.mu.Lock()
	r.handled = append(r.handled, fmt.Sprintf("%s#%d", req.objectRef, body.Seq))
	r.mu.Unlock()
	_ = reply([]byte(replyOK))
}

func (r *recordingRunner) Reject(_ context.Context, _ string, failure *deliveryFailure, reply func([]byte) error) {
	r.mu.Lock()
	r.rejected = append(r.rejected, string(failure.reply()))
	r.mu.Unlock()
	_ = reply(failure.reply())
}

func (r *recordingRunner) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.handled...)
}

func dispatchRequest(id string, seq int) []byte {
	return fmt.Appendf(nil, `{"action":"updated","seq":%d,"indexing_config":{"object_id":%q}}`, seq, id)
}

func noReply([]byte) error { return nil }

func noDone() {}

func TestOrderedDispatcher_SameObjectInArrivalOrder(t *testing.T) {
	runner := newRecordingRunner()
	d := newOrderedDispatcher(runner, 100)
	for seq := range 50 {
		d.Dispatch(context.Background(), dispatchRequest("fixture-1", seq), deliverySubject, noReply, noDone)
	}
	d.Wait()

	want := make([]string, 50)
	for seq := range want {
		want[seq] = fmt.Sprintf("fixture_resource:fixture-1#%d", seq)
	}
	require.Equal(t, want, runner.order())
}

func TestOrderedDispatcher_SlowObjectDoesNotDelayOthers(t *testing.T) {
	runner := newRecordingRunner()
	gate := make(chan struct{})
	runner.hold["fixture_resource:slow"] = gate
	d := newOrderedDispatcher(runner, 100)

	d.Dispatch(context.Background(), dispatchRequest("slow", 1), deliverySubject, noReply, noDone)
	d.Dispatch(context.Background(), dispatchRequest("slow", 2), deliverySubject, noReply, noDone)
	start := time.Now()
	d.Dispatch(context.Background(), dispatchRequest("fast", 1), deliverySubject, noReply, noDone)

	require.Eventually(t, func() bool {
		return len(runner.order()) == 1
	}, 5*time.Second, time.Millisecond)
	require.Equal(t, []string{"fixture_resource:fast#1"}, runner.order())
	require.Less(t, time.Since(start), time.Second, "an unrelated object must not wait for the held one")

	close(gate)
	d.Wait()
	require.Equal(t, []string{"fixture_resource:fast#1", "fixture_resource:slow#1", "fixture_resource:slow#2"}, runner.order())
}

// The FGA-only key falls back to fga.data.uid, so it orders with an index
// request for the same object.
func TestOrderedDispatcher_FGAOnlyKeyUsesUID(t *testing.T) {
	req, failure := parseDeliveryRequest([]byte(`{"fga":{"object_type":"fixture_resource","operation":"delete_access","data":{"uid":"fixture-1"}}}`), deliverySubject)
	require.Nil(t, failure)
	require.Equal(t, "fixture_resource:fixture-1", req.objectRef)
	require.Nil(t, req.indexBody)

	req, failure = parseDeliveryRequest(dispatchRequest("fixture-1", 1), deliverySubject)
	require.Nil(t, failure)
	require.Equal(t, "fixture_resource:fixture-1", req.objectRef)
}

func TestOrderedDispatcher_BoundBlocksDispatchWhenFull(t *testing.T) {
	runner := newRecordingRunner()
	gate := make(chan struct{})
	runner.hold["fixture_resource:a"] = gate
	runner.hold["fixture_resource:b"] = gate
	d := newOrderedDispatcher(runner, 2)

	d.Dispatch(context.Background(), dispatchRequest("a", 1), deliverySubject, noReply, noDone)
	d.Dispatch(context.Background(), dispatchRequest("b", 1), deliverySubject, noReply, noDone)
	third := make(chan struct{})
	go func() {
		d.Dispatch(context.Background(), dispatchRequest("c", 1), deliverySubject, noReply, noDone)
		close(third)
	}()
	select {
	case <-third:
		t.Fatal("Dispatch returned while every slot was taken")
	case <-time.After(200 * time.Millisecond):
	}
	close(gate)
	select {
	case <-third:
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatch stayed blocked after slots were released")
	}
	d.Wait()
	require.Len(t, runner.order(), 3)
}

func TestOrderedDispatcher_RejectsWithoutQueueing(t *testing.T) {
	runner := newRecordingRunner()
	d := newOrderedDispatcher(runner, 1)
	var reply string
	d.Dispatch(context.Background(), []byte(`not json`), deliverySubject, func(body []byte) error {
		reply = string(body)
		return nil
	}, noDone)
	require.Equal(t, "ERROR: rejected: index: invalid request JSON", reply)
	d.Wait()
	require.Empty(t, runner.order())
}

// done marks the end of a request's work, which can outlive its reply: the
// shutdown drain waits for it before closing the connection.
func TestOrderedDispatcher_DoneFollowsHandleNotReply(t *testing.T) {
	release := make(chan struct{})
	h := NewDeliveryHandler(&blockingIndex{release: release}, &fakeFGAWriter{}, nil, 50*time.Millisecond, discardLogger())
	d := NewOrderedDispatcher(h, 1)
	replied, done := make(chan string, 1), make(chan struct{})
	d.Dispatch(context.Background(), indexRequest(t, nil), deliverySubject, func(body []byte) error {
		replied <- string(body)
		return nil
	}, func() { close(done) })

	require.Equal(t, "ERROR: transient: index: timeout", <-replied)
	select {
	case <-done:
		t.Fatal("done was called while the index half was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("done was not called after the request finished")
	}
	d.Wait()
}

func TestOrderedDispatcher_WaitDrainsQueuedObjects(t *testing.T) {
	runner := newRecordingRunner()
	gate := make(chan struct{})
	runner.hold["fixture_resource:a"] = gate
	d := newOrderedDispatcher(runner, 100)
	for seq := range 5 {
		d.Dispatch(context.Background(), dispatchRequest("a", seq), deliverySubject, noReply, noDone)
	}
	<-runner.started

	waited := make(chan struct{})
	go func() {
		d.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("Wait returned with requests still queued")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the queue drained")
	}
	require.Len(t, runner.order(), 5)
	d.mu.Lock()
	defer d.mu.Unlock()
	require.Empty(t, d.queues, "a drained key must be removed")
}

// The contract's slow-resource case end to end: one resource's FGA write is
// delayed 5 s, another resource replies with no added delay.
func TestOrderedDispatcher_DelayedFGAWriteDoesNotDelayOtherResources(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 5 s")
	}
	slow := &delayingWriter{delayFor: "slow", delay: 5 * time.Second}
	h := NewDeliveryHandler(&fakeIndex{}, slow, nil, 10*time.Second, discardLogger())
	d := NewOrderedDispatcher(h, 100)

	replies := make(chan string, 2)
	send := func(uid string) {
		payload := fmt.Appendf(nil, `{"fga":{"object_type":"fixture_resource","operation":"delete_access","data":{"uid":%q}}}`, uid)
		d.Dispatch(context.Background(), payload, deliverySubject, func(body []byte) error {
			replies <- uid + " " + string(body)
			return nil
		}, noDone)
	}
	start := time.Now()
	send("slow")
	send("fast")
	require.Equal(t, "fast OK", <-replies)
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, "slow OK", <-replies)
	require.GreaterOrEqual(t, time.Since(start), 5*time.Second)
	d.Wait()
}

type delayingWriter struct {
	delayFor string
	delay    time.Duration
}

func (w *delayingWriter) Apply(_ context.Context, _ string, op json.RawMessage) (contracts.FGAWriteResult, error) {
	var parsed struct {
		Data struct {
			UID string `json:"uid"`
		} `json:"data"`
	}
	_ = json.Unmarshal(op, &parsed)
	if parsed.Data.UID == w.delayFor {
		time.Sleep(w.delay)
	}
	return contracts.FGAWriteResult{}, nil
}
