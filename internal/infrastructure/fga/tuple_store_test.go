// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"errors"
	"fmt"
	"testing"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func withToken(token *string) any {
	return mock.MatchedBy(func(opts client.ClientReadOptions) bool {
		if opts.Consistency == nil || *opts.Consistency != openfga.CONSISTENCYPREFERENCE_HIGHER_CONSISTENCY {
			return false
		}
		if token == nil {
			return opts.ContinuationToken == nil
		}
		return opts.ContinuationToken != nil && *opts.ContinuationToken == *token
	})
}

func TestReadObjectTuples(t *testing.T) {
	page2, errToken := "page-2-token", "error-token"
	tests := []struct {
		name      string
		object    string
		mockSetup func(*mockFGAClient)
		want      []openfga.Tuple
		wantErr   bool
	}{
		{
			name:   "single page of tuples",
			object: "project:123",
			mockSetup: func(m *mockFGAClient) {
				m.On("Read", mock.Anything, readFor("project:123"), mock.Anything).Return(tuplesResponse(
					tuple("project:123", "writer", "user:456"),
					tuple("project:123", "viewer", "user:789"),
				), nil).Once()
			},
			want: []openfga.Tuple{tuple("project:123", "writer", "user:456"), tuple("project:123", "viewer", "user:789")},
		},
		{
			name:   "multiple pages with pagination",
			object: "project:456",
			mockSetup: func(m *mockFGAClient) {
				m.On("Read", mock.Anything, readFor("project:456"), withToken(nil)).Return(&client.ClientReadResponse{
					Tuples:            []openfga.Tuple{tuple("project:456", "writer", "user:111"), tuple("project:456", "writer", "user:222")},
					ContinuationToken: page2,
				}, nil).Once()
				m.On("Read", mock.Anything, readFor("project:456"), withToken(&page2)).Return(tuplesResponse(
					tuple("project:456", "viewer", "user:333"),
					tuple("project:456", "writer", "group:devs"),
				), nil).Once()
			},
			want: []openfga.Tuple{
				tuple("project:456", "writer", "user:111"), tuple("project:456", "writer", "user:222"),
				tuple("project:456", "viewer", "user:333"), tuple("project:456", "writer", "group:devs"),
			},
		},
		{
			name:   "empty result",
			object: "project:789",
			mockSetup: func(m *mockFGAClient) {
				m.On("Read", mock.Anything, readFor("project:789"), mock.Anything).Return(tuplesResponse(), nil).Once()
			},
		},
		{
			name:   "error on first page",
			object: "project:error",
			mockSetup: func(m *mockFGAClient) {
				m.On("Read", mock.Anything, readFor("project:error"), mock.Anything).
					Return((*client.ClientReadResponse)(nil), errors.New("fga read error")).Once()
			},
			wantErr: true,
		},
		{
			name:   "error on subsequent page",
			object: "project:partial",
			mockSetup: func(m *mockFGAClient) {
				m.On("Read", mock.Anything, readFor("project:partial"), withToken(nil)).Return(&client.ClientReadResponse{
					Tuples:            []openfga.Tuple{tuple("project:partial", "writer", "user:100")},
					ContinuationToken: errToken,
				}, nil).Once()
				m.On("Read", mock.Anything, readFor("project:partial"), withToken(&errToken)).
					Return((*client.ClientReadResponse)(nil), errors.New("pagination error")).Once()
			},
			wantErr: true,
		},
		{
			name:   "wildcard and group users",
			object: "project:public",
			mockSetup: func(m *mockFGAClient) {
				m.On("Read", mock.Anything, readFor("project:public"), mock.Anything).Return(tuplesResponse(
					tuple("project:public", "viewer", "user:*"),
					tuple("project:public", "writer", "group:writers"),
					tuple("project:public", "writer", "user:123"),
				), nil).Once()
			},
			want: []openfga.Tuple{
				tuple("project:public", "viewer", "user:*"),
				tuple("project:public", "writer", "group:writers"),
				tuple("project:public", "writer", "user:123"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockFGAClient)
			tt.mockSetup(m)

			got, err := newTestStore(m).readObjectTuples(context.Background(), tt.object)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, got)
			} else {
				require.NoError(t, err)
				assert.ElementsMatch(t, tt.want, got)
			}
			m.AssertExpectations(t)
		})
	}
}

func TestGetTuplesByUserAndObject(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		object   string
		existing []openfga.Tuple
		readErr  error
		want     []client.ClientTupleKey
	}{
		{
			name:     "get single tuple for user and object",
			user:     "user:123",
			object:   "meeting:456",
			existing: []openfga.Tuple{tuple("meeting:456", "participant", "user:123"), tuple("meeting:456", "organizer", "user:789")},
			want:     []client.ClientTupleKey{{User: "user:123", Relation: "participant", Object: "meeting:456"}},
		},
		{
			name:   "get multiple tuples for user and object",
			user:   "user:456",
			object: "past_meeting:789",
			existing: []openfga.Tuple{
				tuple("past_meeting:789", "host", "user:456"), tuple("past_meeting:789", "invitee", "user:999"),
				tuple("past_meeting:789", "invitee", "user:456"), tuple("past_meeting:789", "attendee", "user:456"),
			},
			want: []client.ClientTupleKey{
				{User: "user:456", Relation: "host", Object: "past_meeting:789"},
				{User: "user:456", Relation: "invitee", Object: "past_meeting:789"},
				{User: "user:456", Relation: "attendee", Object: "past_meeting:789"},
			},
		},
		{
			name:     "no tuples for user",
			user:     "user:nonexistent",
			object:   "meeting:123",
			existing: []openfga.Tuple{tuple("meeting:123", "participant", "user:other1"), tuple("meeting:123", "organizer", "user:other2")},
		},
		{
			name:    "error reading object tuples",
			user:    "user:123",
			object:  "meeting:error",
			readErr: errors.New("read error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockFGAClient)
			resp := tuplesResponse(tt.existing...)
			if tt.readErr != nil {
				resp = nil
			}
			m.On("Read", mock.Anything, readFor(tt.object), mock.Anything).Return(resp, tt.readErr).Once()

			got, err := newTestStore(m).getTuplesByUserAndObject(context.Background(), tt.user, tt.object)

			if tt.readErr != nil {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
			m.AssertExpectations(t)
		})
	}
}

func TestWriteAndDeleteTuplesBatch(t *testing.T) {
	writesLen := func(n int) any {
		return mock.MatchedBy(func(req client.ClientWriteRequest) bool { return len(req.Writes) == n })
	}
	tests := []struct {
		name        string
		writes      []client.ClientTupleKey
		deletes     []client.ClientTupleKeyWithoutCondition
		mockSetup   func(*mockFGAClient)
		wantErr     bool
		wantSkipped []string
	}{
		{
			name: "invalid write tuple skipped and retry succeeds",
			writes: []client.ClientTupleKey{
				{Object: "project:5e88f157", Relation: "executive_director", User: "user:alice"},
				{Object: "project:5e88f157", Relation: "viewer", User: "user:*"},
			},
			mockSetup: func(m *mockFGAClient) {
				m.On("Write", mock.Anything, writesLen(2), mock.Anything).Return((*client.ClientWriteResponse)(nil),
					makeValidationError("Invalid tuple 'project:5e88f157#executive_director@user:alice'. "+
						"Reason: relation 'project#executive_director' not found"),
				).Once()
				m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
					return len(req.Writes) == 1 && req.Writes[0].Relation == "viewer"
				}), mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()
			},
			wantSkipped: []string{"project:5e88f157#executive_director@user:alice"},
		},
		{
			name: "invalid delete tuple skipped and retry succeeds",
			deletes: []client.ClientTupleKeyWithoutCondition{
				{Object: "vote_response:abc", Relation: "project", User: "project:123"},
				{Object: "vote_response:abc", Relation: "viewer", User: "user:bob"},
			},
			mockSetup: func(m *mockFGAClient) {
				m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
					return len(req.Deletes) == 2
				}), mock.Anything).Return((*client.ClientWriteResponse)(nil),
					makeValidationError("Invalid tuple 'vote_response:abc#project@project:123'. "+
						"Reason: relation 'vote_response#project' not found"),
				).Once()
				m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
					return len(req.Deletes) == 1 && req.Deletes[0].Relation == "viewer"
				}), mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()
			},
		},
		{
			name:   "all write tuples invalid returns nil",
			writes: []client.ClientTupleKey{{Object: "project:123", Relation: "executive_director", User: "user:alice"}},
			mockSetup: func(m *mockFGAClient) {
				m.On("Write", mock.Anything, mock.Anything, mock.Anything).Return((*client.ClientWriteResponse)(nil),
					makeValidationError("Invalid tuple 'project:123#executive_director@user:alice'. "+
						"Reason: relation 'project#executive_director' not found"),
				).Once()
			},
			wantSkipped: []string{"project:123#executive_director@user:alice"},
		},
		{
			name:   "non-validation error returned as-is",
			writes: []client.ClientTupleKey{{Object: "project:123", Relation: "viewer", User: "user:alice"}},
			mockSetup: func(m *mockFGAClient) {
				m.On("Write", mock.Anything, mock.Anything, mock.Anything).
					Return((*client.ClientWriteResponse)(nil), errors.New("network error")).Once()
			},
			wantErr: true,
		},
		{
			name:   "validation error naming no tuple in the batch returned as-is",
			writes: []client.ClientTupleKey{{Object: "project:123", Relation: "viewer", User: "user:alice"}},
			mockSetup: func(m *mockFGAClient) {
				m.On("Write", mock.Anything, mock.Anything, mock.Anything).Return((*client.ClientWriteResponse)(nil),
					makeValidationError("Invalid tuple 'project:999#viewer@user:bob'. Reason: type not found"),
				).Once()
			},
			wantErr: true,
		},
		{
			name: "multiple invalid tuples removed one by one",
			writes: []client.ClientTupleKey{
				{Object: "project:abc", Relation: "executive_director", User: "user:alice"},
				{Object: "project:abc", Relation: "technical_advisor", User: "user:bob"},
				{Object: "project:abc", Relation: "viewer", User: "user:*"},
			},
			mockSetup: func(m *mockFGAClient) {
				m.On("Write", mock.Anything, writesLen(3), mock.Anything).Return((*client.ClientWriteResponse)(nil),
					makeValidationError("Invalid tuple 'project:abc#executive_director@user:alice'. "+
						"Reason: relation 'project#executive_director' not found"),
				).Once()
				m.On("Write", mock.Anything, writesLen(2), mock.Anything).Return((*client.ClientWriteResponse)(nil),
					makeValidationError("Invalid tuple 'project:abc#technical_advisor@user:bob'. "+
						"Reason: relation 'project#technical_advisor' not found"),
				).Once()
				m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
					return len(req.Writes) == 1 && req.Writes[0].Relation == "viewer"
				}), mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()
			},
			wantSkipped: []string{"project:abc#executive_director@user:alice", "project:abc#technical_advisor@user:bob"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockFGAClient)
			tt.mockSetup(m)

			skipped, err := newTestStore(m).writeAndDeleteTuplesBatch(context.Background(), tt.writes, tt.deletes)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.ElementsMatch(t, tt.wantSkipped, skipped)
			m.AssertExpectations(t)
		})
	}
}

func TestWriteAndDeleteTuplesSplitsIntoBatchesOfOneHundred(t *testing.T) {
	writes := make([]client.ClientTupleKey, 150)
	for i := range writes {
		writes[i] = client.ClientTupleKey{Object: "project:1", Relation: "writer", User: fmt.Sprintf("user:w%d", i)}
	}
	deletes := make([]client.ClientTupleKeyWithoutCondition, 120)
	for i := range deletes {
		deletes[i] = client.ClientTupleKeyWithoutCondition{Object: "project:1", Relation: "auditor", User: fmt.Sprintf("user:d%d", i)}
	}
	var batches [][2]int
	m := new(mockFGAClient)
	m.On("Write", mock.Anything, mock.Anything, writeCollisionIgnoreOptions).
		Run(func(args mock.Arguments) {
			req := args.Get(1).(client.ClientWriteRequest) //nolint:errcheck // set by the caller
			batches = append(batches, [2]int{len(req.Writes), len(req.Deletes)})
		}).
		Return(&client.ClientWriteResponse{}, nil)

	skipped, err := newTestStore(m).writeAndDeleteTuples(context.Background(), writes, deletes)

	require.NoError(t, err)
	assert.Empty(t, skipped)
	assert.Equal(t, [][2]int{{100, 0}, {50, 50}, {0, 70}}, batches)
}

func TestWriteAndDeleteTuplesStopsAtTheFailingBatch(t *testing.T) {
	writes := make([]client.ClientTupleKey, 250)
	for i := range writes {
		writes[i] = client.ClientTupleKey{Object: "project:1", Relation: "writer", User: fmt.Sprintf("user:w%d", i)}
	}
	m := new(mockFGAClient)
	m.On("Write", mock.Anything, mock.Anything, mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()
	m.On("Write", mock.Anything, mock.Anything, mock.Anything).
		Return((*client.ClientWriteResponse)(nil), errors.New("network error")).Once()

	_, err := newTestStore(m).writeAndDeleteTuples(context.Background(), writes, nil)

	require.Error(t, err)
	m.AssertNumberOfCalls(t, "Write", 2)
}

// A request mixing ignore and error semantics reverts to error for the whole
// request, so both fields must be set together.
func TestWriteCollisionIgnoreOptions(t *testing.T) {
	assert.Equal(t, client.CLIENT_WRITE_REQUEST_ON_DUPLICATE_WRITES_IGNORE, writeCollisionIgnoreOptions.Conflict.OnDuplicateWrites)
	assert.Equal(t, client.CLIENT_WRITE_REQUEST_ON_MISSING_DELETES_IGNORE, writeCollisionIgnoreOptions.Conflict.OnMissingDeletes)
}

func TestWriteAndDeleteTuplesBatchPassesCollisionIgnoreOptions(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Write", mock.Anything, mock.Anything, writeCollisionIgnoreOptions).Return(&client.ClientWriteResponse{}, nil).Once()
	writes := []client.ClientTupleKey{{Object: "project:1", Relation: "viewer", User: "user:alice"}}

	_, err := newTestStore(m).writeAndDeleteTuplesBatch(context.Background(), writes, nil)

	assert.NoError(t, err)
	m.AssertExpectations(t)
}

// With the collision-ignore options OpenFGA reports a colliding batch as a
// success, so nothing is skipped.
func TestWriteAndDeleteTuplesBatchCollisionSucceedsAsNoOp(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
		return len(req.Writes) == 1 && len(req.Deletes) == 1
	}), mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()
	writes := []client.ClientTupleKey{{Object: "project:1", Relation: "viewer", User: "user:alice"}}
	deletes := []client.ClientTupleKeyWithoutCondition{{Object: "project:1", Relation: "writer", User: "user:bob"}}

	skipped, err := newTestStore(m).writeAndDeleteTuplesBatch(context.Background(), writes, deletes)

	assert.NoError(t, err)
	assert.Empty(t, skipped)
	m.AssertExpectations(t)
}
