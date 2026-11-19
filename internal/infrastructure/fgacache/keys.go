// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import "encoding/base32"

// The key format and the last-invalidation marker semantics in this file are
// duplicated in lfx-v2-fga-sync/fga_cache.go. While both services read or
// write the same cache they must stay identical. Change them in both repos and
// upgrade both services together.

// InvalidationKey is the cache key of the last-invalidation marker.
const InvalidationKey = "inv"

const relationKeyPrefix = "rel."

// Unpadded base32 keeps cache keys within the characters NATS KV allows.
var cacheKeyEncoder = base32.StdEncoding.WithPadding(base32.NoPadding)

// RelationKey returns "<object>#<relation>@<user>".
func RelationKey(object, relation, user string) string {
	return object + "#" + relation + "@" + user
}

// CacheKey returns the store key for a relation key.
func CacheKey(relationKey string) string {
	return relationKeyPrefix + cacheKeyEncoder.EncodeToString([]byte(relationKey))
}
