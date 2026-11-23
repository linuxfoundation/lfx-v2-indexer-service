// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"errors"
	"strings"
	"testing"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRelationsMap(t *testing.T) {
	tests := []struct {
		name      string
		object    string
		relations []client.ClientTupleKey
		wantCount int
	}{
		{
			name:   "relations with object field empty",
			object: "project:123",
			relations: []client.ClientTupleKey{
				{User: "user:456", Relation: "writer", Object: ""},
				{User: "user:789", Relation: "viewer", Object: ""},
			},
			wantCount: 2,
		},
		{
			name:   "relations with matching object",
			object: "project:123",
			relations: []client.ClientTupleKey{
				{User: "user:456", Relation: "writer", Object: "project:123"},
				{User: "user:789", Relation: "viewer", Object: "project:123"},
			},
			wantCount: 2,
		},
		{
			name:   "relations with different object",
			object: "project:123",
			relations: []client.ClientTupleKey{
				{User: "user:456", Relation: "writer", Object: "project:999"},
				{User: "user:789", Relation: "viewer", Object: "project:123"},
			},
			wantCount: 1,
		},
		{name: "empty relations", object: "project:123", relations: []client.ClientTupleKey{}},
		{name: "nil relations", object: "project:123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relationsMap(tt.object, tt.relations)

			assert.Len(t, got, tt.wantCount)
			for _, relation := range got {
				assert.Equal(t, tt.object, relation.Object)
			}
		})
	}
}

type syncCase struct {
	name             string
	object           string
	desiredRelations []client.ClientTupleKey
	existingTuples   []openfga.Tuple
	excludeRelations []string
	expectedWrites   int
	expectedDeletes  int
}

func runSyncCase(t *testing.T, tt syncCase) syncResult {
	t.Helper()
	m := new(mockFGAClient)
	m.On("Read", mock.Anything, readFor(tt.object), mock.Anything).Return(tuplesResponse(tt.existingTuples...), nil).Once()
	if tt.expectedWrites > 0 || tt.expectedDeletes > 0 {
		m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
			return len(req.Writes) == tt.expectedWrites && len(req.Deletes) == tt.expectedDeletes
		}), mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()
	}

	result, err := newTestStore(m).syncObjectTuples(context.Background(), tt.object, tt.desiredRelations, tt.excludeRelations...)

	require.NoError(t, err)
	assert.Len(t, result.writes, tt.expectedWrites)
	assert.Len(t, result.deletes, tt.expectedDeletes)
	m.AssertExpectations(t)
	return result
}

func TestSyncObjectTuples_ExcludeRelations(t *testing.T) {
	tests := []syncCase{
		{
			name:   "exclude participant relations from deletion",
			object: "meeting:123",
			desiredRelations: []client.ClientTupleKey{
				{User: "user:*", Relation: "viewer", Object: "meeting:123"},
				{User: "user:organizer1", Relation: "organizer", Object: "meeting:123"},
			},
			existingTuples: []openfga.Tuple{
				tuple("meeting:123", "viewer", "user:*"), tuple("meeting:123", "organizer", "user:organizer1"),
				tuple("meeting:123", "participant", "user:participant1"), tuple("meeting:123", "host", "user:host1"),
			},
			excludeRelations: []string{"participant", "host"},
		},
		{
			name:             "exclude only host relation",
			object:           "meeting:456",
			desiredRelations: []client.ClientTupleKey{{User: "user:*", Relation: "viewer", Object: "meeting:456"}},
			existingTuples:   []openfga.Tuple{tuple("meeting:456", "participant", "user:participant1"), tuple("meeting:456", "host", "user:host1")},
			excludeRelations: []string{"host"},
			expectedWrites:   1,
			expectedDeletes:  1,
		},
		{
			name:             "no exclusions - delete all unmatched",
			object:           "meeting:789",
			desiredRelations: []client.ClientTupleKey{{User: "user:*", Relation: "viewer", Object: "meeting:789"}},
			existingTuples:   []openfga.Tuple{tuple("meeting:789", "participant", "user:participant1"), tuple("meeting:789", "host", "user:host1")},
			excludeRelations: []string{},
			expectedWrites:   1,
			expectedDeletes:  2,
		},
		{
			name:   "exclude past meeting participant relations",
			object: "past_meeting:abc",
			desiredRelations: []client.ClientTupleKey{
				{User: "user:*", Relation: "viewer", Object: "past_meeting:abc"},
				{User: "project:123", Relation: "project", Object: "past_meeting:abc"},
			},
			existingTuples: []openfga.Tuple{
				tuple("past_meeting:abc", "viewer", "user:*"), tuple("past_meeting:abc", "project", "project:123"),
				tuple("past_meeting:abc", "host", "user:user1"), tuple("past_meeting:abc", "invitee", "user:user2"),
				tuple("past_meeting:abc", "attendee", "user:user3"),
			},
			excludeRelations: []string{"host", "invitee", "attendee"},
		},
		{
			name:   "mixed scenario with some exclusions",
			object: "meeting:mixed",
			desiredRelations: []client.ClientTupleKey{
				{User: "user:*", Relation: "viewer", Object: "meeting:mixed"},
				{User: "user:new-organizer", Relation: "organizer", Object: "meeting:mixed"},
			},
			existingTuples: []openfga.Tuple{
				tuple("meeting:mixed", "organizer", "user:old-organizer"),
				tuple("meeting:mixed", "participant", "user:participant1"), tuple("meeting:mixed", "host", "user:host1"),
			},
			excludeRelations: []string{"participant", "host"},
			expectedWrites:   2,
			expectedDeletes:  1,
		},
		{
			name:             "groupsio_mailing_list transitioning from public to private",
			object:           "groupsio_mailing_list:list-123",
			desiredRelations: []client.ClientTupleKey{{User: "user:writer1", Relation: "writer", Object: "groupsio_mailing_list:list-123"}},
			existingTuples: []openfga.Tuple{
				tuple("groupsio_mailing_list:list-123", "viewer", "user:*"),
				tuple("groupsio_mailing_list:list-123", "public", "user:*"),
				tuple("groupsio_mailing_list:list-123", "writer", "user:writer1"),
			},
			excludeRelations: []string{},
			expectedDeletes:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runSyncCase(t, tt)
			for _, del := range result.deletes {
				assert.NotContains(t, tt.excludeRelations, del.Relation)
			}
		})
	}
}

func TestSyncObjectTuples_PreserveTeamGrants(t *testing.T) {
	tests := []syncCase{
		{
			name:   "team member grants preserved during full project sync",
			object: "project:proj-1",
			desiredRelations: []client.ClientTupleKey{
				{User: "user:writer1", Relation: "writer", Object: "project:proj-1"},
				{User: "user:auditor1", Relation: "auditor", Object: "project:proj-1"},
			},
			existingTuples: []openfga.Tuple{
				tuple("project:proj-1", "writer", "user:writer1"), tuple("project:proj-1", "auditor", "user:auditor1"),
				tuple("project:proj-1", "auditor", "team:my-team#member"), tuple("project:proj-1", "owner", "team:other-team#member"),
			},
		},
		{
			name:             "team member grant preserved while stale user tuple is deleted",
			object:           "project:proj-2",
			desiredRelations: []client.ClientTupleKey{{User: "user:new-writer", Relation: "writer", Object: "project:proj-2"}},
			existingTuples:   []openfga.Tuple{tuple("project:proj-2", "writer", "user:old-writer"), tuple("project:proj-2", "owner", "team:my-team#member")},
			expectedWrites:   1,
			expectedDeletes:  1,
		},
		{
			name:             "multiple non-global team member grants are preserved",
			object:           "project:proj-3",
			desiredRelations: []client.ClientTupleKey{{User: "user:*", Relation: "viewer", Object: "project:proj-3"}},
			existingTuples: []openfga.Tuple{
				tuple("project:proj-3", "viewer", "user:*"),
				tuple("project:proj-3", "auditor", "team:my-team#member"), tuple("project:proj-3", "auditor", "team:another-team#member"),
				tuple("project:proj-3", "owner", "team:team-alpha#member"), tuple("project:proj-3", "owner", "team:team-beta#member"),
			},
		},
		{
			name:   "delete-all path preserves non-global team grants while removing user tuples",
			object: "project:proj-4",
			existingTuples: []openfga.Tuple{
				tuple("project:proj-4", "writer", "user:writer1"), tuple("project:proj-4", "auditor", "user:auditor1"),
				tuple("project:proj-4", "owner", "team:my-team#member"),
			},
			expectedDeletes: 2,
		},
		{
			name:            "stale global team grant is deleted",
			object:          "project:proj-5",
			existingTuples:  []openfga.Tuple{tuple("project:proj-5", "global_writer", "team:global-project-writers#member")},
			expectedDeletes: 1,
		},
		{
			name:             "excluded global team grant is preserved",
			object:           "project:proj-6",
			existingTuples:   []openfga.Tuple{tuple("project:proj-6", "global_writer", "team:global-project-writers#member")},
			excludeRelations: []string{"global_writer"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runSyncCase(t, tt)
			for _, del := range result.deletes {
				assert.False(t, strings.HasPrefix(del.User, "team:") && !strings.HasPrefix(del.Relation, "global_"),
					"non-global team grant %s#%s deleted", del.User, del.Relation)
			}
		})
	}
}

func TestSyncObjectTuples_SeedKeys(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Read", mock.Anything, readFor("committee:c1"), mock.Anything).
		Return(tuplesResponse(tuple("committee:c1", "writer", "user:kept")), nil).Once()
	m.On("Write", mock.Anything, mock.Anything, mock.Anything).Return((*client.ClientWriteResponse)(nil),
		makeValidationError("Invalid tuple 'committee:c1#bogus@user:alice'. Reason: relation 'committee#bogus' not found"),
	).Once()
	m.On("Write", mock.Anything, mock.Anything, mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()
	desired := []client.ClientTupleKey{
		{User: "user:kept", Relation: "writer"},
		{User: "user:alice", Relation: "member"},
		{User: "user:alice", Relation: "bogus"},
		{User: "user:*", Relation: "viewer"},
		{User: "project:p1", Relation: "project"},
	}

	result, err := newTestStore(m).syncObjectTuples(context.Background(), "committee:c1", desired)

	require.NoError(t, err)
	// The tuple that already existed was not written, the skipped one was not
	// stored, and the project reference does not resolve to a user.
	assert.Equal(t, []string{"committee:c1#member@user:alice", "committee:c1#viewer@user:*"}, result.seedKeys)
}

func TestSyncObjectTuples_NothingToDoSkipsTheWrite(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Read", mock.Anything, readFor("committee:c1"), mock.Anything).
		Return(tuplesResponse(tuple("committee:c1", "writer", "user:kept")), nil).Once()

	result, err := newTestStore(m).syncObjectTuples(context.Background(), "committee:c1",
		[]client.ClientTupleKey{{User: "user:kept", Relation: "writer"}})

	require.NoError(t, err)
	assert.Equal(t, syncResult{}, result)
	m.AssertNotCalled(t, "Write", mock.Anything, mock.Anything, mock.Anything)
}

func TestSyncObjectTuples_WriteErrorStillReportsWhatWasSent(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Read", mock.Anything, readFor("committee:c1"), mock.Anything).Return(tuplesResponse(), nil).Once()
	m.On("Write", mock.Anything, mock.Anything, mock.Anything).
		Return((*client.ClientWriteResponse)(nil), errors.New("network error")).Once()

	result, err := newTestStore(m).syncObjectTuples(context.Background(), "committee:c1",
		[]client.ClientTupleKey{{User: "user:alice", Relation: "member"}})

	require.Error(t, err)
	assert.Len(t, result.writes, 1)
	assert.Empty(t, result.seedKeys)
}
