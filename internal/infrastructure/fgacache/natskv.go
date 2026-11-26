// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// The key format and the last-invalidation marker semantics in this file are
// duplicated in lfx-v2-fga-sync/fga_cache.go. While both services read or
// write the same cache they must stay identical. Change them in both repos and
// upgrade both services together.
//
// The marker is the key "inv"; its server-assigned creation time is the
// cut-off, and an entry created before it is stale. This needs only the
// latest revision of each key, so it works with a bucket that keeps one.

// repairTimeout bounds making stale results unreadable after the caller's own
// deadline has passed. Hiding a possibly revoked grant is worth this extra
// time on a path where the store is already failing.
const repairTimeout = time.Second

// natsKVStore is the NATS JetStream KV backend. It binds the bucket lazily,
// so a missing bucket is reported as an error instead of failing startup.
type natsKVStore struct {
	js     jetstream.JetStream
	bucket string
	// modelID is the authorization model the FGA writer pins; empty when it
	// writes with the store's latest model.
	modelID string
	// sawOtherModel is set once inv.model has named another model, and cleared
	// only when it names modelID, so a marker that then expires or is deleted
	// keeps blocking seeds, as lfx-v2-fga-sync's modelFence does.
	sawOtherModel atomic.Bool

	mu sync.RWMutex
	kv jetstream.KeyValue

	// repairsPending counts Set calls that could neither remove a possibly
	// revoked result nor rewrite the marker. While it is non-zero Get fails
	// and connect rewrites the marker before reporting the store healthy,
	// because the result is also readable by lfx-v2-fga-sync.
	repairsPending atomic.Int64

	// afterWrites, when set by tests, runs between Set's writes and its
	// second marker read.
	afterWrites func()
}

// errSeedOvertaken means a seed was skipped, or what it wrote was removed,
// because a marker that applies to its keys moved during the write.
var errSeedOvertaken = errors.New("fga cache seed overtaken by an invalidation")

func newNATSKVStore(js jetstream.JetStream, bucket, modelID string) *natsKVStore {
	return &natsKVStore{js: js, bucket: bucket, modelID: modelID}
}

func (s *natsKVStore) connect(ctx context.Context) error {
	if pending := s.repairsPending.Load(); pending > 0 {
		kv, err := s.bound(ctx)
		if err != nil {
			return errors.Join(errRepairPending, err)
		}
		if _, err := kv.Put(ctx, InvalidationKey, []byte("1")); err != nil {
			return errors.Join(errRepairPending, err)
		}
		// A Set that failed meanwhile keeps its own repair pending.
		s.repairsPending.CompareAndSwap(pending, 0)
	}
	if err := s.HealthCheck(ctx); err != nil {
		return err
	}
	if s.repairsPending.Load() > 0 {
		return errRepairPending
	}
	return nil
}

func (s *natsKVStore) bound(ctx context.Context) (jetstream.KeyValue, error) {
	s.mu.RLock()
	kv := s.kv
	s.mu.RUnlock()
	if kv != nil {
		return kv, nil
	}

	kv, err := s.js.KeyValue(ctx, s.bucket)
	if err != nil {
		return nil, fmt.Errorf("bind KV bucket %q: %w", s.bucket, err)
	}
	s.mu.Lock()
	s.kv = kv
	s.mu.Unlock()
	return kv, nil
}

func (s *natsKVStore) HealthCheck(ctx context.Context) error {
	kv, err := s.bound(ctx)
	if err != nil {
		return err
	}
	if _, err := kv.Status(ctx); err != nil {
		return fmt.Errorf("KV bucket %q status: %w", s.bucket, err)
	}
	return nil
}

// CurrentMarker also records the bucket's last sequence, read first: every
// marker written after it has a higher revision, which is how Seed tells that
// a marker moved during the write that followed. The sequence comes from a
// stream handle of its own: kv.Status rewrites stream info that concurrent
// Gets on the shared handle read.
func (s *natsKVStore) CurrentMarker(ctx context.Context) (contracts.InvalidationMarker, error) {
	kv, err := s.bound(ctx)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	stream, err := s.js.Stream(ctx, "KV_"+s.bucket)
	if err != nil {
		return contracts.InvalidationMarker{}, fmt.Errorf("KV bucket %q stream info: %w", s.bucket, err)
	}
	m, err := s.invMarker(ctx, kv)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	return m.WithStartSeq(stream.CachedInfo().State.LastSeq), nil
}

func (s *natsKVStore) invMarker(ctx context.Context, kv jetstream.KeyValue) (contracts.InvalidationMarker, error) {
	entry, err := kv.Get(ctx, InvalidationKey)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		// No invalidation within the bucket TTL: every entry is fresh.
		return contracts.NewTimeMarker(time.Time{}), nil
	case err != nil:
		return contracts.InvalidationMarker{}, err
	}
	return contracts.NewTimeMarker(entry.Created()).WithRevision(entry.Revision()), nil
}

func (s *natsKVStore) Get(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) (map[string]bool, error) {
	if s.repairsPending.Load() > 0 {
		return nil, errRepairPending
	}
	kv, err := s.bound(ctx)
	if err != nil {
		return nil, err
	}
	found := make(map[string]bool, len(relationKeys))
	for _, relationKey := range relationKeys {
		entry, err := kv.Get(ctx, CacheKey(relationKey))
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if m.Time().After(entry.Created()) {
			continue
		}
		switch string(entry.Value()) {
		case "true":
			found[relationKey] = true
		case "false":
			found[relationKey] = false
		}
	}
	return found, nil
}

// Set does not keep results computed before an invalidation that landed after
// m: their creation time alone would make them look fresh. It skips writing
// when the marker has already moved, and hides what it wrote when the marker
// moved during the writes. An invalidation landing between the first marker
// read and the writes leaves the result readable until it is hidden; this is
// stricter than lfx-v2-fga-sync, which keeps such a result until the TTL.
func (s *natsKVStore) Set(ctx context.Context, results map[string]bool, m contracts.InvalidationMarker) error {
	kv, err := s.bound(ctx)
	if err != nil {
		return err
	}
	current, err := s.invMarker(ctx, kv)
	if err != nil {
		return err
	}
	if current.Time().After(m.Time()) {
		return nil
	}

	// A Put that fails can still have been stored, so a key is tracked
	// before it is written.
	attempted := make([]string, 0, len(results))
	var writeErr error
	for relationKey, allowed := range results {
		cacheKey := CacheKey(relationKey)
		attempted = append(attempted, cacheKey)
		if _, writeErr = kv.Put(ctx, cacheKey, []byte(strconv.FormatBool(allowed))); writeErr != nil {
			break
		}
	}
	if s.afterWrites != nil {
		s.afterWrites()
	}
	if writeErr != nil {
		// The failed Put may be stored after any marker read made now.
		return errors.Join(writeErr, s.hide(ctx, kv, attempted))
	}
	current, err = s.invMarker(ctx, kv)
	if err == nil && !current.Time().After(m.Time()) {
		return nil
	}
	return errors.Join(err, s.hide(ctx, kv, attempted))
}

// hide makes cacheKeys unreadable. It removes them, or else rewrites the
// marker, which makes every entry stale as lfx-v2-fga-sync does after a write
// it is unsure of. If both fail the store stays unusable until connect
// rewrites the marker. Each step gets its own budget, since the caller's may
// be spent.
func (s *natsKVStore) hide(ctx context.Context, kv jetstream.KeyValue, cacheKeys []string) error {
	removeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), repairTimeout)
	removeErr := s.remove(removeCtx, kv, cacheKeys)
	cancel()
	if removeErr == nil {
		return nil
	}
	markerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), repairTimeout)
	defer cancel()
	if _, err := kv.Put(markerCtx, InvalidationKey, []byte("1")); err != nil {
		s.repairsPending.Add(1)
		return errors.Join(errRepairPending, removeErr, err)
	}
	return nil
}

func (s *natsKVStore) remove(ctx context.Context, kv jetstream.KeyValue, cacheKeys []string) error {
	var errs []error
	for _, cacheKey := range cacheKeys {
		if err := kv.Delete(ctx, cacheKey); err != nil {
			errs = append(errs, fmt.Errorf("remove stale result: %w", err))
		}
	}
	return errors.Join(errs...)
}

// Invalidate returns the marker of its own write. Reading the latest marker
// instead could return a later writer's invalidation, and a seed under that
// marker would survive it. It replaces inv only if inv is still the entry
// before was read from; if that fails, another invalidation landed (or the
// store failed, which the unconditional Put then reports) and it writes inv
// anyway.
func (s *natsKVStore) Invalidate(
	ctx context.Context, before contracts.InvalidationMarker,
) (contracts.InvalidationMarker, bool, error) {
	kv, err := s.bound(ctx)
	if err != nil {
		return contracts.InvalidationMarker{}, false, err
	}
	var revision uint64
	if before.Revision() == 0 {
		revision, err = kv.Create(ctx, InvalidationKey, []byte("1"))
	} else {
		revision, err = kv.Update(ctx, InvalidationKey, []byte("1"), before.Revision())
	}
	onlyMine := err == nil
	if !onlyMine {
		revision, err = kv.Put(ctx, InvalidationKey, []byte("1"))
		if err != nil {
			return contracts.InvalidationMarker{}, false, err
		}
	}
	m, err := s.markerAt(ctx, kv, revision)
	if err != nil {
		return contracts.InvalidationMarker{}, false, err
	}
	return m.WithStartSeq(before.StartSeq()), onlyMine, nil
}

// markerAt returns the marker written at revision. When a later invalidation
// has already replaced it (a bucket that keeps one revision), it returns a
// marker just before the current one: never earlier than the replaced write,
// so Get stays strict, and older than the current marker, so Set discards a
// seed made under it.
func (s *natsKVStore) markerAt(ctx context.Context, kv jetstream.KeyValue, revision uint64) (contracts.InvalidationMarker, error) {
	entry, err := kv.GetRevision(ctx, InvalidationKey, revision)
	if err == nil {
		return contracts.NewTimeMarker(entry.Created()), nil
	}
	if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return contracts.InvalidationMarker{}, fmt.Errorf("read own invalidation marker: %w", err)
	}
	current, err := s.invMarker(ctx, kv)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	if current.Time().IsZero() {
		return contracts.InvalidationMarker{}, errors.New("invalidation marker disappeared after it was written")
	}
	return contracts.NewTimeMarker(current.Time().Add(-time.Nanosecond)), nil
}

// Seed checks lfx-v2-fga-sync's scoped markers for the keys, and inv.model,
// before and after its writes, as Set does for inv: fga-sync's lookups honor
// them, so a revocation recorded only there must not be undone by the seed.
// m.StartSeq is the bucket sequence read before the write.
func (s *natsKVStore) Seed(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) error {
	kv, err := s.bound(ctx)
	if err != nil {
		return err
	}
	markers := scopedMarkerKeys(relationKeys)
	overtaken, err := s.seedOvertaken(ctx, kv, markers, m.StartSeq())
	if err != nil {
		return err
	}
	if overtaken {
		return errSeedOvertaken
	}
	results := make(map[string]bool, len(relationKeys))
	cacheKeys := make([]string, 0, len(relationKeys))
	for _, relationKey := range relationKeys {
		results[relationKey] = true
		cacheKeys = append(cacheKeys, CacheKey(relationKey))
	}
	if err := s.Set(ctx, results, m); err != nil {
		return err
	}
	overtaken, err = s.seedOvertaken(ctx, kv, markers, m.StartSeq())
	if err == nil && !overtaken {
		return nil
	}
	if hideErr := s.hide(ctx, kv, cacheKeys); hideErr != nil || err != nil {
		return errors.Join(err, hideErr)
	}
	return errSeedOvertaken
}

// seedOvertaken reports whether a marker in markers was written after
// startSeq, or inv.model names another model than the pinned one. A missing
// marker has not moved, and a missing inv.model matches every model unless
// another model has been seen since modelID was last announced.
func (s *natsKVStore) seedOvertaken(
	ctx context.Context, kv jetstream.KeyValue, markers []string, startSeq uint64,
) (bool, error) {
	for _, key := range markers {
		entry, err := kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("read scoped invalidation marker: %w", err)
		}
		if entry.Revision() > startSeq {
			return true, nil
		}
	}
	if s.modelID == "" {
		return false, nil
	}
	entry, err := kv.Get(ctx, ModelMarkerKey)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		return s.sawOtherModel.Load(), nil
	case err != nil:
		return false, fmt.Errorf("read model marker: %w", err)
	}
	other := string(entry.Value()) != s.modelID
	s.sawOtherModel.Store(other)
	return other, nil
}
