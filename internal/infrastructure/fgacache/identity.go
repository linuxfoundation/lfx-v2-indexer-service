// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/valkey-io/valkey-go"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

const identityKeyPrefix = "identity."

// identityEntry has no tokens or display profile. NATS KV's shared bucket has
// bucket-wide retention, so expiry is enforced on read; Valkey also expires the
// key server-side. Neither backend consults the permission invalidation marker.
type identityEntry struct {
	contracts.IdentityLookup
	ExpiresAt time.Time `json:"expires_at"`
}

func identityKey(principal string) (string, error) {
	if len(principal) == 0 || len(principal) > 256 {
		return "", fmt.Errorf("invalid identity principal")
	}
	for _, r := range principal {
		if r < 0x21 || r > 0x7e || strings.ContainsRune(":#@{}", r) {
			return "", fmt.Errorf("invalid identity principal")
		}
	}
	return identityKeyPrefix + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(principal)), nil
}

func identityTTL(result contracts.IdentityLookup) time.Duration {
	if result.Found {
		return contracts.IdentityFoundTTL
	}
	return contracts.IdentityNotFoundTTL
}

func encodeIdentity(result contracts.IdentityLookup, now time.Time) ([]byte, error) {
	if !result.Found && result.Email != nil {
		return nil, fmt.Errorf("absent identity must not contain email")
	}
	return json.Marshal(identityEntry{IdentityLookup: result, ExpiresAt: now.Add(identityTTL(result)).UTC()})
}

func decodeIdentity(payload []byte, now time.Time) (contracts.IdentityLookup, bool) {
	// Treat malformed or old-format entries as misses, never as deleted users.
	if len(payload) > 4096 {
		return contracts.IdentityLookup{}, false
	}
	var entry identityEntry
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&entry) != nil || decoder.Decode(new(any)) != io.EOF ||
		!entry.ExpiresAt.After(now) || entry.ExpiresAt.After(now.Add(identityTTL(entry.IdentityLookup))) ||
		(!entry.Found && entry.Email != nil) {
		return contracts.IdentityLookup{}, false
	}
	// Required fields distinguish a cached not-found result from a malformed
	// object with only an expiry timestamp.
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil || string(fields["found"]) == "null" ||
		len(fields["found"]) == 0 || len(fields["email"]) == 0 {
		return contracts.IdentityLookup{}, false
	}
	return entry.IdentityLookup, true
}

func (s *natsKVStore) GetIdentity(ctx context.Context, principal string) (contracts.IdentityLookup, bool, error) {
	key, err := identityKey(principal)
	if err != nil {
		return contracts.IdentityLookup{}, false, err
	}
	kv, err := s.bound(ctx)
	if err != nil {
		return contracts.IdentityLookup{}, false, err
	}
	entry, err := kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return contracts.IdentityLookup{}, false, nil
	}
	if err != nil {
		return contracts.IdentityLookup{}, false, err
	}
	result, hit := decodeIdentity(entry.Value(), time.Now())
	return result, hit, nil
}

func (s *natsKVStore) SetIdentity(ctx context.Context, principal string, result contracts.IdentityLookup) error {
	key, err := identityKey(principal)
	if err != nil {
		return err
	}
	payload, err := encodeIdentity(result, time.Now())
	if err != nil {
		return err
	}
	kv, err := s.bound(ctx)
	if err != nil {
		return err
	}
	_, err = kv.Put(ctx, key, payload)
	return err
}

func (s *valkeyStore) GetIdentity(ctx context.Context, principal string) (contracts.IdentityLookup, bool, error) {
	key, err := identityKey(principal)
	if err != nil {
		return contracts.IdentityLookup{}, false, err
	}
	client, err := s.connected(ctx)
	if err != nil {
		return contracts.IdentityLookup{}, false, err
	}
	payload, err := client.Do(ctx, client.B().Get().Key(key).Build()).AsBytes()
	if valkey.IsValkeyNil(err) {
		return contracts.IdentityLookup{}, false, nil
	}
	if err != nil {
		return contracts.IdentityLookup{}, false, err
	}
	result, hit := decodeIdentity(payload, time.Now())
	return result, hit, nil
}

func (s *valkeyStore) SetIdentity(ctx context.Context, principal string, result contracts.IdentityLookup) error {
	key, err := identityKey(principal)
	if err != nil {
		return err
	}
	payload, err := encodeIdentity(result, time.Now())
	if err != nil {
		return err
	}
	client, err := s.connected(ctx)
	if err != nil {
		return err
	}
	return client.Do(ctx, client.B().Set().Key(key).Value(string(payload)).Px(identityTTL(result)).Build()).Error()
}

// identityStoreError hides driver error text, which may contain a cache key or
// value, from the shared fail-safe logger while preserving error inspection.
type identityStoreError struct{ cause error }

func (e identityStoreError) Error() string { return "identity cache operation failed" }
func (e identityStoreError) Unwrap() error { return e.cause }

func (c *failSafe) GetIdentity(ctx context.Context, principal string) (contracts.IdentityLookup, bool, error) {
	var result contracts.IdentityLookup
	var hit bool
	inner, ok := c.inner.(contracts.IdentityCache)
	if !ok {
		return result, false, contracts.ErrCacheBypassed
	}
	err := c.call(ctx, func(ctx context.Context) error {
		var err error
		result, hit, err = inner.GetIdentity(ctx, principal)
		if err != nil {
			return identityStoreError{cause: err}
		}
		return nil
	})
	if err != nil {
		return contracts.IdentityLookup{}, false, err
	}
	return result, hit, nil
}

func (c *failSafe) SetIdentity(ctx context.Context, principal string, result contracts.IdentityLookup) error {
	inner, ok := c.inner.(contracts.IdentityCache)
	if !ok {
		return contracts.ErrCacheBypassed
	}
	return c.call(ctx, func(ctx context.Context) error {
		if err := inner.SetIdentity(ctx, principal, result); err != nil {
			return identityStoreError{cause: err}
		}
		return nil
	})
}

func (noneCache) GetIdentity(context.Context, string) (contracts.IdentityLookup, bool, error) {
	return contracts.IdentityLookup{}, false, contracts.ErrCacheBypassed
}

func (noneCache) SetIdentity(context.Context, string, contracts.IdentityLookup) error {
	return contracts.ErrCacheBypassed
}
