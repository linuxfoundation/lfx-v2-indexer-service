// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// AuditProfileAttributes contains only the approved captured attributes. Nil
// means unavailable; empty strings are not a substitute for null.
type AuditProfileAttributes struct {
	Name      *string
	AvatarURL *string
	Email     *string
	Username  *string
}

// NewAuditActorProfile copies captured attributes and determines availability.
// An empty input explicitly records unavailability. It never looks up a user.
func NewAuditActorProfile(attributes AuditProfileAttributes) (*AuditActorProfile, error) {
	profile := &AuditActorProfile{
		Availability: AuditProfileUnavailable,
		Name:         cloneAuditString(attributes.Name), AvatarURL: cloneAuditString(attributes.AvatarURL),
		Email: cloneAuditString(attributes.Email), Username: cloneAuditString(attributes.Username),
	}
	if profile.Name != nil || profile.AvatarURL != nil || profile.Email != nil || profile.Username != nil {
		profile.Availability = AuditProfileCaptured
	}
	if err := validateAuditActorProfile("user:captured", profile); err != nil {
		return nil, err
	}
	return profile, nil
}

// AuditActor pairs a stable initiating/effective ref with its captured profile.
// Service actors have nil profiles; user actors require an explicit profile.
type AuditActor struct {
	Ref     string
	Profile *AuditActorProfile
}

// NewAuditAttribution copies actor snapshots. A nil effective actor means no
// delegation and reuses the captured initiating profile, without another lookup.
// This validates structure, not authority to delegate or attest these actors.
func NewAuditAttribution(actor AuditActor, effective *AuditActor) (AuditAttribution, error) {
	if effective == nil {
		effective = &actor
	}
	if !auditActorRef(actor.Ref) || !auditActorRef(effective.Ref) {
		return AuditAttribution{}, fmt.Errorf("explicit audit actor and effective actor references are required")
	}
	attribution := AuditAttribution{
		ActorRef: actor.Ref, EffectiveActorRef: effective.Ref,
		ActorProfile: cloneAuditProfile(actor.Profile), EffectiveActorProfile: cloneAuditProfile(effective.Profile),
	}
	if err := attribution.validateProfiles(); err != nil {
		return AuditAttribution{}, err
	}
	return attribution, nil
}

// NewAuditSnapshots copies before/after JSON and supplies the fixed snapshots
// representation. Omit an absent side with nil, not JSON null. Schema ownership,
// allowlists, and action-specific sides are enforced by NewMessageV2.
func NewAuditSnapshots(schema string, before, after json.RawMessage) (*AuditContent, error) {
	if !auditEventIDPattern.MatchString(schema) {
		return nil, fmt.Errorf("audit.content requires a valid schema identifier")
	}
	if (len(before) != 0 && !auditJSONObject(before)) || (len(after) != 0 && !auditJSONObject(after)) {
		return nil, fmt.Errorf("audit.content snapshots must be JSON objects, not null or arrays")
	}
	return &AuditContent{
		Schema: schema, Representation: AuditRepresentationSnapshots,
		Before: append(json.RawMessage(nil), before...), After: append(json.RawMessage(nil), after...),
	}, nil
}

// MessageV2Options supplies immutable source data, without transport headers.
// Assign Audit's identity, occurrence time, revision and captured attribution at
// the source operation, not on dispatch/retry. Data must already be sanitized.
type MessageV2Options struct {
	Action         constants.MessageAction
	Data           any
	Tags           []string
	IndexingConfig *IndexingConfig
	Audit          AuditEvent
}

// MessageV2 is a validated, frozen event suitable for an approved transactional
// outbox. Its JSON is an envelope with null headers, never a dispatch credential.
// Store the resource type alongside the JSON; neither is an authorization grant.
// Availability of these builders does not enable version 2 consumer admission.
type MessageV2 struct {
	payload    []byte
	objectType string
}

// NewMessageV2 constructs and freezes the complete version 2 event. It reuses
// strict public preflight semantics, except that dispatch authentication is
// deliberately absent. Mutations require an approved schema validator; no
// default validator is supplied. No identity/time/profile values are generated.
func NewMessageV2(objectType string, options MessageV2Options, validateContent AuditContentValidator) (*MessageV2, error) {
	envelope := &IndexerMessageEnvelope{
		SchemaVersion: AuditSchemaVersion, Action: options.Action, Data: options.Data,
		Tags: options.Tags, IndexingConfig: options.IndexingConfig, Audit: &options.Audit,
	}
	// Validate typed RawMessage inputs before marshaling can repair invalid UTF-8.
	if err := envelope.validateV2(objectType, validateContent, false); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("version 2 event must be JSON serializable")
	}
	return RestoreMessageV2(payload, objectType, validateContent)
}

// RestoreMessageV2 validates frozen outbox JSON without enriching or regenerating
// source values. It rejects transport headers, including credentials. Schema
// validation is repeated on restore; the validator must be deterministic and
// side-effect-free, and must not echo private data in its errors.
func RestoreMessageV2(payload []byte, objectType string, validateContent AuditContentValidator) (*MessageV2, error) {
	envelope, err := decodeV2Envelope(payload, objectType, validateContent, false)
	if err != nil {
		return nil, err
	}
	if len(envelope.Headers) != 0 {
		return nil, fmt.Errorf("outbox events must not contain transport headers")
	}
	return &MessageV2{payload: append([]byte(nil), payload...), objectType: objectType}, nil
}

// MarshalJSON returns a copy of immutable outbox data, without authentication.
func (m *MessageV2) MarshalJSON() ([]byte, error) {
	if m == nil || len(m.payload) == 0 {
		return nil, fmt.Errorf("a constructed version 2 event is required")
	}
	return append([]byte(nil), m.payload...), nil
}

// Envelope attaches fresh authorization to a separate envelope for dispatch.
// The original outbox event is untouched. ValidateContent has the same approved
// schema requirements as NewMessageV2. Success is not admission or persistence.
func (m *MessageV2) Envelope(authorization string, validateContent AuditContentValidator) (*IndexerMessageEnvelope, error) {
	if m == nil || len(m.payload) == 0 {
		return nil, fmt.Errorf("a constructed version 2 event is required")
	}
	envelope, err := decodeV2Envelope(m.payload, m.objectType, validateContent, false)
	if err != nil {
		return nil, err
	}
	envelope.Headers = map[string]string{constants.AuthorizationHeader: authorization}
	if err := envelope.ValidateV2(m.objectType, validateContent); err != nil {
		return nil, err
	}
	// Fresh credentials add bytes that were not present in the frozen outbox
	// record. Enforce the complete wire limit, not just the stored event's size.
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("version 2 event must be JSON serializable")
	}
	if len(payload) > MaxAuditEnvelopeBytes {
		return nil, fmt.Errorf("audit envelope exceeds maximum size")
	}
	return envelope, nil
}

func cloneAuditString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneAuditProfile(profile *AuditActorProfile) *AuditActorProfile {
	if profile == nil {
		return nil
	}
	return &AuditActorProfile{
		Availability: profile.Availability,
		Name:         cloneAuditString(profile.Name), AvatarURL: cloneAuditString(profile.AvatarURL),
		Email: cloneAuditString(profile.Email), Username: cloneAuditString(profile.Username),
	}
}
