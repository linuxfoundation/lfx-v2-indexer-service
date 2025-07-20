// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

import "time"

// NATS subject prefixes (preserved from existing)
// These constants define the protocol format for NATS message subjects
const (
	IndexPrefix  = "lfx.index."    // V2 message prefix
	FromV1Prefix = "lfx.v1.index." // V1 message prefix
)

// NATS subjects (professional expansion)
const (
	ProjectSubject    = IndexPrefix + "project"  // V2 project messages
	V1ProjectSubject  = FromV1Prefix + "project" // V1 project messages
	SubjectIndexing   = IndexPrefix + "index"    // V2 indexing messages
	SubjectV1Indexing = FromV1Prefix + "index"   // V1 indexing messages
	AllSubjects       = IndexPrefix + ">"        // All V2 subjects
	AllV1Subjects     = FromV1Prefix + ">"       // All V1 subjects
)

// Transaction actions (centralized from scattered strings)
const (
	ActionCreate  = "create"  // V1 format (present tense)
	ActionCreated = "created" // V2 format (past tense)
	ActionUpdate  = "update"  // V1 format (present tense)
	ActionUpdated = "updated" // V2 format (past tense)
	ActionDelete  = "delete"  // V1 format (present tense)
	ActionDeleted = "deleted" // V2 format (past tense)
)

// Header constants (centralized)
const (
	HeaderXUsername = "x-username" // V1 username header
	HeaderXEmail    = "x-email"    // V1 email header
)

// Object types (centralized)
const (
	ObjectTypeProject         = "project"
	ObjectTypeCommittee       = "committee"
	ObjectTypeCommitteeMember = "committeemember"
)

// Message processing constants
const (
	DefaultQueue = "lfx.indexer.queue"
	RefreshTrue  = "true"  // OpenSearch refresh parameter
	RefreshFalse = "false" // OpenSearch refresh parameter
	ReplyTimeout = 5 * time.Second
)
