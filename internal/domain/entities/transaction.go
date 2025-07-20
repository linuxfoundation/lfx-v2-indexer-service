// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package entities

import (
	"fmt"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
)

// TransactionBody represents the OpenSearch document structure
type TransactionBody struct {
	ObjectRef            string         `json:"object_ref,omitempty"`
	ObjectType           string         `json:"object_type,omitempty"`
	ObjectID             string         `json:"object_id,omitempty"`
	ParentRefs           []string       `json:"parent_refs,omitempty"`
	SortName             string         `json:"sort_name,omitempty"`
	NameAndAliases       []string       `json:"name_and_aliases,omitempty"`
	Tags                 []string       `json:"tags,omitempty"`
	Public               bool           `json:"public,omitempty"`
	AccessCheckObject    string         `json:"access_check_object,omitempty"`
	AccessCheckRelation  string         `json:"access_check_relation,omitempty"`
	HistoryCheckObject   string         `json:"history_check_object,omitempty"`
	HistoryCheckRelation string         `json:"history_check_relation,omitempty"`
	Latest               *bool          `json:"latest,omitempty"`
	CreatedAt            *time.Time     `json:"created_at,omitempty"`
	CreatedBy            []string       `json:"created_by,omitempty"`
	CreatedByPrincipals  []string       `json:"created_by_principals,omitempty"`
	CreatedByEmails      []string       `json:"created_by_emails,omitempty"`
	UpdatedAt            *time.Time     `json:"updated_at,omitempty"`
	UpdatedBy            []string       `json:"updated_by,omitempty"`
	UpdatedByPrincipals  []string       `json:"updated_by_principals,omitempty"`
	UpdatedByEmails      []string       `json:"updated_by_emails,omitempty"`
	DeletedAt            *time.Time     `json:"deleted_at,omitempty"`
	DeletedBy            []string       `json:"deleted_by,omitempty"`
	DeletedByPrincipals  []string       `json:"deleted_by_principals,omitempty"`
	DeletedByEmails      []string       `json:"deleted_by_emails,omitempty"`
	Data                 map[string]any `json:"data,omitempty"`
	Fulltext             string         `json:"fulltext,omitempty"`
	Contacts             []ContactBody  `json:"contacts,omitempty"`
	V1Data               map[string]any `json:"v1_data,omitempty"`
}

// LFXTransaction represents the input transaction data
type LFXTransaction struct {
	// Action is expected to be one of "create", "update", or "delete".
	Action string `json:"action"`
	// Headers (at this time) is used to pass the authenticated-principal HTTP
	// headers from the originating request. These are passed as part of the NATS
	// data payload, rather than using native NATS headers on the message.
	Headers map[string]string `json:"headers"`
	// Data may be a string or map[string]any, depending on the action type;
	// deletions only pass the resource ID, while creations and updates pass the
	// current value of the resource. ParsedData and ParsedObjectID contain the
	// respective type assertions for each case.
	Data           any            `json:"data"`
	ParsedData     map[string]any `json:"-"`
	ParsedObjectID string         `json:"-"`

	// The object type is extracted from the NATS subject that the message was
	// received on and retained to use for creating the transaction body for the
	// index. The object name is NOT part of the payload for access control
	// reasons: services can only post to the NATS subjects corresponding to
	// their own resources types.
	ObjectType string `json:"-"`

	// V1Data contains the raw Platform DB record data from LFX v1.
	V1Data map[string]any `json:"v1_data,omitempty"`

	// Capture timestamp at ingest time.
	Timestamp time.Time `json:"-"`

	// Enhanced fields for improved validation and processing
	IsV1             bool        `json:"-"`
	ParsedPrincipals []Principal `json:"-"`
}

// =================
// BASIC HELPERS (Inherent to data structure)
// =================

// IsCreateAction returns true if this is a create action (both V1 and V2)
func (t *LFXTransaction) IsCreateAction() bool {
	return t.Action == constants.ActionCreate || t.Action == constants.ActionCreated
}

// IsUpdateAction returns true if this is an update action (both V1 and V2)
func (t *LFXTransaction) IsUpdateAction() bool {
	return t.Action == constants.ActionUpdate || t.Action == constants.ActionUpdated
}

// IsDeleteAction returns true if this is a delete action (both V1 and V2)
func (t *LFXTransaction) IsDeleteAction() bool {
	return t.Action == constants.ActionDelete || t.Action == constants.ActionDeleted
}

// IsV1Transaction returns true if this transaction originated from V1 system
func (t *LFXTransaction) IsV1Transaction() bool {
	return t.IsV1
}

// =================
// BASIC GETTERS (Simple data access)
// =================

// GetObjectID returns the parsed object ID
func (t *LFXTransaction) GetObjectID() string {
	return t.ParsedObjectID
}

// GetObjectType returns the object type
func (t *LFXTransaction) GetObjectType() string {
	return t.ObjectType
}

// GetParsedData returns the parsed data
func (t *LFXTransaction) GetParsedData() map[string]any {
	return t.ParsedData
}

// GetParsedPrincipals returns the parsed principals
func (t *LFXTransaction) GetParsedPrincipals() []Principal {
	return t.ParsedPrincipals
}

// GetTimestamp returns the transaction timestamp
func (t *LFXTransaction) GetTimestamp() time.Time {
	return t.Timestamp
}

// =================
// SIMPLE TRANSACTION BODY HELPERS
// =================

// NewTransactionBody creates a new transaction body
func NewTransactionBody() *TransactionBody {
	latest := true
	return &TransactionBody{
		Latest: &latest,
	}
}

// =================
// SUPPORTING TYPES
// =================

// ContactBody represents contact information within a transaction
type ContactBody struct {
	LfxPrincipal string         `json:"lfx_principal,omitempty"`
	Name         string         `json:"name,omitempty"`
	Emails       []string       `json:"emails,omitempty"`
	Bot          *bool          `json:"bot,omitempty"`
	Profile      map[string]any `json:"profile,omitempty"`
}

// Principal is used to temporarily store parsed Heimdall authorization tokens.
type Principal struct {
	Principal string
	Email     string
}

// String returns a formatted string representation of the principal
func (p Principal) String() string {
	if p.Email != "" {
		return fmt.Sprintf("%s <%s>", p.Principal, p.Email)
	}
	return p.Principal
}

// ProcessingResult represents the result of processing a transaction
type ProcessingResult struct {
	Success      bool
	Error        error
	ProcessedAt  time.Time
	Duration     time.Duration
	MessageID    string
	DocumentID   string
	IndexSuccess bool
}

// String returns a formatted string representation of the processing result
func (pr ProcessingResult) String() string {
	status := "SUCCESS"
	if !pr.Success {
		status = "FAILED"
	}
	return fmt.Sprintf("ProcessingResult{Status: %s, DocumentID: %s, Duration: %v}",
		status, pr.DocumentID, pr.Duration)
}
