// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

import "time"

const (
	// JWTKeyFetchTimeout bounds the HTTP work in each token validation.
	JWTKeyFetchTimeout = 5 * time.Second
	// AuditIdentityLookupTimeout bounds one auth-service primary-email request.
	AuditIdentityLookupTimeout = 5 * time.Second
	// AuditIdentityCacheTimeout bounds each optional identity-cache call, even
	// when permission-cache calls are configured with a larger timeout.
	AuditIdentityCacheTimeout = 5 * time.Second
	// AuditAttributionBudget covers three token validations (publisher and two
	// actors), then two distinct users' cache get, directory lookup and cache set.
	// Keep Helm's audited processing budget and its parity tests aligned.
	AuditAttributionBudget = 3*JWTKeyFetchTimeout + 2*(2*AuditIdentityCacheTimeout+AuditIdentityLookupTimeout)
)
