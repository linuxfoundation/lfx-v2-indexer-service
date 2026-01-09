// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package types provides public types that can be used by external clients.
package types

import "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"

// IndexingConfig represents the strongly-typed indexing configuration for resource messages.
// This struct ensures type safety when clients provide pre-computed enrichment values,
// bypassing the server-side enricher system.
//
// Clients can include this in their message payloads on object-specific NATS subjects
// (e.g., lfx.index.project, lfx.index.committee) to provide indexing metadata and bypass enrichers.
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
	SortName       string   `json:"sort_name,omitempty"`
	NameAndAliases []string `json:"name_and_aliases,omitempty"`
	ParentRefs     []string `json:"parent_refs,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Fulltext       string   `json:"fulltext,omitempty"`
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

	// IndexingConfig (optional) provides pre-computed indexing metadata.
	// When provided, it bypasses server-side enrichers and uses the supplied configuration directly.
	// This is useful when clients want full control over how their resources are indexed.
	IndexingConfig *IndexingConfig `json:"indexing_config,omitempty"`
}
