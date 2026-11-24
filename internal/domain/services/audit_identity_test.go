// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func TestAuditIdentityResolver_Resolve(t *testing.T) {
	email := "user-1@example.com"
	empty := ""
	found := contracts.IdentityLookup{Found: true, Email: &email}
	absent := contracts.IdentityLookup{}
	directoryFailure := errors.New("directory-private-response")
	tests := []struct {
		name         string
		ref          string
		matchedEmail *string
		cached       contracts.IdentityLookup
		hit          bool
		cacheErr     error
		lookup       contracts.IdentityLookup
		lookupErr    error
		wantLookup   bool
		wantCache    bool
		wantEmail    *string
		wantErr      bool
	}{
		{name: "matched evidence avoids directory", ref: "user:testuser01", matchedEmail: &email, wantEmail: &email},
		{name: "service has no identity", ref: "service:test-publisher"},
		{name: "invalid ref", ref: "user:", wantErr: true},
		{name: "positive hit", ref: "user:testuser01", cached: found, hit: true, wantEmail: &email},
		{name: "negative hit", ref: "user:testuser01", cached: absent, hit: true},
		{name: "found without email", ref: "user:testuser01", cached: contracts.IdentityLookup{Found: true}, hit: true},
		{name: "miss", ref: "user:testuser01", lookup: found, wantLookup: true, wantCache: true, wantEmail: &email},
		{name: "empty token email falls back", ref: "user:testuser01", matchedEmail: &empty, lookup: found, wantLookup: true, wantCache: true, wantEmail: &email},
		{name: "cache down falls back", ref: "user:testuser01", cacheErr: contracts.ErrCacheBypassed, lookup: found, wantLookup: true, wantCache: true, wantEmail: &email},
		{name: "cache errors are not deletion", ref: "user:testuser01", cached: absent, hit: true, cacheErr: contracts.ErrCacheBypassed, lookup: found, wantLookup: true, wantCache: true, wantEmail: &email},
		{name: "directory absence is cached", ref: "user:testuser01", lookup: absent, wantLookup: true, wantCache: true},
		{name: "directory down is never cached", ref: "user:testuser01", lookupErr: directoryFailure, wantLookup: true, wantErr: true},
		{name: "malformed directory absence is never cached", ref: "user:testuser01", lookup: contracts.IdentityLookup{Email: &email}, wantLookup: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory := &mocks.MockIdentityDirectory{}
			cache := &mocks.MockIdentityCache{}
			if tt.ref == "user:testuser01" && (tt.matchedEmail == nil || *tt.matchedEmail == "") {
				cache.On("GetIdentity", mock.Anything, "testuser01").Return(tt.cached, tt.hit, tt.cacheErr).Once()
			}
			if tt.wantLookup {
				directory.On("LookupIdentity", mock.Anything, "testuser01").Return(tt.lookup, tt.lookupErr).Once()
			}
			if tt.wantCache {
				// A failed cache write never turns a successful lookup into a
				// failed delivery.
				cache.On("SetIdentity", mock.Anything, "testuser01", tt.lookup).Return(contracts.ErrCacheBypassed).Once()
			}
			resolver, err := NewAuditIdentityResolver(directory, cache)
			require.NoError(t, err)
			identity, err := resolver.Resolve(context.Background(), tt.ref, tt.matchedEmail)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, identity)
				assert.NotContains(t, err.Error(), directoryFailure.Error())
				if tt.wantLookup {
					var transient *contracts.IdentityLookupError
					assert.ErrorAs(t, err, &transient)
				}
			} else {
				require.NoError(t, err)
				if tt.ref == "service:test-publisher" {
					assert.Nil(t, identity)
				} else {
					require.NotNil(t, identity)
					assert.Equal(t, "testuser01", *identity.Username)
					assert.Equal(t, tt.wantEmail, identity.Email)
					if tt.wantEmail != nil {
						assert.NotSame(t, tt.wantEmail, identity.Email)
					}
				}
			}
			directory.AssertExpectations(t)
			cache.AssertExpectations(t)
		})
	}
}

func TestAuditIdentityResolver_LegacyDoesNotLookup(t *testing.T) {
	directory, cache := &mocks.MockIdentityDirectory{}, &mocks.MockIdentityCache{}
	resolver, err := NewAuditIdentityResolver(directory, cache)
	require.NoError(t, err)
	email := "first.last@example.com"
	identity, err := resolver.Resolve(context.Background(), "legacy:v1-platform/actor-001", &email)
	require.NoError(t, err)
	require.Equal(t, &types.AuditActorIdentity{}, identity)
	directory.AssertNotCalled(t, "LookupIdentity", mock.Anything, mock.Anything)
	cache.AssertNotCalled(t, "GetIdentity", mock.Anything, mock.Anything)
	cache.AssertNotCalled(t, "SetIdentity", mock.Anything, mock.Anything, mock.Anything)
}

func TestAuditIdentityResolver_WithoutCache(t *testing.T) {
	directory := &mocks.MockIdentityDirectory{}
	directory.On("LookupIdentity", mock.Anything, "testuser01").Return(contracts.IdentityLookup{}, nil).Once()
	resolver, err := NewAuditIdentityResolver(directory, nil)
	require.NoError(t, err)
	identity, err := resolver.Resolve(context.Background(), "user:testuser01", nil)
	require.NoError(t, err)
	assert.Nil(t, identity.Email)
	directory.AssertExpectations(t)
	_, err = NewAuditIdentityResolver(nil, nil)
	assert.Error(t, err)
}

func TestAuditIdentityResolver_IndependentIODeadlines(t *testing.T) {
	for _, shortParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "own deadlines", true: "shorter delivery deadline"}[shortParent], func(t *testing.T) {
			ctx := context.Background()
			if shortParent {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			}
			var calls []context.Context
			checkDeadline := func(limit time.Duration) func(mock.Arguments) {
				return func(args mock.Arguments) {
					callCtx := args.Get(0).(context.Context)
					require.NoError(t, callCtx.Err(), "a canceled cache call must not cancel fallback lookup")
					deadline, ok := callCtx.Deadline()
					require.True(t, ok)
					require.LessOrEqual(t, time.Until(deadline), limit)
					if parentDeadline, ok := ctx.Deadline(); ok {
						require.False(t, deadline.After(parentDeadline))
					}
					calls = append(calls, callCtx)
				}
			}
			cache := &mocks.MockIdentityCache{}
			directory := &mocks.MockIdentityDirectory{}
			cache.On("GetIdentity", mock.Anything, "testuser01").Run(checkDeadline(constants.AuditIdentityCacheTimeout)).Return(contracts.IdentityLookup{}, false, contracts.ErrCacheBypassed).Once()
			directory.On("LookupIdentity", mock.Anything, "testuser01").Run(checkDeadline(constants.AuditIdentityLookupTimeout)).Return(contracts.IdentityLookup{Found: false}, nil).Once()
			cache.On("SetIdentity", mock.Anything, "testuser01", contracts.IdentityLookup{Found: false}).Run(checkDeadline(constants.AuditIdentityCacheTimeout)).Return(contracts.ErrCacheBypassed).Once()
			resolver, err := NewAuditIdentityResolver(directory, cache)
			require.NoError(t, err)
			identity, err := resolver.Resolve(ctx, "user:testuser01", nil)
			require.NoError(t, err)
			require.Nil(t, identity.Email)
			require.Len(t, calls, 3)
			for _, callCtx := range calls {
				require.ErrorIs(t, callCtx.Err(), context.Canceled, "release I/O timers immediately")
			}
			require.NoError(t, ctx.Err())
			cache.AssertExpectations(t)
			directory.AssertExpectations(t)
		})
	}
}
