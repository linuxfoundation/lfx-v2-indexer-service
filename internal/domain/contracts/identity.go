// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"context"
	"time"
)

// Identity cache lifetimes are independent of permission-cache invalidation.
const (
	IdentityFoundTTL    = 10 * time.Minute
	IdentityNotFoundTTL = time.Minute
)

// IdentityLookup contains only the primary email needed for retained audit
// identity. Found=false means an explicit user-not-found response, not an outage.
// Username is derived from the actor ref; display names and avatars are not kept.
type IdentityLookup struct {
	Found bool    `json:"found"`
	Email *string `json:"email"`
}

// IdentityDirectory resolves a user's primary email by Heimdall principal.
// Errors must not include directory responses or user identity in their text.
type IdentityDirectory interface {
	LookupIdentity(ctx context.Context, principal string) (IdentityLookup, error)
}

// IdentityLookupError causes the complete delivery to retry when a required
// directory lookup fails. Error text is safe for the compatibility reply and
// general logs, while Unwrap preserves the cause for internal classification.
type IdentityLookupError struct {
	Cause error
}

func (e *IdentityLookupError) Error() string { return "transient: identity lookup unavailable" }

// Unwrap exposes the original failure without including it in Error's text.
func (e *IdentityLookupError) Unwrap() error { return e.Cause }

// IdentityCache stores successful directory results, including explicit absence,
// in the selected cache backend. A cache miss is distinct from a not-found user.
// Errors are optimization failures: callers resolve directly from the directory.
// Implementations enforce the fixed positive/negative TTLs and ignore FGA inv.
type IdentityCache interface {
	GetIdentity(ctx context.Context, principal string) (result IdentityLookup, hit bool, err error)
	SetIdentity(ctx context.Context, principal string, result IdentityLookup) error
}
