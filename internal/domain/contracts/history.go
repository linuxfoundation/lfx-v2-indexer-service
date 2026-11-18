// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// HistoryRecord is an approved mutation snapshot plus a fingerprint of the
// immutable delivery content. It deliberately omits transport headers and raw
// projection data; only registered audit snapshots are retained in history.
type HistoryRecord struct {
	Publisher              string                  `json:"publisher"`
	EventID                string                  `json:"event_id"`
	ObjectType             string                  `json:"object_type"`
	ObjectID               string                  `json:"object_id"`
	Action                 constants.MessageAction `json:"action"`
	ResourceRevision       uint64                  `json:"resource_revision"`
	OccurredAt             time.Time               `json:"occurred_at"`
	IngestedAt             time.Time               `json:"ingested_at"`
	HistoryCheckObject     string                  `json:"history_check_object"`
	HistoryCheckRelation   string                  `json:"history_check_relation"`
	Scope                  types.AuditScope        `json:"scope"`
	ActorRef               string                  `json:"actor_ref"`
	EffectiveActorRef      string                  `json:"effective_actor_ref"`
	ActorVerified          bool                    `json:"actor_verified"`
	EffectiveActorVerified bool                    `json:"effective_actor_verified"`
	Audit                  HistoryAudit            `json:"audit"`
	Fingerprint            string                  `json:"fingerprint"`
}

// HistoryAttribution is indexer-owned output. It cannot represent credentials
// or display profiles, and is deliberately separate from publisher input.
type HistoryAttribution struct {
	ActorRef               string                    `json:"actor_ref"`
	EffectiveActorRef      string                    `json:"effective_actor_ref"`
	ActorVerified          bool                      `json:"actor_verified"`
	EffectiveActorVerified bool                      `json:"effective_actor_verified"`
	ActorIdentity          *types.AuditActorIdentity `json:"actor_identity,omitempty"`
	EffectiveActorIdentity *types.AuditActorIdentity `json:"effective_actor_identity,omitempty"`
}

// HistoryAudit retains only approved source metadata and derived attribution.
// Unlike the incoming AuditEvent, this storage type has no JWT fields.
type HistoryAudit struct {
	EventID          string              `json:"event_id"`
	Publisher        string              `json:"publisher"`
	OccurredAt       time.Time           `json:"occurred_at"`
	ResourceRevision uint64              `json:"resource_revision"`
	Origin           types.AuditOrigin   `json:"origin"`
	Attribution      HistoryAttribution  `json:"attribution"`
	Scope            types.AuditScope    `json:"scope"`
	Content          *types.AuditContent `json:"content,omitempty"`
}

// NewHistoryRecord validates the structural contract and approved snapshot
// schema. This does NOT establish publisher/actor authority, retention or
// erasure admission; callers must enforce those before writing this record.
func NewHistoryRecord(envelope *types.IndexerMessageEnvelope, objectType string, validate types.AuditContentValidator, ingestedAt time.Time) (*HistoryRecord, error) {
	if err := envelope.ValidateV2Source(objectType, validate); err != nil {
		return nil, err
	}
	if envelope.Audit.Origin != types.AuditOriginMutation {
		return nil, fmt.Errorf("projection-only origins must not create history")
	}
	if ingestedAt.IsZero() {
		return nil, fmt.Errorf("history ingestion time is required")
	}
	fingerprint, err := AuditFingerprint(envelope, objectType)
	if err != nil {
		return nil, err
	}
	// Copy the approved audit payload so later mutations of a caller-owned
	// envelope cannot change the stored snapshot after we fingerprinted it.
	source := *envelope.Audit
	source.Attribution.ActorJWT, source.Attribution.EffectiveActorJWT = "", ""
	auditBytes, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("freeze audit metadata: %w", err)
	}
	var audit HistoryAudit
	if err := json.Unmarshal(auditBytes, &audit); err != nil {
		return nil, fmt.Errorf("freeze audit metadata: %w", err)
	}
	return &HistoryRecord{
		Publisher: envelope.Audit.Publisher, EventID: envelope.Audit.EventID,
		ObjectType: objectType, ObjectID: envelope.IndexingConfig.ObjectID,
		Action: envelope.Action, ResourceRevision: envelope.Audit.ResourceRevision,
		OccurredAt: envelope.Audit.OccurredAt.UTC(), IngestedAt: ingestedAt.UTC(),
		HistoryCheckObject:   envelope.IndexingConfig.HistoryCheckObject,
		HistoryCheckRelation: envelope.IndexingConfig.HistoryCheckRelation,
		Scope:                audit.Scope, Audit: audit, Fingerprint: fingerprint,
		ActorRef: audit.Attribution.ActorRef, EffectiveActorRef: audit.Attribution.EffectiveActorRef,
	}, nil
}

// AuditFingerprint identifies immutable publisher content for mutations and
// projection operations. Caller must first validate and authorize the envelope.
// Headers and indexer-assigned ingestion timestamps are excluded.
func AuditFingerprint(envelope *types.IndexerMessageEnvelope, objectType string) (string, error) {
	if envelope == nil || envelope.Audit == nil {
		return "", fmt.Errorf("audit envelope is required")
	}
	// Go's JSON encoder sorts object keys and preserves json.Number values.
	audit := *envelope.Audit
	audit.OccurredAt = audit.OccurredAt.UTC()
	audit.Attribution.ActorJWT, audit.Attribution.EffectiveActorJWT = "", ""
	immutable := struct {
		Action         constants.MessageAction `json:"action"`
		ObjectType     string                  `json:"object_type"`
		Data           any                     `json:"data"`
		Tags           []string                `json:"tags"`
		IndexingConfig *types.IndexingConfig   `json:"indexing_config"`
		Audit          *types.AuditEvent       `json:"audit"`
	}{envelope.Action, objectType, envelope.Data, envelope.Tags, envelope.IndexingConfig, &audit}
	encoded, err := json.Marshal(immutable)
	if err != nil {
		return "", fmt.Errorf("encode immutable audit content: %w", err)
	}
	// RawMessage snapshots may have different whitespace/member order on replay.
	// Decode without float64 so integer precision and array order survive.
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return "", fmt.Errorf("normalize immutable audit content: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("immutable audit content must have one JSON value")
	}
	encoded, err = json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode normalized audit content: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
