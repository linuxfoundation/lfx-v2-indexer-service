// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/fgacache"
)

const deliveryCacheBucket = "fixture-delivery-cache"

// natsKVCache starts an embedded JetStream server with the shared cache
// bucket and returns the indexer's NATS KV cache over it.
func natsKVCache(t *testing.T) (*fgacache.Cache, jetstream.KeyValue, *server.Server) {
	t.Helper()
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	require.NoError(t, err)
	go s.Start()
	require.True(t, s.ReadyForConnections(10*time.Second))
	t.Cleanup(s.Shutdown)
	nc, err := natsgo.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: deliveryCacheBucket, History: 1, TTL: 3 * time.Hour})
	require.NoError(t, err)

	cache := fgacache.New(config.CacheConfig{Backend: config.CacheBackendNATSKV, Bucket: deliveryCacheBucket, Timeout: time.Second, RetryInterval: 50 * time.Millisecond}, nc, discardLogger())
	t.Cleanup(cache.Close)
	require.Eventually(t, func() bool {
		_, err := cache.CurrentMarker(context.Background())
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	return cache, kv, s
}

func invRevision(t *testing.T, kv jetstream.KeyValue) uint64 {
	t.Helper()
	entry, err := kv.Get(context.Background(), "inv")
	if err != nil {
		return 0
	}
	return entry.Revision()
}

// replyObserver records the inv marker revision at the moment the reply is
// sent, which proves the invalidation landed before OK.
func deliverObservingInv(t *testing.T, h *DeliveryHandler, kv jetstream.KeyValue) (string, uint64) {
	t.Helper()
	req, failure := parseDeliveryRequest(indexRequest(t, fgaOnly), deliverySubject)
	require.Nil(t, failure)
	var reply string
	var atReply uint64
	h.Handle(context.Background(), req, func(body []byte) error {
		reply = string(body)
		atReply = invRevision(t, kv)
		return nil
	})
	return reply, atReply
}

func TestDeliveryCache_InvalidatesBeforeReplyingOK(t *testing.T) {
	cache, kv, _ := natsKVCache(t)
	before := invRevision(t, kv)
	writer := &fakeFGAWriter{result: contracts.FGAWriteResult{Changed: true, SeedKeys: []string{"fixture_resource:fixture-1#viewer@user:fixture-user"}}}
	h := newTestDeliveryHandler(&fakeIndex{}, writer, cache)

	reply, atReply := deliverObservingInv(t, h, kv)
	require.Equal(t, "OK", reply)
	require.Greater(t, atReply, before, "the inv marker must advance before the reply")

	marker, err := cache.CurrentMarker(context.Background())
	require.NoError(t, err)
	got, err := cache.Get(context.Background(), writer.result.SeedKeys, marker)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{writer.result.SeedKeys[0]: true}, got, "positive keys are seeded after the invalidation")
}

// The replay of a request whose tuples landed but whose invalidation failed
// changes nothing, and must still advance the marker before replying OK.
func TestDeliveryCache_UnchangedReplayStillInvalidates(t *testing.T) {
	cache, kv, _ := natsKVCache(t)
	before := invRevision(t, kv)
	h := newTestDeliveryHandler(&fakeIndex{}, &fakeFGAWriter{result: contracts.FGAWriteResult{Changed: false}}, cache)

	reply, atReply := deliverObservingInv(t, h, kv)
	require.Equal(t, "OK", reply)
	require.Greater(t, atReply, before, "the inv marker must advance before the reply")
}

func TestDeliveryCache_FailedWriteThatChangedNothingSkipsInvalidation(t *testing.T) {
	cache, kv, _ := natsKVCache(t)
	before := invRevision(t, kv)
	writer := &fakeFGAWriter{result: contracts.FGAWriteResult{Changed: false}, err: errors.New("openfga unavailable")}
	h := newTestDeliveryHandler(&fakeIndex{}, writer, cache)

	reply, atReply := deliverObservingInv(t, h, kv)
	require.Equal(t, "ERROR: transient: fga: openfga unavailable", reply)
	require.Equal(t, before, atReply)
}

func TestDeliveryCache_InvalidationFailureIsNotOK(t *testing.T) {
	cache, _, s := natsKVCache(t)
	s.Shutdown()
	h := newTestDeliveryHandler(&fakeIndex{}, &fakeFGAWriter{result: contracts.FGAWriteResult{Changed: true}}, cache)

	req, failure := parseDeliveryRequest(indexRequest(t, fgaOnly), deliverySubject)
	require.Nil(t, failure)
	var reply string
	h.Handle(context.Background(), req, func(body []byte) error {
		reply = string(body)
		return nil
	})
	require.Equal(t, "ERROR: transient: fga: cache invalidation failed", reply)
}

// A write that fails after an earlier batch landed reports Changed with its
// error: the cache is still invalidated, nothing is seeded, and the reply
// carries the write error.
func TestDeliveryCache_FailedWriteThatChangedTuplesStillInvalidates(t *testing.T) {
	cache, kv, _ := natsKVCache(t)
	before := invRevision(t, kv)
	seedKey := "fixture_resource:fixture-1#viewer@user:fixture-user"
	writer := &fakeFGAWriter{
		result: contracts.FGAWriteResult{Changed: true, SeedKeys: []string{seedKey}},
		err:    errors.New("second batch failed"),
	}
	h := newTestDeliveryHandler(&fakeIndex{}, writer, cache)

	reply, atReply := deliverObservingInv(t, h, kv)
	require.Equal(t, "ERROR: transient: fga: second batch failed", reply)
	require.Greater(t, atReply, before, "the inv marker must advance before the reply")

	marker, err := cache.CurrentMarker(context.Background())
	require.NoError(t, err)
	got, err := cache.Get(context.Background(), []string{seedKey}, marker)
	require.NoError(t, err)
	require.Empty(t, got, "a failed write must not seed")
}

// Writer A grants a tuple; while A is writing, writer B removes it and
// invalidates. A's invalidation then follows B's, and A's seed would answer
// "allowed" for a tuple OpenFGA no longer has, so A must not seed.
func TestDeliveryCache_WriteOvertakenByAnotherInvalidationDoesNotSeed(t *testing.T) {
	cache, kv, _ := natsKVCache(t)
	seedKey := "fixture_resource:fixture-1#viewer@user:fixture-user"
	var otherErr error
	writer := &fakeFGAWriter{
		result: contracts.FGAWriteResult{Changed: true, SeedKeys: []string{seedKey}},
		during: func() {
			_, _, otherErr = cache.Invalidate(context.Background(), contracts.InvalidationMarker{})
		},
	}
	h := newTestDeliveryHandler(&fakeIndex{}, writer, cache)
	before := invRevision(t, kv)

	reply, atReply := deliverObservingInv(t, h, kv)
	require.NoError(t, otherErr)
	require.Equal(t, "OK", reply)
	require.Greater(t, atReply, before+1, "both invalidations land before the reply")

	marker, err := cache.CurrentMarker(context.Background())
	require.NoError(t, err)
	got, err := cache.Get(context.Background(), []string{seedKey}, marker)
	require.NoError(t, err)
	require.Empty(t, got, "an overtaken write must not seed")
}

// markerReadFails is a cache whose CurrentMarker fails while the rest works.
type markerReadFails struct {
	contracts.FGACache
}

func (markerReadFails) CurrentMarker(context.Context) (contracts.InvalidationMarker, error) {
	return contracts.InvalidationMarker{}, contracts.ErrCacheBypassed
}

func TestDeliveryCache_FailedMarkerReadStillInvalidatesWithoutSeeding(t *testing.T) {
	cache, kv, _ := natsKVCache(t)
	seedKey := "fixture_resource:fixture-1#viewer@user:fixture-user"
	writer := &fakeFGAWriter{result: contracts.FGAWriteResult{Changed: true, SeedKeys: []string{seedKey}}}
	h := newTestDeliveryHandler(&fakeIndex{}, writer, markerReadFails{cache})
	before := invRevision(t, kv)

	reply, atReply := deliverObservingInv(t, h, kv)
	require.Equal(t, "OK", reply, "a failed marker read must not fail the write")
	require.Greater(t, atReply, before, "the inv marker must advance before the reply")

	marker, err := cache.CurrentMarker(context.Background())
	require.NoError(t, err)
	got, err := cache.Get(context.Background(), []string{seedKey}, marker)
	require.NoError(t, err)
	require.Empty(t, got, "no seed without a marker read before the write")
}

// A write that lands after the request deadline must still advance the
// marker: its tuples are in OpenFGA, so entries cached before them are stale.
func TestDeliveryCache_InvalidatesAfterTheRequestDeadline(t *testing.T) {
	cache, kv, _ := natsKVCache(t)
	writer := &fakeFGAWriter{
		result: contracts.FGAWriteResult{Changed: true},
		during: func() { time.Sleep(200 * time.Millisecond) },
	}
	h := NewDeliveryHandler(&fakeIndex{}, writer, cache, 50*time.Millisecond, discardLogger())
	before := invRevision(t, kv)

	reply, atReply := deliverObservingInv(t, h, kv)
	require.Equal(t, "ERROR: transient: fga: timeout", reply)
	require.Equal(t, before, atReply, "the reply goes out at the deadline, before the write lands")
	require.Greater(t, invRevision(t, kv), before, "the marker must advance once the late write lands")
}

// With CACHE_BACKEND=none the container passes no cache, so a tuple change
// replies OK without an invalidation.
func TestDeliveryCache_NoneBackendSkipsInvalidation(t *testing.T) {
	none := fgacache.New(config.CacheConfig{Backend: config.CacheBackendNone}, nil, discardLogger())
	t.Cleanup(none.Close)
	_, _, err := none.Invalidate(context.Background(), contracts.InvalidationMarker{})
	require.ErrorIs(t, err, contracts.ErrCacheBypassed, "the none backend fails Invalidate, so the handler must not call it")

	h := newTestDeliveryHandler(&fakeIndex{}, &fakeFGAWriter{result: contracts.FGAWriteResult{Changed: true}}, nil)
	require.Equal(t, "OK", deliver(t, h, deliverySubject, indexRequest(t, fgaOnly)))
}
