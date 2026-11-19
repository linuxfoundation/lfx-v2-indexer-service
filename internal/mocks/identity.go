// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// MockIdentityDirectory is a primary-email directory with explicit outcomes.
type MockIdentityDirectory struct{ mock.Mock }

// MockAuditTokenValidator supplies independently validated actor/dispatch claims.
type MockAuditTokenValidator struct{ mock.Mock }

// ValidateActorToken returns only claims configured for supplied actor evidence.
func (m *MockAuditTokenValidator) ValidateActorToken(ctx context.Context, token string) (*contracts.Principal, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*contracts.Principal), args.Error(1)
}

// LookupIdentity returns the configured result or transient directory failure.
func (m *MockIdentityDirectory) LookupIdentity(ctx context.Context, principal string) (contracts.IdentityLookup, error) {
	args := m.Called(ctx, principal)
	return args.Get(0).(contracts.IdentityLookup), args.Error(1)
}

// MockIdentityCache distinguishes hits, misses and backend failures.
type MockIdentityCache struct{ mock.Mock }

// GetIdentity returns a configured identity-cache result.
func (m *MockIdentityCache) GetIdentity(ctx context.Context, principal string) (contracts.IdentityLookup, bool, error) {
	args := m.Called(ctx, principal)
	return args.Get(0).(contracts.IdentityLookup), args.Bool(1), args.Error(2)
}

// SetIdentity records an explicit directory result only.
func (m *MockIdentityCache) SetIdentity(ctx context.Context, principal string, result contracts.IdentityLookup) error {
	return m.Called(ctx, principal, result).Error(0)
}
