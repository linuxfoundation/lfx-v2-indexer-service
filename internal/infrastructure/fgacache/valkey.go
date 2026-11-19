// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
)

// On Valkey the marker key "inv" is a hash, never given a TTL, of a random
// generation ("gen") and a counter ("n") raised with HINCRBY. Each entry stores
// the generation and counter it was computed under ("<gen>:<n>:true" or
// "<gen>:<n>:false") and is fresh only while both equal the current ones.
// Results are written only while both still equal the ones they were computed
// under.
//
// A missing inv (a new server, or one that lost its contents) starts a new
// random generation at counter 0. A result computed before the loss carries
// the old generation, so it can neither be written nor read as fresh after it,
// however far the new counter climbs.

// valkeyStore is the Valkey backend. The client dials when it is created and
// fails when nothing is listening, so it is created on first use instead of
// at startup.
type valkeyStore struct {
	option valkey.ClientOption
	ttl    time.Duration

	mu     sync.RWMutex
	client valkey.Client
	dial   *valkeyDial
	closed bool
}

// valkeyDial is one client creation in progress. client and err are set
// before done is closed.
type valkeyDial struct {
	done   chan struct{}
	client valkey.Client
	err    error
}

var errValkeyClosed = errors.New("valkey cache closed")

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
	return &valkeyStore{option: option, ttl: cfg.TTL}
}

func (s *valkeyStore) connect(ctx context.Context) error {
	if _, err := s.connected(ctx); err != nil {
		return err
	}
	return s.HealthCheck(ctx)
}

// close releases the client, if one was created. A creation still in
// progress closes its client when it finishes.
func (s *valkeyStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
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

// connected returns the client, starting its creation if none is in
// progress. valkey.NewClient takes no context, so creation runs on its own
// and the caller stops waiting when its context ends.
func (s *valkeyStore) connected(ctx context.Context) (valkey.Client, error) {
	if client := s.current(); client != nil {
		return client, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	switch {
	case s.client != nil:
		client := s.client
		s.mu.Unlock()
		return client, nil
	case s.closed:
		s.mu.Unlock()
		return nil, errValkeyClosed
	case s.dial == nil:
		s.dial = &valkeyDial{done: make(chan struct{})}
		go s.create(s.dial)
	}
	dial := s.dial
	s.mu.Unlock()

	select {
	case <-dial.done:
		return dial.client, dial.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// create builds the client for dial. The client keeps its options for every
// reconnect, so the dial timeout stays the configured one rather than a
// caller's remaining budget.
func (s *valkeyStore) create(dial *valkeyDial) {
	client, err := valkey.NewClient(s.option)
	s.mu.Lock()
	s.dial = nil
	switch {
	case err != nil:
		dial.err = fmt.Errorf("connect to valkey: %w", err)
	case s.closed:
		client.Close()
		dial.err = errValkeyClosed
	default:
		s.client = client
		dial.client = client
	}
	s.mu.Unlock()
	close(dial.done)
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
	m, err := runMarker(ctx, client, readMarker)
	if err != nil {
		return contracts.InvalidationMarker{}, fmt.Errorf("read invalidation marker: %w", err)
	}
	return m, nil
}

// startMarker reads KEYS[1] (inv) into m as {gen, n}, first starting a missing
// inv at generation ARGV[1] and counter 0.
const startMarker = `
local m = redis.call('HMGET', KEYS[1], 'gen', 'n')
if not m[1] or not m[2] then
  redis.call('HSET', KEYS[1], 'gen', ARGV[1], 'n', 0)
  m = {ARGV[1], '0'}
end
`

var (
	readMarker  = valkey.NewLuaScript(startMarker + "return m")
	raiseMarker = valkey.NewLuaScript(startMarker + "return {m[1], tostring(redis.call('HINCRBY', KEYS[1], 'n', 1))}")
)

// runMarker runs a marker script, offering a fresh generation in case inv is
// missing.
func runMarker(ctx context.Context, client valkey.Client, script *valkey.Lua) (contracts.InvalidationMarker, error) {
	reply, err := script.Exec(ctx, client, []string{InvalidationKey}, []string{rand.Text()}).AsStrSlice()
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	if len(reply) != 2 || reply[0] == "" {
		return contracts.InvalidationMarker{}, fmt.Errorf("unexpected invalidation marker %q", reply)
	}
	counter, err := strconv.ParseInt(reply[1], 10, 64)
	if err != nil {
		return contracts.InvalidationMarker{}, fmt.Errorf("parse invalidation counter: %w", err)
	}
	return contracts.NewCounterMarker(reply[0], counter), nil
}

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
	switch value {
	case storedValue(m, true):
		return true, true
	case storedValue(m, false):
		return false, true
	}
	return false, false
}

func storedValue(m contracts.InvalidationMarker, allowed bool) string {
	return m.Generation() + ":" + strconv.FormatInt(m.Counter(), 10) + ":" + strconv.FormatBool(allowed)
}

// setIfCurrent writes KEYS[i] = ARGV[i+1] for i >= 2 with PX ARGV[#KEYS+2],
// only while KEYS[1] (inv) exists with generation ARGV[1] and counter ARGV[2].
// A missing inv means the server lost its contents after m was read, so
// nothing is written.
var setIfCurrent = valkey.NewLuaScript(`
local m = redis.call('HMGET', KEYS[1], 'gen', 'n')
if m[1] ~= ARGV[1] or m[2] ~= ARGV[2] then return 0 end
for i = 2, #KEYS do
  redis.call('SET', KEYS[i], ARGV[i + 1], 'PX', ARGV[#KEYS + 2])
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
	args := make([]string, 0, len(results)+3)
	keys = append(keys, InvalidationKey)
	args = append(args, m.Generation(), strconv.FormatInt(m.Counter(), 10))
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

// Invalidate returns the counter its own HINCRBY produced, never a later
// writer's.
func (s *valkeyStore) Invalidate(ctx context.Context) (contracts.InvalidationMarker, error) {
	client, err := s.connected(ctx)
	if err != nil {
		return contracts.InvalidationMarker{}, err
	}
	m, err := runMarker(ctx, client, raiseMarker)
	if err != nil {
		return contracts.InvalidationMarker{}, fmt.Errorf("raise invalidation marker: %w", err)
	}
	return m, nil
}

func (s *valkeyStore) Seed(ctx context.Context, relationKeys []string, m contracts.InvalidationMarker) error {
	results := make(map[string]bool, len(relationKeys))
	for _, relationKey := range relationKeys {
		results[relationKey] = true
	}
	return s.Set(ctx, results, m)
}
