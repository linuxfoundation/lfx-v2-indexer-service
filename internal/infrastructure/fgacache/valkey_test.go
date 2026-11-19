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

func TestValkey_MissingMarkerStartsAtServerTime(t *testing.T) {
	addr := valkeyAddr(t)
	ctx := testContext(t)
	s := freshValkeyStore(t, addr)

	before := time.Now().Add(-time.Minute).UnixMicro()
	m, err := s.CurrentMarker(ctx)
	require.NoError(t, err)
	after := time.Now().Add(time.Minute).UnixMicro()
	assert.Greater(t, m.Counter(), before)
	assert.Less(t, m.Counter(), after)

	again, err := s.CurrentMarker(ctx)
	require.NoError(t, err)
	assert.Equal(t, m.Counter(), again.Counter(), "reading does not move the marker")
	raised, err := s.Invalidate(ctx)
	require.NoError(t, err)
	assert.Equal(t, m.Counter()+1, raised.Counter())
}

// A check reads the marker, computes "allowed", and is delayed. Access is
// revoked, Valkey loses its contents, and the delayed result arrives.
func TestValkey_ResultFromBeforeLostContentsNeverBecomesFresh(t *testing.T) {
	const relation = "obj:3#writer@user:a"
	for _, tt := range []struct {
		name string
		// lose runs between the revocation and the delayed write.
		lose func(t *testing.T, s *valkeyStore, raw valkey.Client)
	}{
		{"marker missing at the delayed write", func(t *testing.T, _ *valkeyStore, raw valkey.Client) {
			require.NoError(t, raw.Do(testContext(t), raw.B().Flushdb().Build()).Error())
		}},
		{"marker raised past the old value before the delayed write", func(t *testing.T, s *valkeyStore, raw valkey.Client) {
			require.NoError(t, raw.Do(testContext(t), raw.B().Flushdb().Build()).Error())
			for range 3 {
				_, err := s.Invalidate(testContext(t))
				require.NoError(t, err)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			addr := valkeyAddr(t)
			ctx := testContext(t)
			s := freshValkeyStore(t, addr)
			raw := rawValkey(t, addr)

			beforeCheck, err := s.CurrentMarker(ctx)
			require.NoError(t, err)
			revoked, err := s.Invalidate(ctx)
			require.NoError(t, err)
			// Many invalidations before the loss must still stay below the
			// next lifetime's start.
			require.NoError(t, raw.Do(ctx, raw.B().Incrby().Key(InvalidationKey).Increment(1000).Build()).Error())
			time.Sleep(5 * time.Millisecond)
			tt.lose(t, s, raw)

			require.NoError(t, s.Set(ctx, map[string]bool{relation: true}, beforeCheck))
			err = raw.Do(ctx, raw.B().Get().Key(CacheKey(relation)).Build()).Error()
			assert.True(t, valkey.IsValkeyNil(err), "the delayed result is not written: %v", err)

			current, err := s.CurrentMarker(ctx)
			require.NoError(t, err)
			assert.Greater(t, current.Counter(), revoked.Counter()+1000, "the new lifetime never repeats an old counter")
			got, err := s.Get(ctx, []string{relation}, current)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
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
