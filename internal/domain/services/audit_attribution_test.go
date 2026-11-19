// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func TestAuditAttributionService_Verify(t *testing.T) {
	for _, tt := range []struct {
		name, actor, effective, actorJWT, effectiveJWT, publisher string
		claims                                                    *contracts.Principal
		validationErr                                             error
		wantActor, wantEffective                                  bool
		wantCalls                                                 int
	}{
		{name: "missing evidence", actor: "user:testuser01", effective: "user:testuser01"},
		{name: "valid user", actor: "user:testuser01", effective: "user:testuser01", actorJWT: "actor-private-token", claims: &contracts.Principal{Principal: "testuser01", Email: "user-1@example.com"}, wantActor: true, wantEffective: true, wantCalls: 1},
		{name: "invalid signature or expired", actor: "user:testuser01", effective: "user:testuser01", actorJWT: "actor-private-token", validationErr: fmt.Errorf("signature/expiry failure: actor-private-token"), wantCalls: 1},
		{name: "mismatch", actor: "user:testuser01", effective: "user:testuser01", actorJWT: "actor-private-token", claims: &contracts.Principal{Principal: "testuser02", Email: "mismatch@example.com"}, wantCalls: 1},
		{name: "nil claims", actor: "user:testuser01", effective: "user:testuser01", actorJWT: "actor-private-token", wantCalls: 1},
		{name: "valid service", actor: "service:fixture", effective: "service:fixture", actorJWT: "actor-private-token", claims: &contracts.Principal{Principal: "fixture@clients"}, wantActor: true, wantEffective: true, wantCalls: 1},
		{name: "service needs machine suffix", actor: "service:fixture", effective: "service:fixture", actorJWT: "actor-private-token", claims: &contracts.Principal{Principal: "fixture"}, wantCalls: 1},
		{name: "delegation", actor: "service:fixture", effective: "user:testuser01", effectiveJWT: "effective-private-token", publisher: "publisher@clients", claims: &contracts.Principal{Principal: "testuser01", Email: "user-1@example.com"}, wantEffective: true, wantCalls: 1},
		{name: "non-machine cannot delegate", actor: "service:fixture", effective: "user:testuser01", effectiveJWT: "effective-private-token", publisher: "testuser02", claims: &contracts.Principal{Principal: "testuser01", Email: "user-1@example.com"}, wantCalls: 1},
		{name: "same ref uses initiating evidence only", actor: "user:testuser01", effective: "user:testuser01", effectiveJWT: "ignored-effective-token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			auth := &mocks.MockAuditTokenValidator{}
			if tt.wantCalls > 0 {
				token := tt.actorJWT
				if token == "" {
					token = tt.effectiveJWT
				}
				auth.On("ValidateActorToken", mock.Anything, token).Return(tt.claims, tt.validationErr).Times(tt.wantCalls)
			}
			identities, err := NewAuditIdentityResolver(&mocks.MockIdentityDirectory{}, nil)
			require.NoError(t, err)
			var logs bytes.Buffer
			s, err := NewAuditAttributionService(auth, identities, slog.New(slog.NewJSONHandler(&logs, nil)))
			require.NoError(t, err)
			got := s.Verify(context.Background(), types.AuditAttribution{ActorRef: tt.actor, EffectiveActorRef: tt.effective, ActorJWT: tt.actorJWT, EffectiveActorJWT: tt.effectiveJWT}, tt.publisher)
			assert.Equal(t, tt.actor, got.Attribution.ActorRef)
			assert.Equal(t, tt.effective, got.Attribution.EffectiveActorRef)
			assert.Equal(t, tt.wantActor, got.Attribution.ActorVerified)
			assert.Equal(t, tt.wantEffective, got.Attribution.EffectiveActorVerified)
			var wantActorEmail, wantEffectiveEmail *string
			if tt.wantActor && tt.claims != nil && tt.claims.Email != "" {
				wantActorEmail = &tt.claims.Email
			}
			if tt.wantEffective && tt.claims != nil && tt.claims.Email != "" {
				wantEffectiveEmail = &tt.claims.Email
			}
			assert.Equal(t, wantActorEmail, got.actorEmail)
			assert.Equal(t, wantEffectiveEmail, got.effectiveEmail)
			for _, secret := range []string{"actor-private-token", "effective-private-token", "user-1@example.com", "mismatch@example.com", "testuser01", "testuser02"} {
				assert.NotContains(t, logs.String(), secret)
			}
			auth.AssertExpectations(t)
		})
	}
}

func TestAuditAttributionService_VerifyThenResolveMatchedEmails(t *testing.T) {
	auth := &mocks.MockAuditTokenValidator{}
	auth.On("ValidateActorToken", mock.Anything, "actor-fixture-evidence").Return(&contracts.Principal{Principal: "testuser01", Email: "user-1@example.com"}, nil).Once()
	auth.On("ValidateActorToken", mock.Anything, "effective-fixture-evidence").Return(&contracts.Principal{Principal: "testuser02", Email: "user-2@example.com"}, nil).Once()
	// Neither mock has I/O expectations: matched token emails must bypass both.
	directory := &mocks.MockIdentityDirectory{}
	cache := &mocks.MockIdentityCache{}
	identities, err := NewAuditIdentityResolver(directory, cache)
	require.NoError(t, err)
	s, err := NewAuditAttributionService(auth, identities, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	require.NoError(t, err)
	evidence := s.Verify(context.Background(), types.AuditAttribution{
		ActorRef: "user:testuser01", EffectiveActorRef: "user:testuser02",
		ActorJWT: "actor-fixture-evidence", EffectiveActorJWT: "effective-fixture-evidence",
	}, "fixture-publisher@clients")
	attribution, err := s.Resolve(context.Background(), evidence)
	require.NoError(t, err)
	require.True(t, attribution.ActorVerified)
	require.True(t, attribution.EffectiveActorVerified)
	require.Equal(t, "user:testuser01", attribution.ActorRef)
	require.Equal(t, "user:testuser02", attribution.EffectiveActorRef)
	require.Equal(t, "testuser01", *attribution.ActorIdentity.Username)
	require.Equal(t, "user-1@example.com", *attribution.ActorIdentity.Email)
	require.Equal(t, "testuser02", *attribution.EffectiveActorIdentity.Username)
	require.Equal(t, "user-2@example.com", *attribution.EffectiveActorIdentity.Email)
	auth.AssertExpectations(t)
	directory.AssertNotCalled(t, "LookupIdentity", mock.Anything, mock.Anything)
	cache.AssertNotCalled(t, "GetIdentity", mock.Anything, mock.Anything)
	cache.AssertNotCalled(t, "SetIdentity", mock.Anything, mock.Anything, mock.Anything)
}

func TestAuditAttributionService_ResolveSameActorOnce(t *testing.T) {
	directory := &mocks.MockIdentityDirectory{}
	email := "user-1@example.com"
	directory.On("LookupIdentity", mock.Anything, "testuser01").Return(contracts.IdentityLookup{Found: true, Email: &email}, nil).Once()
	identities, err := NewAuditIdentityResolver(directory, nil)
	require.NoError(t, err)
	s, err := NewAuditAttributionService(&mocks.MockAuditTokenValidator{}, identities, slog.Default())
	require.NoError(t, err)
	attribution, err := s.Resolve(context.Background(), VerifiedAuditActors{Attribution: contracts.HistoryAttribution{ActorRef: "user:testuser01", EffectiveActorRef: "user:testuser01"}})
	require.NoError(t, err)
	assert.Equal(t, attribution.ActorIdentity, attribution.EffectiveActorIdentity)
	assert.Equal(t, "testuser01", *attribution.ActorIdentity.Username)
	assert.Equal(t, email, *attribution.ActorIdentity.Email)
	directory.AssertExpectations(t)
}
