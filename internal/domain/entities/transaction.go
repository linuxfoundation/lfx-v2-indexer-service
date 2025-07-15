package entities

import (
	"time"
)

// TransactionBody represents the OpenSearch document generated for any
// given LFX Transaction that will be stored into our resources index.
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

	// Parsed and validated principals.
	ParsedPrincipals []Principal `json:"-"`

	// Capture timestamp at ingest time.
	Timestamp time.Time `json:"-"`
}

// ContactBody represents contact information within a transaction
type ContactBody struct {
	LfxPrincipal string         `json:"lfx_principal,omitempty"`
	Name         string         `json:"name,omitempty"`
	Emails       []string       `json:"emails,omitempty"`
	Bot          *bool          `json:"bot,omitempty"`
	Profile      map[string]any `json:"profile,omitempty"`
}

// Principal represents an authenticated principal with their email
type Principal struct {
	Principal string
	Email     string
}

// IsCreate checks if the transaction is a create action
func (t *LFXTransaction) IsCreate() bool {
	return t.Action == "create"
}

// IsUpdate checks if the transaction is an update action
func (t *LFXTransaction) IsUpdate() bool {
	return t.Action == "update"
}

// IsDelete checks if the transaction is a delete action
func (t *LFXTransaction) IsDelete() bool {
	return t.Action == "delete"
}

// IsV1Transaction checks if this is a V1 transaction
func (t *LFXTransaction) IsV1Transaction() bool {
	return t.V1Data != nil
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

// GetPrincipals returns the parsed principals
func (t *LFXTransaction) GetPrincipals() []Principal {
	return t.ParsedPrincipals
}

// GetTimestamp returns the transaction timestamp
func (t *LFXTransaction) GetTimestamp() time.Time {
	return t.Timestamp
}
