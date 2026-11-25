// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import "encoding/json"

// IndexRequest is the request body for lfx.indexer.index.{object_type}: an
// IndexerMessageEnvelope plus an optional FGA operation and sync flag.
//
// FGA and Sync are deliberately not fields of IndexerMessageEnvelope. The
// version 2 decoder rejects unknown fields, so adding them there would make
// the lfx.index.* path silently accept and ignore an FGA operation it never
// applies.
type IndexRequest struct {
	IndexerMessageEnvelope

	// FGA is one fga-sync generic access operation (object_type, operation,
	// data) applied together with the index write. It is kept raw here and
	// decoded by the FGA writer.
	FGA json.RawMessage `json:"fga,omitempty"`

	// Sync makes the index write wait for an OpenSearch refresh before the
	// reply, so the document is searchable as soon as the reply is OK.
	Sync bool `json:"sync,omitempty"`
}
