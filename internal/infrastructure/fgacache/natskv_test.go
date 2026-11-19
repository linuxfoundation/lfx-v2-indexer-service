// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

var bucketSeq atomic.Int64

func startJetStream(t *testing.T) *server.Server {
	t.Helper()
	s, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	})
	require.NoError(t, err)
	go s.Start()
	require.True(t, s.ReadyForConnections(10*time.Second), "nats server did not start")
	t.Cleanup(s.Shutdown)
	return s
}

func connectJetStream(t *testing.T, s *server.Server) jetstream.JetStream {
	t.Helper()
	nc, err := natsgo.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	return js
}

// createBucket matches the deployed bucket policy: one revision per key, 3 h TTL.
func createBucket(t *testing.T, js jetstream.JetStream, name string) jetstream.KeyValue {
	t.Helper()
	kv, err := js.CreateKeyValue(testContext(t), jetstream.KeyValueConfig{
		Bucket:  name,
		History: 1,
		TTL:     3 * time.Hour,
	})
	require.NoError(t, err)
	return kv
}

func newBucketName() string {
	return fmt.Sprintf("fga-cache-test-%d", bucketSeq.Add(1))
}

func TestNATSKV_Conformance(t *testing.T) {
	s := startJetStream(t)
	js := connectJetStream(t, s)

	runConformance(t, conformanceBackend{
		fresh: func(t *testing.T) contracts.FGACache {
			name := newBucketName()
			createBucket(t, js, name)
			c := newNATSKVStore(js, name)
			require.NoError(t, c.connect(testContext(t)))
			return c
		},
		unreachable: func(t *testing.T) contracts.FGACache {
			gone := startJetStream(t)
			goneJS := connectJetStream(t, gone)
			name := newBucketName()
			createBucket(t, goneJS, name)
			c := newNATSKVStore(goneJS, name)
			require.NoError(t, c.connect(testContext(t)))
			gone.Shutdown()
			return c
		},
	})
}

func TestNATSKV_InvalidationWrittenByFGASyncHidesOlderEntries(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	c := newNATSKVStore(js, name)
	require.NoError(t, c.connect(ctx))

	m, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	require.NoError(t, c.Set(ctx, map[string]bool{"obj:1#writer@user:a": true}, m))

	// fga-sync writes the marker this way (fga_cache.go, invalidate).
	_, err = kv.Put(ctx, "inv", []byte("1"))
	require.NoError(t, err)

	m, err = c.CurrentMarker(ctx)
	require.NoError(t, err)
	got, err := c.Get(ctx, []string{"obj:1#writer@user:a"}, m)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestNATSKV_EntryWrittenByFGASyncIsReadable(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	c := newNATSKVStore(js, name)
	require.NoError(t, c.connect(ctx))

	relationKey := RelationKey("project:00000000-0000-4000-8000-000000000001", "writer", "user:example-user")
	_, err := kv.Put(ctx, CacheKey(relationKey), []byte("true"))
	require.NoError(t, err)

	m, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	got, err := c.Get(ctx, []string{relationKey}, m)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{relationKey: true}, got)
}

func TestNATSKV_InvalidateMarkerIsItsOwnWrite(t *testing.T) {
	js := connectJetStream(t, startJetStream(t))

	t.Run("a later invalidation discards the earlier writer's seed", func(t *testing.T) {
		ctx := testContext(t)
		name := newBucketName()
		kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: name, History: 20, TTL: 3 * time.Hour})
		require.NoError(t, err)
		c := newNATSKVStore(js, name)
		require.NoError(t, c.connect(ctx))

		// Writer A grants and invalidates; writer B revokes and invalidates
		// before A reads its marker back.
		revA, err := kv.Put(ctx, InvalidationKey, []byte("1"))
		require.NoError(t, err)
		_, err = kv.Put(ctx, InvalidationKey, []byte("1"))
		require.NoError(t, err)

		markerA, err := c.markerAt(ctx, kv, revA)
		require.NoError(t, err)
		require.NoError(t, c.Seed(ctx, []string{"obj:7#writer@user:a"}, markerA))

		current, err := c.CurrentMarker(ctx)
		require.NoError(t, err)
		assert.True(t, current.Time().After(markerA.Time()))
		got, err := c.Get(ctx, []string{"obj:7#writer@user:a"}, current)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("in a one-revision bucket a replaced marker still discards the seed", func(t *testing.T) {
		ctx := testContext(t)
		name := newBucketName()
		kv := createBucket(t, js, name)
		c := newNATSKVStore(js, name)
		require.NoError(t, c.connect(ctx))

		revA, err := kv.Put(ctx, InvalidationKey, []byte("1"))
		require.NoError(t, err)
		_, err = kv.Put(ctx, InvalidationKey, []byte("1"))
		require.NoError(t, err)

		markerA, err := c.markerAt(ctx, kv, revA)
		require.NoError(t, err, "the invalidation was recorded, so this is not a failure")
		require.NoError(t, c.Seed(ctx, []string{"obj:9#writer@user:a"}, markerA))

		current, err := c.CurrentMarker(ctx)
		require.NoError(t, err)
		got, err := c.Get(ctx, []string{"obj:9#writer@user:a"}, current)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestNATSKV_SetDoesNotPublishResultsFromBeforeAnInvalidation(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	c := newNATSKVStore(js, name)
	require.NoError(t, c.connect(ctx))

	beforeCheck, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	_, err = c.Invalidate(ctx)
	require.NoError(t, err)

	require.NoError(t, c.Set(ctx, map[string]bool{"obj:8#writer@user:a": true}, beforeCheck))

	// Never written, so a concurrent reader could not have seen it.
	_, err = kv.History(ctx, CacheKey("obj:8#writer@user:a"))
	assert.ErrorIs(t, err, jetstream.ErrKeyNotFound)
}

func TestNATSKV_SetRemovesResultsWhenInvalidatedDuringTheWrites(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	c := newNATSKVStore(js, name)
	require.NoError(t, c.connect(ctx))

	beforeCheck, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	c.afterWrites = func() {
		_, err := kv.Put(ctx, InvalidationKey, []byte("1"))
		require.NoError(t, err)
	}

	require.NoError(t, c.Set(ctx, map[string]bool{"obj:10#writer@user:a": true}, beforeCheck))

	_, err = kv.Get(ctx, CacheKey("obj:10#writer@user:a"))
	assert.ErrorIs(t, err, jetstream.ErrKeyNotFound, "the written result was removed")
}

func TestNATSKV_InvalidateReturnsMarkerOfItsWrite(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: name, History: 20, TTL: 3 * time.Hour})
	require.NoError(t, err)
	c := newNATSKVStore(js, name)
	require.NoError(t, c.connect(ctx))

	first, err := c.Invalidate(ctx)
	require.NoError(t, err)
	second, err := c.Invalidate(ctx)
	require.NoError(t, err)

	entry, err := kv.Get(ctx, InvalidationKey)
	require.NoError(t, err)
	assert.Equal(t, entry.Created(), second.Time())
	assert.True(t, second.Time().After(first.Time()))
}

func TestNATSKV_OnlyExactTrueOrFalseIsAResult(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	c := newNATSKVStore(js, name)
	require.NoError(t, c.connect(ctx))

	for _, value := range []string{"1", "T", "TRUE", "yes"} {
		_, err := kv.Put(ctx, CacheKey("obj:1#writer@user:"+value), []byte(value))
		require.NoError(t, err)
	}

	m, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	got, err := c.Get(ctx, []string{"obj:1#writer@user:1", "obj:1#writer@user:T", "obj:1#writer@user:TRUE", "obj:1#writer@user:yes"}, m)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestNATSKV_MissingBucketBypassesThenRecovers(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	logger, buf := newTestLogger()

	c := newFailSafe(newNATSKVStore(js, name), time.Second, 50*time.Millisecond, logger)
	defer c.Close()

	_, err := c.CurrentMarker(ctx)
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	require.Eventually(t, func() bool { return buf.warnCount() > 0 }, 2*time.Second, 5*time.Millisecond)

	createBucket(t, js, name)
	require.Eventually(t, c.isReady, 2*time.Second, 5*time.Millisecond)
	_, err = c.CurrentMarker(ctx)
	require.NoError(t, err)
}
