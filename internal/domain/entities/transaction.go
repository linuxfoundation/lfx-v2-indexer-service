package entities

import (
	"fmt"
	"time"
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

// Action Helper Methods
//
// The LFX indexer processes transactions from two different sources:
// 1. Regular LFX services: send "created"/"updated"/"deleted" (past-tense)
// 2. v1-sync-helper service: sends "create"/"update"/"delete" (present-tense)
//
// These helper methods handle both formats to provide a unified interface
// for business logic that doesn't need to distinguish between sources.

// IsCreateAction returns true if this is a create action (both V1 and V2)
func (t *LFXTransaction) IsCreateAction() bool {
	return t.Action == "create" || t.Action == "created"
}

// IsUpdateAction returns true if this is an update action (both V1 and V2)
func (t *LFXTransaction) IsUpdateAction() bool {
	return t.Action == "update" || t.Action == "updated"
}

// IsDeleteAction returns true if this is a delete action (both V1 and V2)
func (t *LFXTransaction) IsDeleteAction() bool {
	return t.Action == "delete" || t.Action == "deleted"
}

// IsV1Transaction returns true if this transaction originated from V1 system
func (t *LFXTransaction) IsV1Transaction() bool {
	return t.IsV1
}

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

// GetCanonicalAction returns the canonical (past-tense) action for indexing
func (t *LFXTransaction) GetCanonicalAction() string {
	switch t.Action {
	case "create", "created":
		return "created"
	case "update", "updated":
		return "updated"
	case "delete", "deleted":
		return "deleted"
	default:
		return t.Action
	}
}

// ValidateAction validates the transaction action based on transaction source
func (t *LFXTransaction) ValidateAction() error {
	if t.IsV1 {
		// V1 transactions are sent by the v1-sync-helper service
		// These use present-tense actions to match the original V1 API format
		// Subject pattern: lfx.v1.index.{object_type}
		switch t.Action {
		case "create", "update", "delete":
			return nil
		default:
			return fmt.Errorf("invalid transaction action: %s", t.Action)
		}
	} else {
		// V2 transactions are sent by regular LFX services
		// These use past-tense actions to indicate completed operations
		// Subject pattern: lfx.index.{object_type}
		switch t.Action {
		case "created", "updated", "deleted":
			return nil
		default:
			return fmt.Errorf("invalid transaction action: %s", t.Action)
		}
	}
}

// ValidateObjectType checks if an object type is supported (domain business rule)
func (t *LFXTransaction) ValidateObjectType() error {
	switch t.ObjectType {
	case "project":
		return nil
	default:
		return fmt.Errorf("unsupported object type: %s", t.ObjectType)
	}
}

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
