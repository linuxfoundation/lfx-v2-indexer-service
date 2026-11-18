// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// AuditPublisherPolicy binds a publisher namespace to a validated service
// principal, resource types, origins and approved immutable snapshot schemas.
// CanAttestActors is explicit: the indexer cannot independently reconstruct a
// historical actor's account or source request at delivery time.
type AuditPublisherPolicy struct {
	Publisher       string
	Principal       string
	CanAttestActors bool
	Resources       map[string]AuditResourcePolicy
}

// AuditResourcePolicy grants exactly the listed origins and snapshot schemas.
// A projection origin requires no snapshot schema but must still be authorized.
type AuditResourcePolicy struct {
	Origins map[types.AuditOrigin]bool
	Schemas map[string]func(before, after json.RawMessage) error
}

// AuditAdmission performs strict decoding and trusted publisher admission.
// An empty policy list is fail-closed; no production policies are registered.
type AuditAdmission struct {
	auth     contracts.TokenValidator
	policies map[string]AuditPublisherPolicy
}

// validateContent resolves a snapshot validator only within its registered
// publisher/resource namespace; no schema is globally trusted by name alone.
func (a *AuditAdmission) validateContent(publisher, resource string, content types.AuditContent) error {
	policy, ok := a.policies[publisher]
	if !ok {
		return fmt.Errorf("unregistered audit publisher")
	}
	resourcePolicy, ok := policy.Resources[resource]
	if !ok || !resourcePolicy.Origins[types.AuditOriginMutation] {
		return fmt.Errorf("audit publisher cannot mutate resource type")
	}
	validate, ok := resourcePolicy.Schemas[content.Schema]
	if !ok || validate == nil {
		return fmt.Errorf("unregistered audit snapshot schema")
	}
	return validate(content.Before, content.After)
}

// NewRecord rechecks the approved schema when constructing a history record.
// It must only be called after Admit has authenticated the publisher.
func (a *AuditAdmission) NewRecord(envelope *types.IndexerMessageEnvelope, objectType string, now time.Time) (*contracts.HistoryRecord, error) {
	if a == nil {
		return nil, fmt.Errorf("audit admission is not configured")
	}
	return contracts.NewHistoryRecord(envelope, objectType, a.validateContent, now)
}

// NewAuditAdmission installs only explicitly supplied publisher policies.
func NewAuditAdmission(auth contracts.TokenValidator, policies []AuditPublisherPolicy) (*AuditAdmission, error) {
	admission := &AuditAdmission{auth: auth, policies: make(map[string]AuditPublisherPolicy, len(policies))}
	for _, policy := range policies {
		if !types.ValidAuditPublisherName(policy.Publisher) || !strings.HasSuffix(policy.Principal, constants.MachineUserSuffix) || !policy.CanAttestActors || len(policy.Resources) == 0 {
			return nil, fmt.Errorf("incomplete audit publisher policy")
		}
		if _, exists := admission.policies[policy.Publisher]; exists {
			return nil, fmt.Errorf("duplicate audit publisher policy")
		}
		copyPolicy := AuditPublisherPolicy{
			Publisher: policy.Publisher, Principal: policy.Principal,
			CanAttestActors: policy.CanAttestActors,
			Resources:       make(map[string]AuditResourcePolicy, len(policy.Resources)),
		}
		for resource, resourcePolicy := range policy.Resources {
			if !types.ValidAuditObjectType(resource) || len(resourcePolicy.Origins) == 0 {
				return nil, fmt.Errorf("incomplete audit resource policy")
			}
			copyResource := AuditResourcePolicy{
				Origins: make(map[types.AuditOrigin]bool, len(resourcePolicy.Origins)),
				Schemas: make(map[string]func(before, after json.RawMessage) error, len(resourcePolicy.Schemas)),
			}
			for origin, enabled := range resourcePolicy.Origins {
				copyResource.Origins[origin] = enabled
			}
			for schema, validate := range resourcePolicy.Schemas {
				copyResource.Schemas[schema] = validate
			}
			copyPolicy.Resources[resource] = copyResource
		}
		admission.policies[policy.Publisher] = copyPolicy
	}
	return admission, nil
}

// Admit checks the wire contract and snapshot schema, then authenticates the
// publishing service and verifies its ownership of the supplied claims.
// It does not enable writes or decide erasure/reconciliation policy.
func (a *AuditAdmission) Admit(ctx context.Context, payload []byte, objectType string) (*types.IndexerMessageEnvelope, error) {
	if a == nil || a.auth == nil || len(a.policies) == 0 {
		return nil, fmt.Errorf("audit admission is not configured")
	}
	envelope, err := types.DecodeV2Envelope(payload, objectType, a.validateContent)
	if err != nil {
		return nil, err
	}
	policy, ok := a.policies[envelope.Audit.Publisher]
	if !ok || !policy.CanAttestActors {
		return nil, fmt.Errorf("unregistered audit publisher")
	}
	resourcePolicy, ok := policy.Resources[objectType]
	if !ok || !resourcePolicy.Origins[envelope.Audit.Origin] {
		return nil, fmt.Errorf("audit origin or resource type not authorized")
	}
	principal, err := a.auth.ValidateToken(ctx, envelope.Headers[constants.AuthorizationHeader])
	if err != nil || principal == nil || principal.Principal != policy.Principal {
		// Do not return auth errors: they can contain JWT contents or identity data.
		return nil, fmt.Errorf("audit publisher authentication failed")
	}
	return envelope, nil
}
