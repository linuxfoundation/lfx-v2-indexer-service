// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
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
	require.Equal(t, []string{"service:fixture"}, body.UpdatedByPrincipals)
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

// TestAuditProjectionBody_DelegatedAttribution retains both actor refs without
// duplicate identities, shared mutable slices, or private profiles in events.
func TestAuditProjectionBody_DelegatedAttribution(t *testing.T) {
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
				want := []string{"service:fixture"}
				if delegated {
					e.Audit.Attribution.EffectiveActorRef = "user:delegated-fixture"
					want = append(want, "user:delegated-fixture")
				}
				private := "private-profile-marker"
				e.Audit.Attribution.ActorProfile = &types.AuditActorProfile{Name: &private}
				e.Audit.Attribution.EffectiveActorProfile = &types.AuditActorProfile{Name: &private}
				body, err := AuditProjectionBody(e, "fixture_resource")
				require.NoError(t, err)
				var by, principals []string
				switch action {
				case constants.ActionCreated:
					by, principals = body.CreatedBy, body.CreatedByPrincipals
				case constants.ActionUpdated:
					by, principals = body.UpdatedBy, body.UpdatedByPrincipals
				case constants.ActionDeleted:
					by, principals = body.DeletedBy, body.DeletedByPrincipals
				}
				require.Equal(t, want, by)
				require.Equal(t, want, principals)
				encoded, err := json.Marshal(IndexingEvent{Action: action, Body: body})
				require.NoError(t, err)
				require.NotContains(t, string(encoded), private)
				require.NotContains(t, string(encoded), "actor_profile")
				by[0] = "changed"
				require.Equal(t, "service:fixture", principals[0])
				require.Equal(t, "service:fixture", e.Audit.Attribution.ActorRef)
			})
		}
	}
}
