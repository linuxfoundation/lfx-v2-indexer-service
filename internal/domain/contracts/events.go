// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package contracts defines the interfaces and contracts for the domain layer of the LFX indexer service.
package contracts

import (
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// IndexingEvent is the NATS event payload published after a successful OpenSearch write.
// It is emitted on subjects of the form lfx.{object_type}.{action}
// (e.g., lfx.project.created, lfx.committee.deleted).
type IndexingEvent struct {
	// DocumentID is the canonical OpenSearch document reference in the form
	// "object_type:object_id" (e.g., "project:abc-123").
	DocumentID string `json:"document_id"`

	// ObjectID is the identifier of the object (e.g., "abc-123").
	ObjectID string `json:"object_id"`

	// ObjectType is the type of the object (e.g., "project", "committee").
	ObjectType string `json:"object_type"`

	// Action is the canonical past-tense action that produced this event
	// (e.g., "created", "updated", "deleted").
	Action constants.MessageAction `json:"action"`

	// Body is the full TransactionBody that was written to OpenSearch.
	Body *TransactionBody `json:"body"`

	// Timestamp is the UTC time at which the indexing operation completed.
	Timestamp time.Time `json:"timestamp"`
}
