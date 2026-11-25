// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import "encoding/json"

// FGA operation names carried in FGAOperation.Operation.
const (
	FGAOperationUpdateAccess = "update_access"
	FGAOperationDeleteAccess = "delete_access"
	FGAOperationMemberPut    = "member_put"
	FGAOperationMemberRemove = "member_remove"
)

// FGAOperation is the fga block of an IndexRequest. Its wire format is
// lfx-v2-fga-sync's GenericFGAMessage, so a producer can move an existing
// fga-sync message into the request unchanged.
type FGAOperation struct {
	ObjectType string `json:"object_type"` // e.g., "committee", "project", "meeting"
	Operation  string `json:"operation"`   // one of the FGAOperation* constants
	Data       any    `json:"data"`        // operation-specific payload
}

// UnmarshalData decodes the Data field into v.
func (m *FGAOperation) UnmarshalData(v any) error {
	b, err := json.Marshal(m.Data)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// FGAAccessData is the Data payload for update_access. It is a full sync: any
// relation not listed, and not excluded, is removed.
type FGAAccessData struct {
	UID              string              `json:"uid"`
	Public           bool                `json:"public"`
	Relations        map[string][]string `json:"relations"`         // relation name → usernames
	References       map[string][]string `json:"references"`        // relation name → object ids or "type:id"
	ExcludeRelations []string            `json:"exclude_relations"` // relations managed elsewhere
}

// FGADeleteData is the Data payload for delete_access.
type FGADeleteData struct {
	UID string `json:"uid"`
}

// FGAMemberData is the Data payload for member_put and member_remove.
type FGAMemberData struct {
	UID                   string   `json:"uid"`
	Username              string   `json:"username"`
	Relations             []string `json:"relations"`               // relations to add or remove
	MutuallyExclusiveWith []string `json:"mutually_exclusive_with"` // on member_put: remove these
}
