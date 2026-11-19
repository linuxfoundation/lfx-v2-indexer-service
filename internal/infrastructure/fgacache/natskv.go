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

// natsKVStore is the NATS JetStream KV backend. It binds the bucket lazily,
// so a missing bucket is reported as an error instead of failing startup.
type natsKVStore struct {
	js     jetstream.JetStream
	bucket string

	mu sync.RWMutex
	kv jetstream.KeyValue
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

// Set writes results, then removes them again if an invalidation landed after
// m: those results were computed before it and may predate a revocation, yet
// their creation time alone would make them look fresh.
func (s *natsKVStore) Set(ctx context.Context, results map[string]bool, m contracts.InvalidationMarker) error {
	kv, err := s.bound(ctx)
	if err != nil {
		return err
	}
	for relationKey, allowed := range results {
		if _, err := kv.Put(ctx, CacheKey(relationKey), []byte(strconv.FormatBool(allowed))); err != nil {
			return err
		}
	}

	current, err := s.CurrentMarker(ctx)
	if err != nil {
		return err
	}
	if !current.Time().After(m.Time()) {
		return nil
	}
	for relationKey := range results {
		if err := kv.Delete(ctx, CacheKey(relationKey)); err != nil {
			return err
		}
	}
	return nil
}

func (s *natsKVStore) Invalidate(ctx context.Context) (contracts.InvalidationMarker, error) {
	kv, err := s.bound(ctx)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	if _, err := kv.Put(ctx, InvalidationKey, []byte("1")); err != nil {
		return contracts.InvalidationMarker{}, err
	}
	return s.CurrentMarker(ctx)
}

func (s *natsKVStore) Seed(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) error {
	results := make(map[string]bool, len(relationKeys))
	for _, relationKey := range relationKeys {
		results[relationKey] = true
	}
	return s.Set(ctx, results, m)
}
