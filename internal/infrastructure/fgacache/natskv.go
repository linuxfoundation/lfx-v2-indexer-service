// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
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

// removeTimeout bounds removing stale results after the caller's own deadline
// has passed. Removing a possibly revoked grant is worth this extra time on a
// path where the store is already failing.
const removeTimeout = time.Second

// natsKVStore is the NATS JetStream KV backend. It binds the bucket lazily,
// so a missing bucket is reported as an error instead of failing startup.
type natsKVStore struct {
	js     jetstream.JetStream
	bucket string

	mu sync.RWMutex
	kv jetstream.KeyValue

	// afterWrites, when set by tests, runs between Set's writes and its
	// second marker read.
	afterWrites func()
}

func newNATSKVStore(js jetstream.JetStream, bucket string) *natsKVStore {
	return &natsKVStore{js: js, bucket: bucket}
}

func (s *natsKVStore) connect(ctx context.Context) error {
	return s.HealthCheck(ctx)
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

func (s *natsKVStore) CurrentMarker(ctx context.Context) (contracts.InvalidationMarker, error) {
	kv, err := s.bound(ctx)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	entry, err := kv.Get(ctx, InvalidationKey)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		// No invalidation within the bucket TTL: every entry is fresh.
		return contracts.NewTimeMarker(time.Time{}), nil
	case err != nil:
		return contracts.InvalidationMarker{}, err
	}
	return contracts.NewTimeMarker(entry.Created()), nil
}

func (s *natsKVStore) Get(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) (map[string]bool, error) {
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
// when the marker has already moved, and removes what it wrote when the marker
// moved during the writes. An invalidation landing between the first marker
// read and the writes leaves the result readable until that removal; this is
// stricter than lfx-v2-fga-sync, which keeps such a result until the TTL.
func (s *natsKVStore) Set(ctx context.Context, results map[string]bool, m contracts.InvalidationMarker) error {
	kv, err := s.bound(ctx)
	if err != nil {
		return err
	}
	current, err := s.CurrentMarker(ctx)
	if err != nil {
		return err
	}
	if current.Time().After(m.Time()) {
		return nil
	}

	written := make([]string, 0, len(results))
	for relationKey, allowed := range results {
		cacheKey := CacheKey(relationKey)
		if _, err := kv.Put(ctx, cacheKey, []byte(strconv.FormatBool(allowed))); err != nil {
			return errors.Join(err, s.remove(ctx, kv, written))
		}
		written = append(written, cacheKey)
	}
	if s.afterWrites != nil {
		s.afterWrites()
	}

	current, err = s.CurrentMarker(ctx)
	if err == nil && !current.Time().After(m.Time()) {
		return nil
	}
	return errors.Join(err, s.remove(ctx, kv, written))
}

// remove deletes cacheKeys. An error is returned so the caller marks the
// store down: a result that could not be removed may be a revoked grant.
func (s *natsKVStore) remove(ctx context.Context, kv jetstream.KeyValue, cacheKeys []string) error {
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), removeTimeout)
		defer cancel()
	}
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
// marker would survive it.
func (s *natsKVStore) Invalidate(ctx context.Context) (contracts.InvalidationMarker, error) {
	kv, err := s.bound(ctx)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	revision, err := kv.Put(ctx, InvalidationKey, []byte("1"))
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	return s.markerAt(ctx, kv, revision)
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
	current, err := s.CurrentMarker(ctx)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	if current.Time().IsZero() {
		return contracts.InvalidationMarker{}, errors.New("invalidation marker disappeared after it was written")
	}
	return contracts.NewTimeMarker(current.Time().Add(-time.Nanosecond)), nil
}

func (s *natsKVStore) Seed(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) error {
	results := make(map[string]bool, len(relationKeys))
	for _, relationKey := range relationKeys {
		results[relationKey] = true
	}
	return s.Set(ctx, results, m)
}
