// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/fgacache"
)

func apply(w *Writer, payload string) (contracts.FGAWriteResult, error) {
	return w.Apply(context.Background(), "committee", json.RawMessage(payload))
}

func isRejected(err error) bool {
	var rejectedErr *contracts.FGARejectedError
	return errors.As(err, &rejectedErr)
}

func TestApplyRejectsPayloadsFgaSyncTerminates(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		redacted string
	}{
		{name: "malformed JSON", payload: `{`},
		{name: "empty payload", payload: ``},
		{name: "null payload", payload: `null`},
		{name: "missing object type", payload: `{"operation":"update_access","data":{"uid":"resource-1"}}`},
		{name: "missing operation", payload: `{"object_type":"committee","data":{"uid":"resource-1"}}`},
		{name: "unknown operation", payload: `{"object_type":"committee","operation":"grant_everything","data":{"uid":"resource-1"}}`},
		{name: "update missing UID", payload: `{"object_type":"committee","operation":"update_access","data":{}}`},
		{name: "update missing data", payload: `{"object_type":"committee","operation":"update_access"}`},
		{
			name:     "update reference with empty type",
			payload:  `{"object_type":"committee","operation":"update_access","data":{"uid":"resource-1","references":{"project":[":invalid"]}}}`,
			redacted: ":invalid",
		},
		{
			name:     "update reference with empty id",
			payload:  `{"object_type":"committee","operation":"update_access","data":{"uid":"resource-1","references":{"project":["project-type-only:"]}}}`,
			redacted: "project-type-only",
		},
		{name: "update malformed access data", payload: `{"object_type":"committee","operation":"update_access","data":"not-an-object"}`},
		{name: "delete missing UID", payload: `{"object_type":"committee","operation":"delete_access","data":{}}`},
		{name: "member_put malformed member data", payload: `{"object_type":"committee","operation":"member_put","data":"not-an-object"}`},
		{
			name:    "member_put missing username",
			payload: `{"object_type":"committee","operation":"member_put","data":{"uid":"resource-1","relations":["member"]}}`,
		},
		{
			name:    "member_put missing UID",
			payload: `{"object_type":"committee","operation":"member_put","data":{"username":"user-1","relations":["member"]}}`,
		},
		{
			name:    "member_put empty relations array",
			payload: `{"object_type":"committee","operation":"member_put","data":{"uid":"resource-1","username":"user-1","relations":[]}}`,
		},
		{
			name:    "member_put missing relations",
			payload: `{"object_type":"committee","operation":"member_put","data":{"uid":"resource-1","username":"user-1"}}`,
		},
		{
			name:    "member_put empty relation entry",
			payload: `{"object_type":"committee","operation":"member_put","data":{"uid":"resource-1","username":"user-1","relations":[""]}}`,
		},
		{name: "member_remove malformed member data", payload: `{"object_type":"committee","operation":"member_remove","data":"not-an-object"}`},
		{name: "member_remove missing username", payload: `{"object_type":"committee","operation":"member_remove","data":{"uid":"resource-1"}}`},
		{name: "member_remove missing UID", payload: `{"object_type":"committee","operation":"member_remove","data":{"username":"user-1"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockFGAClient)

			result, err := apply(newTestWriter(m), tt.payload)

			require.Error(t, err)
			assert.True(t, isRejected(err), "expected a rejected error: %v", err)
			assert.False(t, result.Changed)
			if tt.redacted != "" {
				assert.NotContains(t, err.Error(), tt.redacted)
			}
			m.AssertNotCalled(t, "Read", mock.Anything, mock.Anything, mock.Anything)
			m.AssertNotCalled(t, "Write", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// member_remove with no relations, or only empty ones, removes all of the
// user's relations and is not rejected.
func TestApplyMemberRemoveEmptyRelationsIsValid(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "empty relations array",
			payload: `{"object_type":"committee","operation":"member_remove","data":{"uid":"resource-1","username":"user-1","relations":[]}}`,
		},
		{
			name:    "relations containing only empty strings",
			payload: `{"object_type":"committee","operation":"member_remove","data":{"uid":"resource-1","username":"user-1","relations":["",""]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockFGAClient)
			m.On("Read", mock.Anything, mock.Anything, client.ClientReadOptions{}).Return(tuplesResponse(), nil)

			result, err := apply(newTestWriter(m), tt.payload)

			require.NoError(t, err)
			assert.False(t, result.Changed)
		})
	}
}

func TestApplyLeavesFgaErrorsTransient(t *testing.T) {
	payloads := []struct {
		name    string
		payload string
	}{
		{name: "update", payload: `{"object_type":"committee","operation":"update_access","data":{"uid":"resource-1"}}`},
		{name: "delete", payload: `{"object_type":"committee","operation":"delete_access","data":{"uid":"resource-1"}}`},
		{
			name:    "member_put",
			payload: `{"object_type":"committee","operation":"member_put","data":{"uid":"resource-1","username":"user-1","relations":["member"]}}`,
		},
		{name: "member_remove", payload: `{"object_type":"committee","operation":"member_remove","data":{"uid":"resource-1","username":"user-1"}}`},
	}
	fgaErrors := []struct {
		name string
		err  error
	}{
		{name: "unknown SDK error", err: assert.AnError},
		{name: "connection refused", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{name: "context deadline", err: context.DeadlineExceeded},
		{name: "validation error", err: makeValidationError("request validation failed")},
		{name: "HTTP 400", err: fakeStatusErr{code: 400}},
		{name: "HTTP 401", err: fakeStatusErr{code: 401}},
		{name: "HTTP 403", err: fakeStatusErr{code: 403}},
		{name: "HTTP 408", err: fakeStatusErr{code: 408}},
		{name: "HTTP 409", err: fakeStatusErr{code: 409}},
		{name: "HTTP 429", err: fakeStatusErr{code: 429}},
		{name: "HTTP 500", err: fakeStatusErr{code: 500}},
	}

	for _, p := range payloads {
		for _, fgaErr := range fgaErrors {
			t.Run(p.name+"/"+fgaErr.name, func(t *testing.T) {
				m := new(mockFGAClient)
				m.On("Read", mock.Anything, mock.Anything, client.ClientReadOptions{}).
					Return((*client.ClientReadResponse)(nil), fgaErr.err)

				_, err := apply(newTestWriter(m), p.payload)

				require.Error(t, err)
				assert.False(t, isRejected(err), "an OpenFGA error must stay transient: %v", err)
			})
		}
	}
}

func TestApplyWriteErrorIsTransientAndReportsChanged(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Write", mock.Anything, mock.Anything, mock.Anything).
		Return((*client.ClientWriteResponse)(nil), fakeStatusErr{code: 500})

	result, err := apply(newTestWriter(m),
		`{"object_type":"committee","operation":"member_remove","data":{"uid":"resource-1","username":"user-1","relations":["member"]}}`)

	require.Error(t, err)
	assert.False(t, isRejected(err))
	assert.True(t, result.Changed, "an earlier batch may have been applied")
}

func TestApplyUpdateAccess(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Read", mock.Anything, readFor("committee:resource-1"), client.ClientReadOptions{}).Return(tuplesResponse(
		tuple("committee:resource-1", "member", "user:old-member"),
		tuple("committee:resource-1", "participant", "user:managed-elsewhere"),
	), nil).Once()
	var written []client.ClientTupleKey
	var deleted []client.ClientTupleKeyWithoutCondition
	m.On("Write", mock.Anything, mock.Anything, writeCollisionIgnoreOptions).Run(func(args mock.Arguments) {
		req := args.Get(1).(client.ClientWriteRequest) //nolint:errcheck // set by the caller
		written, deleted = req.Writes, req.Deletes
	}).Return(&client.ClientWriteResponse{}, nil).Once()

	result, err := apply(newTestWriter(m), `{"object_type":"committee","operation":"update_access","data":{`+
		`"uid":"resource-1","public":true,"relations":{"member":["alice"]},`+
		`"references":{"project":["project-1"],"parent":["committee-0"],"meeting":["meeting:m1"]},`+
		`"exclude_relations":["participant"]}}`)

	require.NoError(t, err)
	assert.ElementsMatch(t, []client.ClientTupleKey{
		{User: "user:*", Relation: "viewer", Object: "committee:resource-1"},
		{User: "user:alice", Relation: "member", Object: "committee:resource-1"},
		{User: "project:project-1", Relation: "project", Object: "committee:resource-1"},
		{User: "committee:committee-0", Relation: "parent", Object: "committee:resource-1"},
		{User: "meeting:m1", Relation: "meeting", Object: "committee:resource-1"},
	}, written)
	assert.Equal(t, []client.ClientTupleKeyWithoutCondition{
		{User: "user:old-member", Relation: "member", Object: "committee:resource-1"},
	}, deleted)
	assert.Equal(t, contracts.FGAWriteResult{
		Changed:  true,
		SeedKeys: []string{"committee:resource-1#member@user:alice", "committee:resource-1#viewer@user:*"},
	}, result)
}

func TestApplyUpdateAccessNoChange(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Read", mock.Anything, mock.Anything, client.ClientReadOptions{}).
		Return(tuplesResponse(tuple("committee:resource-1", "viewer", "user:*")), nil).Once()

	result, err := apply(newTestWriter(m), `{"object_type":"committee","operation":"update_access","data":{"uid":"resource-1","public":true}}`)

	require.NoError(t, err)
	assert.Equal(t, contracts.FGAWriteResult{}, result)
}

func TestApplyUpdateAccessOnlyInvalidWritesIsNotChanged(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Read", mock.Anything, mock.Anything, client.ClientReadOptions{}).Return(tuplesResponse(), nil).Once()
	m.On("Write", mock.Anything, mock.Anything, mock.Anything).Return((*client.ClientWriteResponse)(nil),
		makeValidationError("Invalid tuple 'committee:resource-1#bogus@user:alice'. Reason: relation 'committee#bogus' not found"),
	).Once()

	result, err := apply(newTestWriter(m), `{"object_type":"committee","operation":"update_access","data":{"uid":"resource-1","relations":{"bogus":["alice"]}}}`)

	require.NoError(t, err)
	assert.Equal(t, contracts.FGAWriteResult{}, result)
}

func TestApplyDeleteAccess(t *testing.T) {
	m := new(mockFGAClient)
	m.On("Read", mock.Anything, readFor("committee:resource-1"), client.ClientReadOptions{}).Return(tuplesResponse(
		tuple("committee:resource-1", "viewer", "user:*"),
		tuple("committee:resource-1", "auditor", "team:auditors#member"),
	), nil).Once()
	m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
		return len(req.Writes) == 0 && len(req.Deletes) == 1 && req.Deletes[0].User == "user:*"
	}), mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()

	result, err := apply(newTestWriter(m), `{"object_type":"committee","operation":"delete_access","data":{"uid":"resource-1"}}`)

	require.NoError(t, err)
	assert.Equal(t, contracts.FGAWriteResult{Changed: true}, result)
	m.AssertExpectations(t)
}

func TestApplyMemberPut(t *testing.T) {
	existing := []openfga.Tuple{
		tuple("meeting:m1", "participant", "user:alice"),
		tuple("meeting:m1", "invitee", "user:alice"),
		tuple("meeting:m1", "host", "user:bob"),
	}
	tests := []struct {
		name        string
		payload     string
		wantWrites  []client.ClientTupleKey
		wantDeletes []client.ClientTupleKeyWithoutCondition
	}{
		{
			name:        "mutually exclusive relation is replaced",
			payload:     `{"object_type":"meeting","operation":"member_put","data":{"uid":"m1","username":"alice","relations":["host"],"mutually_exclusive_with":["participant","host"]}}`,
			wantWrites:  []client.ClientTupleKey{{User: "user:alice", Relation: "host", Object: "meeting:m1"}},
			wantDeletes: []client.ClientTupleKeyWithoutCondition{{User: "user:alice", Relation: "participant", Object: "meeting:m1"}},
		},
		{
			name:       "relations are added without removing others",
			payload:    `{"object_type":"meeting","operation":"member_put","data":{"uid":"m1","username":"alice","relations":["host","attendee"]}}`,
			wantWrites: []client.ClientTupleKey{{User: "user:alice", Relation: "host", Object: "meeting:m1"}, {User: "user:alice", Relation: "attendee", Object: "meeting:m1"}},
		},
		{
			name:    "member already has the relations",
			payload: `{"object_type":"meeting","operation":"member_put","data":{"uid":"m1","username":"alice","relations":["participant"],"mutually_exclusive_with":["host"]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockFGAClient)
			m.On("Read", mock.Anything, readFor("meeting:m1"), client.ClientReadOptions{}).Return(tuplesResponse(existing...), nil).Once()
			changed := len(tt.wantWrites) > 0 || len(tt.wantDeletes) > 0
			if changed {
				m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
					return assert.ElementsMatch(t, tt.wantWrites, req.Writes) && assert.ElementsMatch(t, tt.wantDeletes, req.Deletes)
				}), writeCollisionIgnoreOptions).Return(&client.ClientWriteResponse{}, nil).Once()
			}

			result, err := apply(newTestWriter(m), tt.payload)

			require.NoError(t, err)
			assert.Equal(t, contracts.FGAWriteResult{Changed: changed}, result)
			m.AssertExpectations(t)
		})
	}
}

func TestApplyMemberRemove(t *testing.T) {
	t.Run("listed relations are deleted without a read", func(t *testing.T) {
		m := new(mockFGAClient)
		m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
			return assert.ElementsMatch(t, []client.ClientTupleKeyWithoutCondition{
				{User: "user:alice", Relation: "host", Object: "past_meeting:p1"},
				{User: "user:alice", Relation: "invitee", Object: "past_meeting:p1"},
			}, req.Deletes)
		}), mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()

		result, err := apply(newTestWriter(m),
			`{"object_type":"past_meeting","operation":"member_remove","data":{"uid":"p1","username":"alice","relations":["host","","invitee"]}}`)

		require.NoError(t, err)
		assert.Equal(t, contracts.FGAWriteResult{Changed: true}, result)
		m.AssertExpectations(t)
	})

	t.Run("no relations deletes every relation the user holds", func(t *testing.T) {
		m := new(mockFGAClient)
		m.On("Read", mock.Anything, readFor("past_meeting:p1"), mock.Anything).Return(tuplesResponse(
			tuple("past_meeting:p1", "host", "user:alice"), tuple("past_meeting:p1", "attendee", "user:alice"),
			tuple("past_meeting:p1", "invitee", "user:bob"),
		), nil).Once()
		m.On("Write", mock.Anything, mock.MatchedBy(func(req client.ClientWriteRequest) bool {
			return assert.ElementsMatch(t, []client.ClientTupleKeyWithoutCondition{
				{User: "user:alice", Relation: "host", Object: "past_meeting:p1"},
				{User: "user:alice", Relation: "attendee", Object: "past_meeting:p1"},
			}, req.Deletes)
		}), mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()

		result, err := apply(newTestWriter(m),
			`{"object_type":"past_meeting","operation":"member_remove","data":{"uid":"p1","username":"alice"}}`)

		require.NoError(t, err)
		assert.Equal(t, contracts.FGAWriteResult{Changed: true}, result)
		m.AssertExpectations(t)
	})
}

func TestNewWithoutAPIURLIsNotConfigured(t *testing.T) {
	w := New(Config{}, discardLogger())
	defer w.Close()

	_, err := apply(w, `{"object_type":"committee","operation":"delete_access","data":{"uid":"resource-1"}}`)

	assert.ErrorIs(t, err, contracts.ErrFGANotConfigured)
	assert.False(t, isRejected(err))
}

func TestNewWithInvalidAPIURLIsNotConfigured(t *testing.T) {
	w := New(Config{APIURL: "://not a url"}, discardLogger())
	defer w.Close()

	_, err := apply(w, `{"object_type":"committee","operation":"delete_access","data":{"uid":"resource-1"}}`)

	assert.ErrorIs(t, err, contracts.ErrFGANotConfigured)
	assert.False(t, isRejected(err))
}

// The FGA cache encodes SeedKeys itself, so they must be plain relation keys
// that encode to the cache keys lfx-v2-fga-sync reads.
func TestSeedKeysMatchTheCacheKeyGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "fgacache", "testdata", "cache_key_golden.json"))
	require.NoError(t, err)
	var golden struct {
		Cases []struct {
			Object   string `json:"object"`
			Relation string `json:"relation"`
			User     string `json:"user"`
			Want     string `json:"want"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &golden))
	require.NotEmpty(t, golden.Cases)

	for _, c := range golden.Cases {
		if !strings.HasPrefix(c.User, userPrefix) {
			continue
		}
		m := new(mockFGAClient)
		m.On("Read", mock.Anything, mock.Anything, mock.Anything).Return(tuplesResponse(), nil).Once()
		m.On("Write", mock.Anything, mock.Anything, mock.Anything).Return(&client.ClientWriteResponse{}, nil).Once()

		result, err := newTestStore(m).syncObjectTuples(context.Background(), c.Object,
			[]client.ClientTupleKey{{User: c.User, Relation: c.Relation}})

		require.NoError(t, err)
		require.Len(t, result.seedKeys, 1)
		assert.Equal(t, c.Want, fgacache.CacheKey(result.seedKeys[0]), c.Object)
	}
}
