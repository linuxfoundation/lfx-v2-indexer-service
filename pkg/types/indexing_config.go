// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package types provides public types that can be used by external clients.
package types

import (
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// IndexingConfig represents the strongly-typed indexing configuration for resource messages.
// Clients must include this in their create/update message payloads on object-specific NATS subjects
// (e.g., lfx.index.project, lfx.index.committee) to provide the indexing metadata the server needs.
type IndexingConfig struct {
	// ObjectID is the unique identifier for the resource (required)
	ObjectID string `json:"object_id"`

	// Public indicates whether the resource is publicly accessible (optional)
	Public *bool `json:"public,omitempty"`

	// Fine-Grained Authorization (FGA) fields (required for access control)
	AccessCheckObject    string `json:"access_check_object"`
	AccessCheckRelation  string `json:"access_check_relation"`
	HistoryCheckObject   string `json:"history_check_object"`
	HistoryCheckRelation string `json:"history_check_relation"`

	// Search and discovery fields (optional)
	SortName       string        `json:"sort_name,omitempty"`
	NameAndAliases []string      `json:"name_and_aliases,omitempty"`
	ParentRefs     []string      `json:"parent_refs,omitempty"`
	Tags           []string      `json:"tags,omitempty"`
	Fulltext       string        `json:"fulltext,omitempty"`
	Contacts       []ContactBody `json:"contacts,omitempty"`
}

// ContactBody represents contact information within a transaction
type ContactBody struct {
	LfxPrincipal string         `json:"lfx_principal,omitempty"`
	Name         string         `json:"name,omitempty"`
	Emails       []string       `json:"emails,omitempty"`
	Bot          *bool          `json:"bot,omitempty"`
	Profile      map[string]any `json:"profile,omitempty"`
}

// IndexerMessageEnvelope is the complete message format sent to NATS for indexing operations.
//
// This is the top-level structure that clients should use when publishing messages to the indexer service.
// Messages should be published to object-specific subjects (e.g., lfx.index.project, lfx.index.committee).
//
// Example usage:
//
//	envelope := &types.IndexerMessageEnvelope{
//		Action: constants.ActionCreated,
//		Headers: map[string]string{
//			"authorization": "Bearer <token>",
//		},
//		Data: map[string]any{
//			"id":   "proj-123",
//			"name": "My Project",
//		},
//		Tags: []string{"uid", "slug"},
//		IndexingConfig: &types.IndexingConfig{
//			ObjectID:             "proj-123",
//			AccessCheckObject:    "project:proj-123",
//			AccessCheckRelation:  "viewer",
//			HistoryCheckObject:   "project:proj-123",
//			HistoryCheckRelation: "historian",
//			Public:               &publicTrue,
//			SortName:             "my project",
//			Tags:                 []string{"featured"},
//		},
//	}
type IndexerMessageEnvelope struct {
	// Action specifies the operation type: "created", "updated", or "deleted"
	Action constants.MessageAction `json:"action"`

	// Headers contains authentication and authorization headers (e.g., "authorization": "Bearer <token>")
	Headers map[string]string `json:"headers"`

	// Data is the actual resource data.
	// For "created" and "updated" actions, this should be a map[string]any containing the resource data.
	// For "deleted" actions, this can be just the resource ID as a string.
	Data any `json:"data"`

	// Tags (optional) specifies which fields in the data should be indexed as searchable tags.
	// These are separate from IndexingConfig.Tags which are additional static tags for the document.
	Tags []string `json:"tags,omitempty"`

	// IndexingConfig provides indexing metadata required for create/update actions.
	// The server uses this to determine access control, search fields, and document identity.
	// Not required for delete actions.
	IndexingConfig *IndexingConfig `json:"indexing_config,omitempty"`
}
