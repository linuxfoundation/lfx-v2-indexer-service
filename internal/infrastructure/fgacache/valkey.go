// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
)

// On Valkey the marker key "inv" is a counter raised with INCR and never
// given a TTL. Each entry stores the counter it was computed under
// ("<counter>:true" or "<counter>:false") and is fresh only while that equals
// the current counter. Results are written only while the counter still
// equals the one they were computed under.
//
// A missing counter (a new server, or one that lost its contents) starts at
// the server time in microseconds, never at 0. A server that lost its contents
// therefore never hands out a counter an earlier lifetime used, so a result
// computed before the loss can neither be written nor read as fresh after it.
// This holds while a replacement's clock, in microseconds, is ahead of the
// lost server's start by more than the invalidations that server recorded.

// valkeyStore is the Valkey backend. The client dials when it is created and
// fails when nothing is listening, so it is created on first use instead of
// at startup.
type valkeyStore struct {
	option valkey.ClientOption
	ttl    time.Duration

	// dialing admits one client creation at a time without ignoring the
	// caller's deadline the way a mutex wait would.
	dialing chan struct{}

	mu     sync.RWMutex
	client valkey.Client
}

func newValkeyStore(cfg config.CacheConfig) *valkeyStore {
	option := valkey.ClientOption{
		InitAddress: []string{cfg.ValkeyAddr},
		Username:    cfg.ValkeyUsername,
		Password:    cfg.ValkeyPassword,
		Dialer:      net.Dialer{Timeout: cfg.Timeout},
		// Client-side caching would track keys on the server; this cache
		// must always read the server's current value.
		DisableCache: true,
		// MGET spans hash slots, which a cluster rejects.
		ForceSingleClient: true,
	}
	if cfg.ValkeyTLS {
		option.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &valkeyStore{option: option, ttl: cfg.TTL, dialing: make(chan struct{}, 1)}
}

func (s *valkeyStore) connect(ctx context.Context) error {
	if _, err := s.connected(ctx); err != nil {
		return err
	}
	return s.HealthCheck(ctx)
}

// close releases the client, if one was created.
func (s *valkeyStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
}

func (s *valkeyStore) current() valkey.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

func (s *valkeyStore) connected(ctx context.Context) (valkey.Client, error) {
	if client := s.current(); client != nil {
		return client, nil
	}
	select {
	case s.dialing <- struct{}{}:
		defer func() { <-s.dialing }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if client := s.current(); client != nil {
		return client, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The client keeps its options for every reconnect, so the dial timeout
	// stays the configured one rather than this caller's remaining budget.
	client, err := valkey.NewClient(s.option)
	if err != nil {
		return nil, fmt.Errorf("connect to valkey: %w", err)
	}
	s.mu.Lock()
	s.client = client
	s.mu.Unlock()
	return client, nil
}

func (s *valkeyStore) HealthCheck(ctx context.Context) error {
	client, err := s.connected(ctx)
	if err != nil {
		return err
	}
	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		return fmt.Errorf("valkey ping: %w", err)
	}
	return nil
}

func (s *valkeyStore) CurrentMarker(ctx context.Context) (contracts.InvalidationMarker, error) {
	client, err := s.connected(ctx)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	counter, err := readMarker.Exec(ctx, client, []string{InvalidationKey}, nil).AsInt64()
	if err != nil {
		return contracts.InvalidationMarker{}, fmt.Errorf("read invalidation marker: %w", err)
	}
	return contracts.NewCounterMarker(counter), nil
}

// startMarker sets a missing KEYS[1] to the server time in microseconds and
// leaves it in v.
const startMarker = `
local v = redis.call('GET', KEYS[1])
if not v then
  local t = redis.call('TIME')
  v = t[1] .. string.format('%06d', tonumber(t[2]))
  redis.call('SET', KEYS[1], v)
end
`

var (
	readMarker  = valkey.NewLuaScript(startMarker + "return v")
	raiseMarker = valkey.NewLuaScript(startMarker + "return redis.call('INCR', KEYS[1])")
)

func (s *valkeyStore) Get(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) (map[string]bool, error) {
	found := make(map[string]bool, len(relationKeys))
	if len(relationKeys) == 0 {
		return found, nil
	}
	client, err := s.connected(ctx)
	if err != nil {
		return nil, err
	}
	cacheKeys := make([]string, len(relationKeys))
	for i, relationKey := range relationKeys {
		cacheKeys[i] = CacheKey(relationKey)
	}
	values, err := client.Do(ctx, client.B().Mget().Key(cacheKeys...).Build()).AsStrSlice()
	if err != nil {
		return nil, err
	}
	if len(values) != len(relationKeys) {
		return nil, fmt.Errorf("valkey MGET returned %d values for %d keys", len(values), len(relationKeys))
	}
	for i, value := range values {
		if allowed, ok := freshResult(value, m); ok {
			found[relationKeys[i]] = allowed
		}
	}
	return found, nil
}

// freshResult parses a stored value. A missing key arrives as "", which
// never matches.
func freshResult(value string, m contracts.InvalidationMarker) (allowed, ok bool) {
	counter, result, _ := strings.Cut(value, ":")
	if counter != strconv.FormatInt(m.Counter(), 10) {
		return false, false
	}
	switch result {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

func storedValue(m contracts.InvalidationMarker, allowed bool) string {
	return strconv.FormatInt(m.Counter(), 10) + ":" + strconv.FormatBool(allowed)
}

// setIfCurrent writes KEYS[2..] = ARGV[2..] with PX ARGV[#KEYS+1], only while
// KEYS[1] (inv) exists and still equals ARGV[1]. A missing inv means the
// server lost its contents after m was read, so nothing is written.
var setIfCurrent = valkey.NewLuaScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
for i = 2, #KEYS do
  redis.call('SET', KEYS[i], ARGV[i], 'PX', ARGV[#KEYS + 1])
end
return 1`)

func (s *valkeyStore) Set(ctx context.Context, results map[string]bool, m contracts.InvalidationMarker) error {
	if len(results) == 0 {
		return nil
	}
	client, err := s.connected(ctx)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(results)+1)
	args := make([]string, 0, len(results)+2)
	keys = append(keys, InvalidationKey)
	args = append(args, strconv.FormatInt(m.Counter(), 10))
	for relationKey, allowed := range results {
		keys = append(keys, CacheKey(relationKey))
		args = append(args, storedValue(m, allowed))
	}
	args = append(args, strconv.FormatInt(s.ttl.Milliseconds(), 10))
	if err := setIfCurrent.Exec(ctx, client, keys, args).Error(); err != nil {
		return fmt.Errorf("write cache results: %w", err)
	}
	return nil
}

// Invalidate returns the counter its own INCR produced, never a later
// writer's.
func (s *valkeyStore) Invalidate(ctx context.Context) (contracts.InvalidationMarker, error) {
	client, err := s.connected(ctx)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	counter, err := raiseMarker.Exec(ctx, client, []string{InvalidationKey}, nil).AsInt64()
	if err != nil {
		return contracts.InvalidationMarker{}, fmt.Errorf("raise invalidation marker: %w", err)
	}
	return contracts.NewCounterMarker(counter), nil
}

func (s *valkeyStore) Seed(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) error {
	results := make(map[string]bool, len(relationKeys))
	for _, relationKey := range relationKeys {
		results[relationKey] = true
	}
	return s.Set(ctx, results, m)
}
