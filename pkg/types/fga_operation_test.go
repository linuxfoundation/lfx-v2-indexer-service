// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFGAOperation_UnmarshalData(t *testing.T) {
	t.Run("access data", func(t *testing.T) {
		var op FGAOperation
		require.NoError(t, json.Unmarshal([]byte(`{"object_type":"committee","operation":"update_access",`+
			`"data":{"uid":"committee-123","public":true,"relations":{"member":["user-alice"]}}}`), &op))

		var decoded FGAAccessData
		require.NoError(t, op.UnmarshalData(&decoded))
		assert.Equal(t, "committee-123", decoded.UID)
		assert.True(t, decoded.Public)
		assert.Equal(t, map[string][]string{"member": {"user-alice"}}, decoded.Relations)
	})

	t.Run("member data", func(t *testing.T) {
		var op FGAOperation
		require.NoError(t, json.Unmarshal([]byte(`{"object_type":"meeting","operation":"member_put",`+
			`"data":{"uid":"meeting-456","username":"user-bob","relations":["host"],`+
			`"mutually_exclusive_with":["participant"]}}`), &op))

		var decoded FGAMemberData
		require.NoError(t, op.UnmarshalData(&decoded))
		assert.Equal(t, "user-bob", decoded.Username)
		assert.Equal(t, []string{"host"}, decoded.Relations)
		assert.Equal(t, []string{"participant"}, decoded.MutuallyExclusiveWith)
	})

	t.Run("nil data decodes to the zero value", func(t *testing.T) {
		op := FGAOperation{ObjectType: "committee", Operation: FGAOperationUpdateAccess}

		var decoded FGAAccessData
		require.NoError(t, op.UnmarshalData(&decoded))
		assert.Empty(t, decoded.UID)
	})

	t.Run("non-object data is an error", func(t *testing.T) {
		op := FGAOperation{ObjectType: "committee", Operation: FGAOperationMemberPut, Data: "not-an-object"}

		var decoded FGAMemberData
		assert.Error(t, op.UnmarshalData(&decoded))
	})
}

// The wire format must stay byte-identical to lfx-v2-fga-sync's
// GenericFGAMessage so producers can reuse their existing messages.
func TestFGAOperation_WireFormat(t *testing.T) {
	tests := []struct {
		name string
		op   FGAOperation
		want string
	}{
		{
			name: "update access",
			op:   FGAOperation{ObjectType: "committee", Operation: FGAOperationUpdateAccess, Data: FGAAccessData{UID: "resource-1"}},
			want: `{"object_type":"committee","operation":"update_access","data":` +
				`{"uid":"resource-1","public":false,"relations":null,"references":null,"exclude_relations":null}}`,
		},
		{
			name: "delete access",
			op:   FGAOperation{ObjectType: "committee", Operation: FGAOperationDeleteAccess, Data: FGADeleteData{UID: "resource-1"}},
			want: `{"object_type":"committee","operation":"delete_access","data":{"uid":"resource-1"}}`,
		},
		{
			name: "member put",
			op: FGAOperation{ObjectType: "committee", Operation: FGAOperationMemberPut, Data: FGAMemberData{
				UID: "resource-1", Username: "user-1", Relations: []string{"member"},
			}},
			want: `{"object_type":"committee","operation":"member_put","data":` +
				`{"uid":"resource-1","username":"user-1","relations":["member"],"mutually_exclusive_with":null}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.op)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}
