// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
)

// valkeyAddr returns TEST_VALKEY_ADDR or skips. The tests flush the server's
// current database, so it must be a disposable instance.
func valkeyAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("TEST_VALKEY_ADDR")
	if addr == "" {
		t.Skip("TEST_VALKEY_ADDR is not set")
	}
	return addr
}

func valkeyConfig(addr string) config.CacheConfig {
	return config.CacheConfig{
		Backend:       config.CacheBackendValkey,
		Timeout:       2 * time.Second,
		RetryInterval: 50 * time.Millisecond,
		TTL:           time.Hour,
		ValkeyAddr:    addr,
	}
}

func rawValkey(t *testing.T, addr string) valkey.Client {
	t.Helper()
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}, DisableCache: true, ForceSingleClient: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func freshValkeyStore(t *testing.T, addr string) *valkeyStore {
	t.Helper()
	raw := rawValkey(t, addr)
	require.NoError(t, raw.Do(testContext(t), raw.B().Flushdb().Build()).Error())
	s := newValkeyStore(valkeyConfig(addr))
	t.Cleanup(s.close)
	require.NoError(t, s.connect(testContext(t)))
	return s
}

// unusedAddr returns a local address with nothing listening on it.
func unusedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func TestValkey_Conformance(t *testing.T) {
	addr := valkeyAddr(t)

	runConformance(t, conformanceBackend{
		fresh: func(t *testing.T) contracts.FGACache {
			return freshValkeyStore(t, addr)
		},
		unreachable: func(t *testing.T) contracts.FGACache {
			s := newValkeyStore(valkeyConfig(unusedAddr(t)))
			t.Cleanup(s.close)
			return s
		},
	})
}

func TestValkey_EntriesExpireAndTheMarkerNever(t *testing.T) {
	addr := valkeyAddr(t)
	ctx := testContext(t)
	s := freshValkeyStore(t, addr)
	raw := rawValkey(t, addr)

	m, err := s.CurrentMarker(ctx)
	require.NoError(t, err)
	require.NoError(t, s.Set(ctx, map[string]bool{"obj:1#viewer@user:a": true, "obj:1#viewer@user:b": false}, m))
	seedMarker, err := s.Invalidate(ctx)
	require.NoError(t, err)
	require.NoError(t, s.Seed(ctx, []string{"obj:1#writer@user:a"}, seedMarker))

	keys, err := raw.Do(ctx, raw.B().Keys().Pattern(relationKeyPrefix+"*").Build()).AsStrSlice()
	require.NoError(t, err)
	require.Len(t, keys, 3)
	for _, key := range keys {
		ttl, err := raw.Do(ctx, raw.B().Pttl().Key(key).Build()).AsInt64()
		require.NoError(t, err)
		assert.Positive(t, ttl, "%s has a TTL", key)
		assert.LessOrEqual(t, ttl, time.Hour.Milliseconds())
	}
	ttl, err := raw.Do(ctx, raw.B().Ttl().Key(InvalidationKey).Build()).AsInt64()
	require.NoError(t, err)
	assert.Equal(t, int64(-1), ttl, "the marker has no TTL")
}

func TestValkey_InvalidationBetweenMarkerReadAndSetLeavesResultStale(t *testing.T) {
	addr := valkeyAddr(t)
	ctx := testContext(t)
	s := freshValkeyStore(t, addr)
	raw := rawValkey(t, addr)

	beforeCheck, err := s.CurrentMarker(ctx)
	require.NoError(t, err)
	require.NoError(t, raw.Do(ctx, raw.B().Incr().Key(InvalidationKey).Build()).Error())

	require.NoError(t, s.Set(ctx, map[string]bool{"obj:2#writer@user:a": true}, beforeCheck))

	err = raw.Do(ctx, raw.B().Get().Key(CacheKey("obj:2#writer@user:a")).Build()).Error()
	assert.True(t, valkey.IsValkeyNil(err), "a result computed before the invalidation is not written: %v", err)

	current, err := s.CurrentMarker(ctx)
	require.NoError(t, err)
	got, err := s.Get(ctx, []string{"obj:2#writer@user:a"}, current)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestValkey_OnlyExactTrueOrFalseIsAResult(t *testing.T) {
	addr := valkeyAddr(t)
	ctx := testContext(t)
	s := freshValkeyStore(t, addr)
	raw := rawValkey(t, addr)

	m, err := s.CurrentMarker(ctx)
	require.NoError(t, err)
	prefix := strconv.FormatInt(m.Counter(), 10)
	values := []string{"true", prefix, prefix + ":1", prefix + ":TRUE", prefix + ":true:x", "0" + prefix + ":true"}
	relationKeys := make([]string, len(values))
	for i, value := range values {
		relationKeys[i] = "obj:3#writer@user:" + strconv.Itoa(i)
		require.NoError(t, raw.Do(ctx, raw.B().Set().Key(CacheKey(relationKeys[i])).Value(value).Build()).Error())
	}

	got, err := s.Get(ctx, relationKeys, m)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestValkey_UnreachableAtStartBypassesThenRecovers(t *testing.T) {
	target := valkeyAddr(t)
	addr := unusedAddr(t)
	logger, buf := newTestLogger()
	s := newValkeyStore(valkeyConfig(addr))
	t.Cleanup(s.close)

	c := newFailSafe(s, time.Second, 50*time.Millisecond, logger)
	defer c.Close()

	_, err := c.CurrentMarker(testContext(t))
	require.ErrorIs(t, err, contracts.ErrCacheBypassed)
	require.Eventually(t, func() bool { return buf.warnCount() > 0 }, 2*time.Second, 5*time.Millisecond)

	forward(t, addr, target)
	require.Eventually(t, c.isReady, 5*time.Second, 5*time.Millisecond)
	_, err = c.CurrentMarker(testContext(t))
	require.NoError(t, err)
}

// forward starts relaying connections on addr to target, as if the store had
// just come up at addr.
func forward(t *testing.T, addr, target string) {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				continue
			}
			go relay(conn, target)
		}
	}()
}

func relay(conn net.Conn, target string) {
	defer func() { _ = conn.Close() }()
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, upstream); done <- struct{}{} }()
	<-done
}

func TestValkey_WriteFromBeforeARestartNeverBecomesFresh(t *testing.T) {
	addr := valkeyAddr(t)
	ctx := testContext(t)
	s := freshValkeyStore(t, addr)
	raw := rawValkey(t, addr)
	incr := func() {
		require.NoError(t, raw.Do(ctx, raw.B().Incr().Key(InvalidationKey).Build()).Error())
	}

	incr()
	incr()
	beforeRestart, err := s.CurrentMarker(ctx)
	require.NoError(t, err)
	require.NoError(t, raw.Do(ctx, raw.B().Flushdb().Build()).Error())

	require.NoError(t, s.Set(ctx, map[string]bool{"obj:3#writer@user:a": true}, beforeRestart))
	incr()
	incr()

	current, err := s.CurrentMarker(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeRestart.Counter(), current.Counter(), "the counter is back at the pre-restart value")
	got, err := s.Get(ctx, []string{"obj:3#writer@user:a"}, current)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestValkeyStore_ConnectsWithURLAddress(t *testing.T) {
	cfg, reason := valkeyConfig("redis://" + valkeyAddr(t)).Normalize()
	require.Empty(t, reason)

	s := newValkeyStore(cfg)
	t.Cleanup(s.close)
	require.NoError(t, s.connect(testContext(t)))
	_, err := s.Invalidate(testContext(t))
	require.NoError(t, err)
}
