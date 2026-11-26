// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"encoding/base32"
	"strings"
)

// The key format and the last-invalidation marker semantics in this file are
// duplicated in lfx-v2-fga-sync/fga_cache.go and fga_invalidation.go. While
// both services read or write the same cache they must stay identical. Change
// them in both repos and upgrade both services together. fga-sync seeds without
// checking for other invalidations only because its tuple writes never overlap;
// the indexer's can, so it seeds only when its invalidation was the only one
// since its write began (FGACache.Invalidate), and only when no scoped marker
// that applies to a seeded key was written during the write and inv.model
// does not name another model than the one it writes with.

// InvalidationKey is the cache key of the last-invalidation marker.
const InvalidationKey = "inv"

// ModelMarkerKey holds the authorization model ID fga-sync last announced in
// scoped mode. A missing marker matches every model.
const ModelMarkerKey = "inv.model"

const (
	relationKeyPrefix = "rel."
	typeMarkerPrefix  = "inv.t."
	userMarkerPrefix  = "inv.u."
	userPrefix        = "user:"
	userWildcard      = "user:*"
)

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

// TypeMarkerKey returns the marker that makes every cached check on objects
// of typ stale.
func TypeMarkerKey(typ string) string {
	return typeMarkerPrefix + typ
}

// UserMarkerKey returns the marker that makes cached checks on objects of typ
// for one user stale.
func UserMarkerKey(typ, user string) string {
	return userMarkerPrefix + typ + "." + cacheKeyEncoder.EncodeToString([]byte(user))
}

// scopedMarkerKeys returns the distinct scoped markers fga-sync's lookups
// honor for relationKeys: one per object type, and one per object type and
// concrete user. A key whose object type fga-sync cannot scope has none.
func scopedMarkerKeys(relationKeys []string) []string {
	seen := make(map[string]struct{})
	var keys []string
	add := func(key string) {
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	for _, relationKey := range relationKeys {
		object, rest, _ := strings.Cut(relationKey, "#")
		_, user, _ := strings.Cut(rest, "@")
		typ, ok := objectType(object)
		if !ok {
			continue
		}
		add(TypeMarkerKey(typ))
		if isConcreteUser(user) {
			add(UserMarkerKey(typ, user))
		}
	}
	return keys
}

// objectType returns the type part of an OpenFGA object, or false when the
// object is malformed or its type has characters fga-sync does not scope.
func objectType(object string) (string, bool) {
	typ, id, found := strings.Cut(object, ":")
	if !found || typ == "" || id == "" {
		return "", false
	}
	for _, r := range typ {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return "", false
		}
	}
	return typ, true
}

// isConcreteUser reports whether subject names exactly one user, not a
// wildcard, a userset or another object.
func isConcreteUser(subject string) bool {
	if !strings.HasPrefix(subject, userPrefix) || subject == userWildcard {
		return false
	}
	return len(subject) > len(userPrefix) && !strings.Contains(subject, "#")
}
