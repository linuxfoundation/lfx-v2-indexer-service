// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func TestDecodeV2Envelope_RejectsPublisherOwnedOutput(t *testing.T) {
	for _, field := range []string{"actor_verified", "effective_actor_verified", "actor_profile", "effective_actor_profile", "actor_identity", "effective_actor_identity", "ActorJWT"} {
		for _, value := range []any{false, true, nil, map[string]any{"name": "private-profile-marker"}} {
			e := fixtureAuditEnvelope(constants.ActionUpdated)
			payload, err := json.Marshal(e)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(payload, &wire))
			wire["audit"].(map[string]any)["attribution"].(map[string]any)[field] = value
			payload, err = json.Marshal(wire)
			require.NoError(t, err)
			_, err = DecodeV2Envelope(payload, "fixture_resource", fixtureAuditValidator)
			require.Error(t, err, "%s must be rejected even when false/null", field)
			require.NotContains(t, err.Error(), "private-profile-marker")
		}
	}
}

func TestMessageV2_ActorEvidence(t *testing.T) {
	e := fixtureAuditEnvelope(constants.ActionUpdated)
	e.Audit.Attribution.ActorJWT = "fixture-actor-evidence"
	e.Audit.Attribution.EffectiveActorJWT = "fixture-effective-evidence"
	m, err := NewMessageV2("fixture_resource", builderOptions(e), fixtureAuditValidator)
	require.NoError(t, err)
	payload, err := json.Marshal(m)
	require.NoError(t, err)
	require.Contains(t, string(payload), "fixture-actor-evidence")
	require.NotContains(t, string(payload), "fresh-service-token")
	restored, err := RestoreMessageV2(payload, "fixture_resource", fixtureAuditValidator)
	require.NoError(t, err)
	dispatch, err := restored.Envelope("fresh-dispatch-credential", fixtureAuditValidator)
	require.NoError(t, err)
	require.Equal(t, e.Audit.Attribution, dispatch.Audit.Attribution)
	identity, err := json.Marshal(AuditActorIdentity{})
	require.NoError(t, err)
	require.JSONEq(t, `{"username":null,"email":null}`, string(identity))
}

func TestRestoreMessageV2_PreAdoptionAttributionRevision(t *testing.T) {
	for _, tt := range []struct {
		name  string
		field string
		value any
		valid bool
	}{
		{name: "ref-only v2 remains supported", valid: true},
		{name: "actor evidence v2", field: "actor_jwt", value: "fixture-evidence", valid: true},
		{name: "superseded actor profile", field: "actor_profile", value: map[string]any{"availability": "unavailable", "name": nil, "avatar_url": nil, "email": nil, "username": nil}},
		{name: "superseded effective profile", field: "effective_actor_profile", value: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := fixtureAuditEnvelope(constants.ActionUpdated)
			e.Headers = nil
			payload, err := json.Marshal(e)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(payload, &wire))
			if tt.field != "" {
				wire["audit"].(map[string]any)["attribution"].(map[string]any)[tt.field] = tt.value
			}
			payload, err = json.Marshal(wire)
			require.NoError(t, err)
			message, err := RestoreMessageV2(payload, "fixture_resource", fixtureAuditValidator)
			if tt.valid {
				require.NoError(t, err)
				require.NotNil(t, message)
			} else {
				require.Error(t, err)
				require.Nil(t, message)
			}
		})
	}
}
