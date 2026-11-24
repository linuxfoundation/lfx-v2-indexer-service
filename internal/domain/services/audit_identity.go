// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// AuditIdentityResolver resolves retained identity only when constructing a
// first history append. Replay detection must happen before invoking it.
type AuditIdentityResolver struct {
	directory contracts.IdentityDirectory
	cache     contracts.IdentityCache
}

// NewAuditIdentityResolver composes primary-email lookup with an optional cache.
// Cache failures are misses and cache writes never determine delivery success.
func NewAuditIdentityResolver(directory contracts.IdentityDirectory, cache contracts.IdentityCache) (*AuditIdentityResolver, error) {
	if directory == nil {
		return nil, fmt.Errorf("audit identity directory is required")
	}
	return &AuditIdentityResolver{directory: directory, cache: cache}, nil
}

// Resolve retains the ref's username and primary email. matchedEmail must come
// only from a successfully validated token whose principal matches this ref,
// never from unverified claims, the publisher token, or another actor's token.
// A matched token without an email still requires directory/cache resolution.
func (r *AuditIdentityResolver) Resolve(ctx context.Context, actorRef string, matchedEmail *string) (*types.AuditActorIdentity, error) {
	if !types.ValidAuditActorRef(actorRef) {
		return nil, fmt.Errorf("invalid audit actor reference")
	}
	kind, principal, _ := strings.Cut(actorRef, ":")
	if kind == "service" {
		return nil, nil
	}
	if kind == "legacy" {
		// A legacy reference is source provenance, not a directory lookup key.
		// Ignore even a caller-supplied matched email; this actor is unresolved.
		return &types.AuditActorIdentity{}, nil
	}
	identity := &types.AuditActorIdentity{Username: &principal}
	if matchedEmail != nil && *matchedEmail != "" {
		email := *matchedEmail
		identity.Email = &email
		return identity, nil
	}
	if r.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, constants.AuditIdentityCacheTimeout)
		result, hit, err := r.cache.GetIdentity(cacheCtx, principal)
		cancel()
		if err == nil && hit && (result.Found || result.Email == nil) {
			identity.Email = copyIdentityEmail(result.Email)
			return identity, nil
		}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, constants.AuditIdentityLookupTimeout)
	result, err := r.directory.LookupIdentity(lookupCtx, principal)
	cancel()
	if err != nil {
		return nil, &contracts.IdentityLookupError{Cause: err}
	}
	if !result.Found && result.Email != nil {
		return nil, &contracts.IdentityLookupError{Cause: fmt.Errorf("invalid identity directory result")}
	}
	if r.cache != nil {
		// Only explicit directory success/absence reaches this write. A timeout,
		// no-responder or malformed reply must never become negative caching.
		cacheCtx, cancel := context.WithTimeout(ctx, constants.AuditIdentityCacheTimeout)
		_ = r.cache.SetIdentity(cacheCtx, principal, result)
		cancel()
	}
	identity.Email = copyIdentityEmail(result.Email)
	return identity, nil
}

func copyIdentityEmail(email *string) *string {
	if email == nil || *email == "" {
		return nil
	}
	copy := *email
	return &copy
}
