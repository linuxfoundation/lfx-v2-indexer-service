// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"sync"
)

// deliveryRunner is the part of DeliveryHandler the dispatcher drives.
type deliveryRunner interface {
	Handle(ctx context.Context, req *deliveryRequest, reply func([]byte) error)
	Reject(ctx context.Context, subject string, failure *deliveryFailure, reply func([]byte) error)
}

type queuedDelivery struct {
	ctx   context.Context
	req   *deliveryRequest
	reply func([]byte) error
}

// OrderedDispatcher applies requests for the same object_ref in arrival order
// and requests for different objects concurrently. Dispatch must be called
// from one goroutine per subscription, in delivery order, which is what
// QueueSubscribeDispatch does.
type OrderedDispatcher struct {
	runner deliveryRunner
	// slots bounds requests queued or running; a full dispatcher blocks
	// Dispatch, and with it the NATS callback, as backpressure.
	slots chan struct{}

	mu sync.Mutex
	// queues holds the pending requests of each key that has a drain
	// goroutine; a key is present exactly while its goroutine runs.
	queues map[string][]queuedDelivery
	wg     sync.WaitGroup
}

// NewOrderedDispatcher bounds in-flight requests by maxInFlight.
func NewOrderedDispatcher(handler *DeliveryHandler, maxInFlight int) *OrderedDispatcher {
	return newOrderedDispatcher(handler, maxInFlight)
}

func newOrderedDispatcher(runner deliveryRunner, maxInFlight int) *OrderedDispatcher {
	return &OrderedDispatcher{
		runner: runner,
		slots:  make(chan struct{}, max(maxInFlight, 1)),
		queues: make(map[string][]queuedDelivery),
	}
}

// Dispatch parses one request and queues it behind earlier requests for the
// same object. A request that cannot be parsed or keyed is rejected at once.
func (d *OrderedDispatcher) Dispatch(ctx context.Context, data []byte, subject string, reply func([]byte) error) {
	req, failure := parseDeliveryRequest(data, subject)
	if failure != nil {
		d.runner.Reject(ctx, subject, failure, reply)
		return
	}
	d.slots <- struct{}{}

	d.mu.Lock()
	defer d.mu.Unlock()
	pending, busy := d.queues[req.objectRef]
	d.queues[req.objectRef] = append(pending, queuedDelivery{ctx: ctx, req: req, reply: reply})
	if !busy {
		d.wg.Add(1)
		go d.drain(req.objectRef)
	}
}

// Wait blocks until every queued request has been handled.
func (d *OrderedDispatcher) Wait() {
	d.wg.Wait()
}

func (d *OrderedDispatcher) drain(key string) {
	defer d.wg.Done()
	for {
		next, ok := d.next(key)
		if !ok {
			return
		}
		d.runner.Handle(next.ctx, next.req, next.reply)
		<-d.slots
	}
}

func (d *OrderedDispatcher) next(key string) (queuedDelivery, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	pending := d.queues[key]
	if len(pending) == 0 {
		delete(d.queues, key)
		return queuedDelivery{}, false
	}
	next := pending[0]
	pending[0] = queuedDelivery{}
	d.queues[key] = pending[1:]
	return next, true
}
