// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
)

type identityBackend interface {
	contracts.FGACache
	contracts.IdentityCache
}

func runIdentityConformance(t *testing.T, fresh func(*testing.T) identityBackend) {
	t.Helper()
	for _, result := range []contracts.IdentityLookup{
		{Found: true, Email: stringPointer("user-1@example.com")},
		{Found: true},
		{Found: false},
	} {
		t.Run(fmt.Sprintf("found=%t,email=%t", result.Found, result.Email != nil), func(t *testing.T) {
			ctx := testContext(t)
			c := fresh(t)
			got, hit, err := c.GetIdentity(ctx, "testuser01")
			require.NoError(t, err)
			assert.False(t, hit, "a missing cache key is not a deleted user")
			assert.Equal(t, contracts.IdentityLookup{}, got)

			require.NoError(t, c.SetIdentity(ctx, "testuser01", result))
			got, hit, err = c.GetIdentity(ctx, "testuser01")
			require.NoError(t, err)
			assert.True(t, hit)
			assert.Equal(t, result, got)

			_, err = invalidate(ctx, c)
			require.NoError(t, err)
			got, hit, err = c.GetIdentity(ctx, "testuser01")
			require.NoError(t, err)
			assert.True(t, hit, "FGA invalidation must not expire identity")
			assert.Equal(t, result, got)

			_, hit, err = c.GetIdentity(ctx, "testuser02")
			require.NoError(t, err)
			assert.False(t, hit, "different principals must not share identity")
		})
	}

	t.Run("permission and identity entries are independent", func(t *testing.T) {
		ctx := testContext(t)
		c := fresh(t)
		marker, err := c.CurrentMarker(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Set(ctx, map[string]bool{"testuser01": true}, marker))
		require.NoError(t, c.SetIdentity(ctx, "testuser01", contracts.IdentityLookup{Found: false}))
		got, err := c.Get(ctx, []string{"testuser01"}, marker)
		require.NoError(t, err)
		assert.Equal(t, map[string]bool{"testuser01": true}, got)
	})

	t.Run("concurrent writers", func(t *testing.T) {
		ctx := testContext(t)
		c := fresh(t)
		var wg sync.WaitGroup
		for i := range 12 {
			wg.Go(func() {
				principal := fmt.Sprintf("testuser%02d", i)
				result := contracts.IdentityLookup{Found: i%2 == 0}
				assert.NoError(t, c.SetIdentity(ctx, principal, result))
				got, hit, err := c.GetIdentity(ctx, principal)
				assert.NoError(t, err)
				assert.True(t, hit)
				assert.Equal(t, result, got)
			})
		}
		wg.Wait()
	})
}

func stringPointer(value string) *string { return &value }

func TestNATSKV_Identity(t *testing.T) {
	js := connectJetStream(t, startJetStream(t))
	runIdentityConformance(t, func(t *testing.T) identityBackend {
		name := newBucketName()
		createBucket(t, js, name)
		return newNATSKVStore(js, name)
	})
}

func TestValkey_Identity(t *testing.T) {
	addr := valkeyAddr(t)
	runIdentityConformance(t, func(t *testing.T) identityBackend { return freshValkeyStore(t, addr) })
}

func TestIdentityEncoding(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	for _, found := range []bool{false, true} {
		t.Run(fmt.Sprintf("found=%t", found), func(t *testing.T) {
			result := contracts.IdentityLookup{Found: found}
			if found {
				result.Email = stringPointer("user-1@example.com")
			}
			payload, err := encodeIdentity(result, now)
			require.NoError(t, err)
			got, hit := decodeIdentity(payload, now)
			assert.True(t, hit)
			assert.Equal(t, result, got)
			_, hit = decodeIdentity(payload, now.Add(identityTTL(result)-time.Nanosecond))
			assert.True(t, hit)
			_, hit = decodeIdentity(payload, now.Add(identityTTL(result)))
			assert.False(t, hit, "entry expires exactly at its TTL")
			_, hit = decodeIdentity(payload, now.Add(-time.Second))
			assert.False(t, hit, "a future timestamp must not extend maximum TTL")
		})
	}
	_, err := encodeIdentity(contracts.IdentityLookup{Email: stringPointer("user-1@example.com")}, now)
	assert.Error(t, err)

	valid, err := encodeIdentity(contracts.IdentityLookup{Found: true}, now)
	require.NoError(t, err)
	for _, payload := range []string{
		`{}`, `null`, `false`, `{"expires_at":"2026-11-19T12:01:00Z"}`,
		`{"found":null,"email":null,"expires_at":"2026-11-19T12:01:00Z"}`,
		`{"found":false,"email":"user-1@example.com","expires_at":"2026-11-19T12:01:00Z"}`,
		strings.Replace(string(valid), `"found":true`, `"name":"Test User 1","found":true`, 1),
		string(valid) + `{}`, strings.Repeat("x", 4097),
	} {
		_, hit := decodeIdentity([]byte(payload), now)
		assert.False(t, hit, "malformed cache value must be a miss")
	}
}

func TestIdentityKey(t *testing.T) {
	key, err := identityKey("testuser01")
	require.NoError(t, err)
	assert.Equal(t, "identity.ORSXG5DVONSXEMBR", key)
	assert.NotEqual(t, CacheKey("testuser01"), key)
	for _, principal := range []string{"", "user:testuser01", "test user", "testuser@clients", "testuser\n", "é", strings.Repeat("x", 257)} {
		_, err := identityKey(principal)
		assert.EqualError(t, err, "invalid identity principal")
	}
}

func TestNATSKV_IdentityExpiry(t *testing.T) {
	ctx := testContext(t)
	js := connectJetStream(t, startJetStream(t))
	name := newBucketName()
	kv := createBucket(t, js, name)
	s := newNATSKVStore(js, name)
	key, err := identityKey("testuser01")
	require.NoError(t, err)
	for _, found := range []bool{false, true} {
		result := contracts.IdentityLookup{Found: found}
		payload, err := encodeIdentity(result, time.Now().Add(-identityTTL(result)))
		require.NoError(t, err)
		_, err = kv.Put(ctx, key, payload)
		require.NoError(t, err)
		_, hit, err := s.GetIdentity(ctx, "testuser01")
		require.NoError(t, err)
		assert.False(t, hit, "bucket-wide TTL must not extend identity freshness")
	}
}

func TestValkey_IdentityExpiry(t *testing.T) {
	addr := valkeyAddr(t)
	ctx := testContext(t)
	s := freshValkeyStore(t, addr)
	raw := rawValkey(t, addr)
	for _, found := range []bool{false, true} {
		result := contracts.IdentityLookup{Found: found}
		require.NoError(t, s.SetIdentity(ctx, "testuser01", result))
		key, err := identityKey("testuser01")
		require.NoError(t, err)
		ttl, err := raw.Do(ctx, raw.B().Pttl().Key(key).Build()).AsInt64()
		require.NoError(t, err)
		assert.Positive(t, ttl)
		assert.LessOrEqual(t, ttl, identityTTL(result).Milliseconds())
		assert.Greater(t, ttl, (identityTTL(result) - time.Second).Milliseconds())
		// Also check logical expiry even if the server still holds the key.
		payload, err := encodeIdentity(result, time.Now().Add(-identityTTL(result)))
		require.NoError(t, err)
		require.NoError(t, raw.Do(ctx, raw.B().Set().Key(key).Value(string(payload)).Build()).Error())
		_, hit, err := s.GetIdentity(ctx, "testuser01")
		require.NoError(t, err)
		assert.False(t, hit)
	}
}

func TestIdentityFactoryAndFailure(t *testing.T) {
	logger, _ := newTestLogger()
	c := New(cacheConfig(config.CacheBackendNone), nil, logger)
	t.Cleanup(c.Close)
	_, hit, err := c.GetIdentity(testContext(t), "testuser01")
	assert.False(t, hit)
	assert.ErrorIs(t, err, contracts.ErrCacheBypassed)
	assert.ErrorIs(t, c.SetIdentity(testContext(t), "testuser01", contracts.IdentityLookup{}), contracts.ErrCacheBypassed)

	s := newValkeyStore(valkeyConfig(unusedAddr(t)))
	t.Cleanup(s.close)
	f := newFailSafe(s, time.Second, time.Hour, logger)
	t.Cleanup(f.Close)
	_, hit, err = f.GetIdentity(testContext(t), "testuser01")
	assert.False(t, hit)
	assert.ErrorIs(t, err, contracts.ErrCacheBypassed)
}

type failingIdentityStore struct {
	store
}

func (s failingIdentityStore) GetIdentity(context.Context, string) (contracts.IdentityLookup, bool, error) {
	return contracts.IdentityLookup{}, false, fmt.Errorf("sensitive-driver-value")
}

func (s failingIdentityStore) SetIdentity(context.Context, string, contracts.IdentityLookup) error {
	return fmt.Errorf("sensitive-driver-value")
}

func TestIdentityFailureDoesNotLogDriverData(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(fmt.Sprintf("write=%t", write), func(t *testing.T) {
			logger, buf := newTestLogger()
			c := &failSafe{
				inner: failingIdentityStore{}, timeout: time.Second, retry: time.Hour,
				logger: logger, metrics: defaultMetrics(), sem: make(chan struct{}, 1), down: make(chan struct{}, 1),
			}
			c.ready.Store(true)
			var err error
			if write {
				err = c.SetIdentity(testContext(t), "testuser01", contracts.IdentityLookup{})
			} else {
				_, _, err = c.GetIdentity(testContext(t), "testuser01")
			}
			assert.ErrorIs(t, err, contracts.ErrCacheBypassed)
			assert.False(t, c.ready.Load())
			buf.mu.Lock()
			defer buf.mu.Unlock()
			assert.NotContains(t, buf.buf.String(), "sensitive-driver-value")
		})
	}
}

func TestIdentityStoredShape(t *testing.T) {
	payload, err := encodeIdentity(contracts.IdentityLookup{Found: true, Email: stringPointer("user-1@example.com")}, time.Now())
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(payload, &fields))
	assert.Len(t, fields, 3, "cache retains only found, email and expiry")
	assert.Contains(t, fields, "found")
	assert.Contains(t, fields, "email")
	assert.Contains(t, fields, "expires_at")
}
