// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// AuditSchemaVersion is the versioned audit-capable envelope, not the NATS
// subject's platform naming. Consumer admission is opt-in and fail-closed.
// The owner-approved pre-adoption attribution correction retains version 2;
// see docs/audit-v2-contract.md for the narrowly scoped compatibility exception.
const AuditSchemaVersion = 2

// MaxAuditEnvelopeBytes bounds the complete JSON envelope before decoding.
// A broker may impose a smaller transport limit including headers.
const MaxAuditEnvelopeBytes = 1 << 20

// AuditOrigin identifies a live mutation, historical import, or projection
// operation. Only an authorized producer may assert these values.
type AuditOrigin string

const (
	// AuditOriginMutation is a committed domain change, including system changes.
	AuditOriginMutation AuditOrigin = "mutation"
	// AuditOriginHistoricalImport appends a historical change without touching
	// current resources, revision guards, cleanup, or downstream resource events.
	AuditOriginHistoricalImport AuditOrigin = "historical_import"
	// AuditOriginReindex rebuilds a projection without inventing a domain change.
	AuditOriginReindex AuditOrigin = "reindex"
	// AuditOriginBackfill loads a projection without fabricating historical changes.
	AuditOriginBackfill AuditOrigin = "backfill"
	// AuditOriginSyncEcho refreshes a projection for an already represented change.
	AuditOriginSyncEcho AuditOrigin = "sync_echo"
)

// AuditEvent is source metadata captured with the committed mutation. Optional
// actor evidence may live in the source outbox until its retention limit, but
// the indexer must strip JWTs before persistence. EventID is never an attempt ID.
type AuditEvent struct {
	EventID          string           `json:"event_id"`
	Publisher        string           `json:"publisher"`
	OccurredAt       time.Time        `json:"occurred_at"`
	ResourceRevision uint64           `json:"resource_revision"`
	Origin           AuditOrigin      `json:"origin"`
	Attribution      AuditAttribution `json:"attribution"`
	Scope            AuditScope       `json:"scope"`
	Content          *AuditContent    `json:"content,omitempty"`
}

// AuditAttribution is publisher input: actor refs and optional signed evidence.
// Verification and retained identity are indexer-owned output, never assertions
// accepted from a publisher. Dispatch authorization identifies the publisher.
type AuditAttribution struct {
	ActorRef          string `json:"actor_ref"`
	EffectiveActorRef string `json:"effective_actor_ref"`
	ActorJWT          string `json:"actor_jwt,omitempty"`
	EffectiveActorJWT string `json:"effective_actor_jwt,omitempty"`
}

// AuditActorIdentity is the indexer-owned retained identity. For users, username
// comes from the actor ref and email from matched token evidence or auth-service.
// Unresolved legacy actors retain explicit nulls, never a fabricated username.
// Display names, avatar URLs and credentials never belong in this record.
type AuditActorIdentity struct {
	Username *string `json:"username"`
	Email    *string `json:"email"`
}

// AuditScope captures before/after ownership. References are opaque resource
// references, never authorization grants. A scoped resource move includes both.
type AuditScope struct {
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

// AuditContent contains approved snapshots, separate from the current-resource
// Data payload. Its registered schema owns allowed fields and privacy rules.
type AuditContent struct {
	Schema         string          `json:"schema"`
	Representation string          `json:"representation"`
	Before         json.RawMessage `json:"before,omitempty"`
	After          json.RawMessage `json:"after,omitempty"`
}

// AuditRepresentationSnapshots is the only content representation in version 2.
const AuditRepresentationSnapshots = "snapshots"

// ValidAuditSchemaName applies the same token grammar used for a version 2
// content.schema on the wire and in publisher policy manifests.
func ValidAuditSchemaName(name string) bool {
	return auditEventIDPattern.MatchString(name)
}

// ValidAuditPublisherName validates the shared publisher namespace used by
// wire envelopes, startup policies and publisher:event_id storage identities.
func ValidAuditPublisherName(name string) bool {
	return auditPublisherPattern.MatchString(name)
}

// ValidAuditObjectType applies the shared audited subject/resource grammar,
// including its 64-character limit and reserved index subject exclusion.
func ValidAuditObjectType(name string) bool {
	return auditObjectPattern.MatchString(name) && name != "index"
}

// RequiresLegacyFence identifies audited origins that depend on retained
// revision guards. Only explicitly authorized backfill may bootstrap an
// unfenced type, without claiming history coverage or ongoing guard stability.
// Historical imports never touch current state and need no projection fence.
func (origin AuditOrigin) RequiresLegacyFence() bool {
	return origin == AuditOriginMutation || origin == AuditOriginReindex || origin == AuditOriginSyncEcho
}

// RequiresHistory identifies origins that must append or verify approved history.
// Historical imports have no current-projection stage and do not require a
// legacy write fence; they still require explicit publisher/origin/schema policy.
func (origin AuditOrigin) RequiresHistory() bool {
	return origin == AuditOriginMutation || origin == AuditOriginHistoricalImport
}

// AuditContentValidator must reject unregistered schemas, schemas not owned by
// this publisher/resource type, and snapshots outside the approved allowlist.
// No resource schemas or production validator registry ship with this contract.
type AuditContentValidator func(publisher, objectType string, content AuditContent) error

var (
	auditPublisherPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	auditEventIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	auditObjectPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	auditRelationPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// DecodeV2Envelope strictly decodes and validates a version 2 envelope for
// publisher preflight/future consumers. Success is not runtime admission,
// publisher authentication, authorization, persistence, or delivery confirmation.
// Decode errors deliberately do not echo untrusted payload values.
func DecodeV2Envelope(payload []byte, objectType string, validateContent AuditContentValidator) (*IndexerMessageEnvelope, error) {
	return decodeV2Envelope(payload, objectType, validateContent, true)
}

func decodeV2Envelope(payload []byte, objectType string, validateContent AuditContentValidator, requireAuthentication bool) (*IndexerMessageEnvelope, error) {
	if len(payload) > MaxAuditEnvelopeBytes {
		return nil, fmt.Errorf("audit envelope exceeds maximum size")
	}
	if !auditJSONShape(payload) {
		return nil, fmt.Errorf("invalid version 2 JSON envelope")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var envelope IndexerMessageEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("invalid version 2 JSON envelope")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("version 2 envelope must contain exactly one JSON value")
	}
	if err := envelope.validateV2(objectType, validateContent, requireAuthentication); err != nil {
		return nil, err
	}
	return &envelope, nil
}

// ValidateV2 checks typed contract semantics and requires an approved content
// validator for mutations. It does not authenticate the publisher or authorize
// attribution, scope, history anchors, projection origins, or retention policy.
func (e *IndexerMessageEnvelope) ValidateV2(objectType string, validateContent AuditContentValidator) error {
	return e.validateV2(objectType, validateContent, true)
}

// ValidateV2Source validates source metadata without requiring dispatch headers.
// Like builder preflight, this is not publisher authentication or admission.
func (e *IndexerMessageEnvelope) ValidateV2Source(objectType string, validateContent AuditContentValidator) error {
	return e.validateV2(objectType, validateContent, false)
}

// validateV2 applies common wire/builder semantics while letting persisted,
// header-free builder messages defer transport authentication until dispatch.
func (e *IndexerMessageEnvelope) validateV2(objectType string, validateContent AuditContentValidator, requireAuthentication bool) error {
	if e == nil || e.SchemaVersion != AuditSchemaVersion || e.Audit == nil {
		return fmt.Errorf("schema_version 2 and audit are required")
	}
	if !ValidAuditObjectType(objectType) {
		return fmt.Errorf("invalid audit object type")
	}
	if requireAuthentication && strings.TrimSpace(e.Headers[constants.AuthorizationHeader]) == "" {
		return fmt.Errorf("headers.authorization is required")
	}
	config := e.IndexingConfig
	if config == nil || !auditID(config.ObjectID) {
		return fmt.Errorf("indexing_config.object_id is required")
	}
	if !auditRef(config.AccessCheckObject) || !auditRelationPattern.MatchString(config.AccessCheckRelation) ||
		!auditRef(config.HistoryCheckObject) || !auditRelationPattern.MatchString(config.HistoryCheckRelation) {
		return fmt.Errorf("concrete access and history authorization fields are required")
	}
	// Version 2 config strings are literal. Concrete identity/reference checks
	// above still apply.
	if _, err := json.Marshal(config); err != nil {
		return fmt.Errorf("version 2 indexing_config must be JSON serializable")
	}
	audit := e.Audit
	if !ValidAuditPublisherName(audit.Publisher) || !auditEventIDPattern.MatchString(audit.EventID) {
		return fmt.Errorf("invalid audit publisher or event_id")
	}
	if audit.OccurredAt.IsZero() || audit.OccurredAt.Year() < 1 || audit.OccurredAt.Year() > 9999 {
		return fmt.Errorf("audit.occurred_at is required and must be an RFC3339 timestamp")
	}
	if audit.ResourceRevision == 0 || audit.ResourceRevision > math.MaxInt64 {
		return fmt.Errorf("audit.resource_revision must be between 1 and 9223372036854775807")
	}
	if !auditActorRef(audit.Attribution.ActorRef) || !auditActorRef(audit.Attribution.EffectiveActorRef) {
		return fmt.Errorf("explicit audit actor and effective actor references are required")
	}
	for _, actor := range []AuditActor{
		{Ref: audit.Attribution.ActorRef, JWT: audit.Attribution.ActorJWT},
		{Ref: audit.Attribution.EffectiveActorRef, JWT: audit.Attribution.EffectiveActorJWT},
	} {
		if ValidLegacyAuditActorRef(actor.Ref) && (audit.Origin != AuditOriginHistoricalImport || actor.JWT != "") {
			return fmt.Errorf("legacy actors require historical_import origin and no actor JWT evidence")
		}
	}
	for _, refs := range [][]string{audit.Scope.Before, audit.Scope.After} {
		for _, ref := range refs {
			if !auditRef(ref) {
				return fmt.Errorf("invalid audit scope reference")
			}
		}
	}
	if err := e.validateV2Action(); err != nil {
		return err
	}
	switch audit.Origin {
	case AuditOriginMutation, AuditOriginHistoricalImport:
		if err := e.validateAuditContent(validateContent, objectType); err != nil {
			return err
		}
	case AuditOriginReindex, AuditOriginBackfill, AuditOriginSyncEcho:
		if audit.Content != nil {
			return fmt.Errorf("projection-only origins must omit audit.content")
		}
	default:
		return fmt.Errorf("invalid audit.origin")
	}
	return nil
}

func (e *IndexerMessageEnvelope) validateV2Action() error {
	scope := e.Audit.Scope
	switch e.Action {
	case constants.ActionCreated:
		if len(scope.Before) != 0 || len(scope.After) == 0 {
			return fmt.Errorf("created events require after scope and no before scope")
		}
	case constants.ActionUpdated:
		if len(scope.Before) == 0 || len(scope.After) == 0 {
			return fmt.Errorf("updated events require before and after scope")
		}
	case constants.ActionDeleted:
		id, ok := e.Data.(string)
		if !ok || id != e.IndexingConfig.ObjectID {
			return fmt.Errorf("version 2 delete data must match indexing_config.object_id")
		}
		if len(scope.Before) == 0 || len(scope.After) != 0 {
			return fmt.Errorf("deleted events require before scope and no after scope")
		}
		return nil
	default:
		return fmt.Errorf("invalid version 2 action")
	}
	// Check only the object's wire shape, not its schema-free resource fields.
	data, err := json.Marshal(e.Data)
	if err != nil || !auditJSONObject(data) {
		return fmt.Errorf("version 2 create/update data must be an object")
	}
	return nil
}

func (e *IndexerMessageEnvelope) validateAuditContent(validate AuditContentValidator, objectType string) error {
	content := e.Audit.Content
	if content == nil || !ValidAuditSchemaName(content.Schema) || content.Representation != AuditRepresentationSnapshots {
		return fmt.Errorf("audit.content requires a schema and snapshots representation")
	}
	before, after := len(content.Before) != 0, len(content.After) != 0
	if (e.Action == constants.ActionCreated && (before || !after)) ||
		(e.Action == constants.ActionUpdated && (!before || !after)) ||
		(e.Action == constants.ActionDeleted && (!before || after)) {
		return fmt.Errorf("audit.content snapshots do not match the action")
	}
	if (before && !auditJSONObject(content.Before)) || (after && !auditJSONObject(content.After)) {
		return fmt.Errorf("audit.content snapshots must be JSON objects, not null or arrays")
	}
	if validate == nil {
		return fmt.Errorf("audit.content requires a registered schema validator")
	}
	if err := validate(e.Audit.Publisher, objectType, *content); err != nil {
		return fmt.Errorf("audit.content rejected by registered schema validator")
	}
	return nil
}

func auditJSONObject(value []byte) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || value[0] != '{' || !utf8.Valid(value) || !json.Valid(value) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	// RawMessage callers can otherwise bypass duplicate-key/depth checks even
	// when validating an already constructed Go envelope.
	return auditJSONValue(decoder, auditOpaqueJSONPath, 0)
}

func auditID(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e || strings.ContainsRune(":#@{}", r) {
			return false
		}
	}
	return true
}

func auditRef(value string) bool {
	kind, id, ok := strings.Cut(value, ":")
	return ok && auditObjectPattern.MatchString(kind) && auditID(id)
}

func auditActorRef(value string) bool {
	kind, _, _ := strings.Cut(value, ":")
	return ((kind == "user" || kind == "service") && auditRef(value)) || ValidLegacyAuditActorRef(value)
}

// ValidAuditActorRef validates user, service and source-qualified legacy refs.
// It checks syntax only, never authentication, origin or delegation authority.
func ValidAuditActorRef(value string) bool {
	return auditActorRef(value)
}

// ValidLegacyAuditActorRef validates legacy:<source>/<opaque-id>. Source uses
// the publisher namespace grammar; the complete source/id is bounded to 256
// characters. This is provenance, not a Heimdall principal or an access grant.
// Only historical imports may use it, and it can never carry actor JWT evidence.
func ValidLegacyAuditActorRef(value string) bool {
	kind, id, ok := strings.Cut(value, ":")
	if !ok || kind != "legacy" || !auditID(id) {
		return false
	}
	source, sourceID, ok := strings.Cut(id, "/")
	return ok && ValidAuditPublisherName(source) && auditID(sourceID)
}
