// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// VerifiedAuditActors holds credential-free evidence for a single ingest
// attempt. Only the winning first append retains the flags and identities.
type VerifiedAuditActors struct {
	Attribution    contracts.HistoryAttribution
	actorEmail     *string
	effectiveEmail *string
}

// AuditAttributionService verifies actor evidence and resolves retained identity
// in separate steps so replay never needs a live directory.
type AuditAttributionService struct {
	auth       contracts.ActorTokenValidator
	identities *AuditIdentityResolver
	logger     *slog.Logger
}

// NewAuditAttributionService requires verification, lookup and a private-data-safe
// logger. No input JWT or raw validation error is ever attached to a log record.
func NewAuditAttributionService(auth contracts.ActorTokenValidator, identities *AuditIdentityResolver, logger *slog.Logger) (*AuditAttributionService, error) {
	if auth == nil || identities == nil || logger == nil {
		return nil, fmt.Errorf("audit attribution requires authentication, identity resolution and logging")
	}
	return &AuditAttributionService{auth: auth, identities: identities, logger: logger}, nil
}

// Verify authenticates the optional evidence independently of publisher trust.
// Invalid/missing evidence only makes attribution unverified, never rejects it.
// The caller discards the original JWTs immediately after this method returns.
func (s *AuditAttributionService) Verify(ctx context.Context, input types.AuditAttribution, dispatchPrincipal string) VerifiedAuditActors {
	result := VerifiedAuditActors{Attribution: contracts.HistoryAttribution{
		ActorRef: input.ActorRef, EffectiveActorRef: input.EffectiveActorRef,
	}}
	result.Attribution.ActorVerified, result.actorEmail = s.verifyActor(ctx, "actor", input.ActorRef, input.ActorJWT)
	if input.ActorRef == input.EffectiveActorRef {
		result.Attribution.EffectiveActorVerified = result.Attribution.ActorVerified
		result.effectiveEmail = result.actorEmail
	} else {
		verified, email := s.verifyActor(ctx, "effective_actor", input.EffectiveActorRef, input.EffectiveActorJWT)
		if strings.HasSuffix(dispatchPrincipal, constants.MachineUserSuffix) {
			result.Attribution.EffectiveActorVerified, result.effectiveEmail = verified, email
		} else {
			s.logger.WarnContext(ctx, "audit actor evidence is unverified", "role", "effective_actor", "reason", "publisher_not_machine")
		}
	}
	return result
}

func (s *AuditAttributionService) verifyActor(ctx context.Context, role, ref, token string) (bool, *string) {
	reason := "missing_token"
	if token != "" {
		principal, err := s.auth.ValidateActorToken(ctx, token)
		reason = "invalid_token"
		if err == nil && principal != nil {
			kind, id, _ := strings.Cut(ref, ":")
			want := id
			if kind == "service" {
				want += constants.MachineUserSuffix
			}
			if types.ValidAuditActorRef(ref) && principal.Principal == want {
				if principal.Email == "" {
					return true, nil
				}
				email := principal.Email
				return true, &email
			}
			reason = "principal_mismatch"
		}
	}
	s.logger.WarnContext(ctx, "audit actor evidence is unverified", "role", role, "reason", reason)
	return false, nil
}

// Resolve fills identity on first append only. Equal actor refs reuse one
// lookup; service actors retain no identity even when their token has email.
func (s *AuditAttributionService) Resolve(ctx context.Context, evidence VerifiedAuditActors) (contracts.HistoryAttribution, error) {
	attribution := evidence.Attribution
	var err error
	attribution.ActorIdentity, err = s.identities.Resolve(ctx, attribution.ActorRef, evidence.actorEmail)
	if err != nil {
		return contracts.HistoryAttribution{}, err
	}
	if attribution.ActorRef == attribution.EffectiveActorRef {
		attribution.EffectiveActorIdentity = attribution.ActorIdentity
	} else {
		attribution.EffectiveActorIdentity, err = s.identities.Resolve(ctx, attribution.EffectiveActorRef, evidence.effectiveEmail)
		if err != nil {
			return contracts.HistoryAttribution{}, err
		}
	}
	return attribution, nil
}
