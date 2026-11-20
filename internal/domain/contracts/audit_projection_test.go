// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func TestAuditProjectionBody(t *testing.T) {
	update := historyFixture()
	update.Tags = []string{"first", "shared"}
	update.IndexingConfig.Tags = []string{"shared", "second"}
	update.Headers[constants.AuthorizationHeader] = "Bearer must-not-be-copied"
	update.IndexingConfig.Fulltext = "searchable"
	public := true
	update.IndexingConfig.Public = &public
	body, err := AuditProjectionBody(update, "fixture_resource")
	require.NoError(t, err)
	require.Equal(t, []string{"first", "shared", "second"}, body.Tags)
	require.Equal(t, update.Audit.OccurredAt, *body.UpdatedAt)
	require.Equal(t, "fixture_resource:fixture-resource", body.ObjectRef)
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "must-not-be-copied")
	require.NotContains(t, string(encoded), "actor_profile")

	deleteEvent := historyFixture()
	deleteEvent.Action = constants.ActionDeleted
	deleteEvent.Data = "fixture-resource"
	deleteEvent.IndexingConfig.Public = &public
	deleteEvent.IndexingConfig.Fulltext = "must-not-be-on-tombstone"
	tombstone, err := AuditProjectionBody(deleteEvent, "fixture_resource")
	require.NoError(t, err)
	require.Equal(t, "project:fixture-project", tombstone.HistoryCheckObject)
	require.NotNil(t, tombstone.DeletedAt)
	require.False(t, tombstone.Public)
	require.Empty(t, tombstone.Data)
	require.Empty(t, tombstone.AccessCheckObject)
	require.Empty(t, tombstone.Fulltext)
}

// Current documents and events contain no audit identity, even for delegation.
func TestAuditProjectionBody_NoCurrentAttribution(t *testing.T) {
	for _, action := range []constants.MessageAction{constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted} {
		for _, delegated := range []bool{false, true} {
			name := string(action) + "/same-actor"
			if delegated {
				name = string(action) + "/delegated"
			}
			t.Run(name, func(t *testing.T) {
				e := historyFixture()
				e.Action = action
				if action == constants.ActionDeleted {
					e.Data = e.IndexingConfig.ObjectID
				}
				e.Audit.Attribution.ActorRef = "service:fixture"
				e.Audit.Attribution.EffectiveActorRef = "service:fixture"
				if delegated {
					e.Audit.Attribution.EffectiveActorRef = "user:delegated-fixture"
				}
				private := "private-profile-marker"
				e.Audit.Attribution.ActorJWT = private
				e.Audit.Attribution.EffectiveActorJWT = private
				body, err := AuditProjectionBody(e, "fixture_resource")
				require.NoError(t, err)
				switch action {
				case constants.ActionCreated:
					require.Equal(t, e.Audit.OccurredAt.UTC(), *body.CreatedAt)
					require.Equal(t, body.CreatedAt, body.UpdatedAt)
				case constants.ActionUpdated:
					require.Equal(t, e.Audit.OccurredAt.UTC(), *body.UpdatedAt)
				case constants.ActionDeleted:
					require.Equal(t, e.Audit.OccurredAt.UTC(), *body.DeletedAt)
				}
				encoded, err := json.Marshal(IndexingEvent{Action: action, Body: body})
				require.NoError(t, err)
				for _, prefix := range []string{"created_by", "updated_by", "deleted_by"} {
					for _, suffix := range []string{"", "_principals", "_emails"} {
						require.NotContains(t, string(encoded), `"`+prefix+suffix+`"`)
					}
				}
				require.NotContains(t, string(encoded), "service:fixture")
				require.NotContains(t, string(encoded), "user:delegated-fixture")
				require.NotContains(t, string(encoded), private)
				require.NotContains(t, string(encoded), "actor_profile")
				require.Equal(t, "service:fixture", e.Audit.Attribution.ActorRef)
			})
		}
	}
}
