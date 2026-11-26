// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const seededKey = "project:p1#writer@user:example-user"

// seedAfterWrite runs a write as the delivery handler does: read the marker,
// let during act as another writer while the tuples are written, invalidate,
// then seed. It returns whether the seeded key is readable and Seed's error.
func seedAfterWrite(t *testing.T, modelID string, prepare, during func(ctx context.Context, kv jetstream.KeyValue)) (bool, error) {
	t.Helper()
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	c := newNATSKVStore(js, name, modelID)
	require.NoError(t, c.connect(ctx))
	if prepare != nil {
		prepare(ctx, kv)
	}

	before, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	if during != nil {
		during(ctx, kv)
	}
	m, onlyMine, err := c.Invalidate(ctx, before)
	require.NoError(t, err)
	require.True(t, onlyMine, "fga-sync's scoped markers leave inv alone")
	seedErr := c.Seed(ctx, []string{seededKey}, m)

	current, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	got, err := c.Get(ctx, []string{seededKey}, current)
	require.NoError(t, err)
	return got[seededKey], seedErr
}

func putMarker(key, value string) func(context.Context, jetstream.KeyValue) {
	return func(ctx context.Context, kv jetstream.KeyValue) {
		if _, err := kv.Put(ctx, key, []byte(value)); err != nil {
			panic(err)
		}
	}
}

// fga-sync in scoped mode records a revocation only in inv.t.<type> or
// inv.u.<type>.<user> (fga_invalidation.go), and its lookups honor both.
func TestNATSKV_SeedHonorsFGASyncScopedMarkers(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(context.Context, jetstream.KeyValue)
		during  func(context.Context, jetstream.KeyValue)
		seeded  bool
	}{
		{name: "no marker moved", seeded: true},
		{name: "type marker written during the write", during: putMarker(TypeMarkerKey("project"), "1")},
		{name: "user marker written during the write", during: putMarker(UserMarkerKey("project", "user:example-user"), "1")},
		{name: "user marker for another user", during: putMarker(UserMarkerKey("project", "user:other-user"), "1"), seeded: true},
		{name: "type marker for another type", during: putMarker(TypeMarkerKey("committee"), "1"), seeded: true},
		{name: "type marker written before the write", prepare: putMarker(TypeMarkerKey("project"), "1"), seeded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seeded, err := seedAfterWrite(t, "", tt.prepare, tt.during)
			assert.Equal(t, tt.seeded, seeded)
			if tt.seeded {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, errSeedOvertaken)
			}
		})
	}
}

// fga-sync skips its own seeds while inv.model names another model than the
// one it writes with (seedPositiveEntries, modelFence).
func TestNATSKV_SeedHonorsFGASyncModelMarker(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		prepare func(context.Context, jetstream.KeyValue)
		seeded  bool
	}{
		{name: "pinned, another model announced", modelID: "model-a", prepare: putMarker(ModelMarkerKey, "model-b")},
		{name: "pinned, same model announced", modelID: "model-a", prepare: putMarker(ModelMarkerKey, "model-a"), seeded: true},
		{name: "pinned, no model announced", modelID: "model-a", seeded: true},
		{name: "unpinned writes with the latest model", prepare: putMarker(ModelMarkerKey, "model-b"), seeded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seeded, err := seedAfterWrite(t, tt.modelID, tt.prepare, nil)
			assert.Equal(t, tt.seeded, seeded)
			if tt.seeded {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, errSeedOvertaken)
			}
		})
	}
}

// A scoped marker written while the seed's entries are being written leaves
// them readable until Seed re-reads the markers and removes them.
func TestNATSKV_SeedRemovesEntriesWhenAMarkerMovesDuringItsWrites(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	c := newNATSKVStore(js, name, "")
	require.NoError(t, c.connect(ctx))
	c.afterWrites = func() { putMarker(TypeMarkerKey("project"), "1")(ctx, kv) }

	before, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	m, _, err := c.Invalidate(ctx, before)
	require.NoError(t, err)
	require.ErrorIs(t, c.Seed(ctx, []string{seededKey}, m), errSeedOvertaken)

	_, err = kv.Get(ctx, CacheKey(seededKey))
	assert.ErrorIs(t, err, jetstream.ErrKeyNotFound)
}

// markerReadFailsKV fails reads of fga-sync's scoped markers.
type markerReadFailsKV struct {
	jetstream.KeyValue
}

func (kv markerReadFailsKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	if strings.HasPrefix(key, typeMarkerPrefix) {
		return nil, errors.New("marker read failed")
	}
	return kv.KeyValue.Get(ctx, key)
}

func TestNATSKV_SeedSkipsWhenAScopedMarkerCannotBeRead(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	c := newNATSKVStore(js, name, "")
	require.NoError(t, c.connect(ctx))
	before, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	m, _, err := c.Invalidate(ctx, before)
	require.NoError(t, err)

	c.kv = markerReadFailsKV{kv}
	err = c.Seed(ctx, []string{seededKey}, m)
	require.Error(t, err)
	assert.NotErrorIs(t, err, errSeedOvertaken, "an unreadable marker is a store error, not an overtaken seed")

	_, err = kv.Get(ctx, CacheKey(seededKey))
	assert.ErrorIs(t, err, jetstream.ErrKeyNotFound)
}

// The fail-safe wrapper counts a skipped seed with the overtaken
// invalidations and keeps the store up.
func TestFailSafe_SeedOvertakenByAScopedMarkerIsCounted(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	c, reader := newMeteredFailSafe(t, newNATSKVStore(js, name, ""))

	before, err := c.CurrentMarker(ctx)
	require.NoError(t, err)
	putMarker(UserMarkerKey("project", "user:example-user"), "1")(ctx, kv)
	m, onlyMine, err := c.Invalidate(ctx, before)
	require.NoError(t, err)
	require.True(t, onlyMine)

	require.NoError(t, c.Seed(ctx, []string{seededKey}, m))
	assert.Equal(t, int64(1), counterValue(t, reader, "fga_cache.invalidations_overtaken"))
	assert.Zero(t, counterValue(t, reader, "fga_cache.errors"))
	assert.True(t, c.isReady())
}
