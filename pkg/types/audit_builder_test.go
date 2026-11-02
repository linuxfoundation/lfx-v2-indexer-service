// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func builderOptions(e *IndexerMessageEnvelope) MessageV2Options {
	return MessageV2Options{Action: e.Action, Data: e.Data, Tags: e.Tags, IndexingConfig: e.IndexingConfig, Audit: *e.Audit}
}

func TestNewMessageV2(t *testing.T) {
	for _, action := range []constants.MessageAction{constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted} {
		for _, origin := range []AuditOrigin{AuditOriginMutation, AuditOriginReindex, AuditOriginBackfill, AuditOriginSyncEcho} {
			t.Run(string(action)+"/"+string(origin), func(t *testing.T) {
				e := fixtureAuditEnvelope(action)
				e.Audit.Origin = origin
				var validate AuditContentValidator = fixtureAuditValidator
				if origin != AuditOriginMutation {
					e.Audit.Content = nil
					validate = nil
				}
				message, err := NewMessageV2("fixture_resource", builderOptions(e), validate)
				require.NoError(t, err)
				dispatch, err := message.Envelope(e.Headers[constants.AuthorizationHeader], validate)
				require.NoError(t, err)
				require.Equal(t, e, dispatch)
				payload, err := json.Marshal(message)
				require.NoError(t, err)
				require.Contains(t, string(payload), `"headers":null`)
				require.NotContains(t, string(payload), "Bearer")
				dispatch.Headers = nil
				require.ErrorContains(t, dispatch.ValidateV2("fixture_resource", validate), "headers.authorization")
			})
		}
	}
}

func TestNewMessageV2RejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*IndexerMessageEnvelope)
	}{
		{"missing_identity", func(e *IndexerMessageEnvelope) { e.Audit.EventID = "" }},
		{"zero_time", func(e *IndexerMessageEnvelope) { e.Audit.OccurredAt = time.Time{} }},
		{"zero_revision", func(e *IndexerMessageEnvelope) { e.Audit.ResourceRevision = 0 }},
		{"config", func(e *IndexerMessageEnvelope) { e.IndexingConfig.HistoryCheckRelation = "" }},
		{"scope", func(e *IndexerMessageEnvelope) { e.Audit.Scope.Before = nil }},
		{"snapshot_side", func(e *IndexerMessageEnvelope) { e.Audit.Content.Before = nil }},
		{"schema", func(e *IndexerMessageEnvelope) { e.Audit.Content.Schema = "unregistered" }},
		{"schema_allowlist", func(e *IndexerMessageEnvelope) {
			e.Audit.Content.After = json.RawMessage(`{"secret":"fixture-private-value"}`)
		}},
		{"profile", func(e *IndexerMessageEnvelope) { e.Audit.Attribution.ActorProfile = nil }},
		{"projection_content", func(e *IndexerMessageEnvelope) { e.Audit.Origin = AuditOriginBackfill }},
		{"data", func(e *IndexerMessageEnvelope) { e.Data = make(chan int) }},
		{"raw_data_utf8", func(e *IndexerMessageEnvelope) { e.Data = json.RawMessage("{\"name\":\"\xff\"}") }},
		{"oversized", func(e *IndexerMessageEnvelope) {
			e.Data = map[string]any{"name": strings.Repeat("a", MaxAuditEnvelopeBytes)}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := fixtureAuditEnvelope(constants.ActionUpdated)
			tt.mutate(e)
			message, err := NewMessageV2("fixture_resource", builderOptions(e), fixtureAuditValidator)
			require.Error(t, err)
			require.Nil(t, message)
			require.NotContains(t, err.Error(), "fixture-private-value")
		})
	}
	options := builderOptions(fixtureAuditEnvelope(constants.ActionUpdated))
	_, err := NewMessageV2("fixture_resource", options, nil)
	require.ErrorContains(t, err, "registered schema validator")
	_, err = NewMessageV2("other_resource", options, fixtureAuditValidator)
	require.ErrorContains(t, err, "rejected by registered schema validator")
	_, err = NewMessageV2("fixture_resource", options, func(string, string, AuditContent) error { return fmt.Errorf("fixture-private-value") })
	require.Error(t, err)
	require.NotContains(t, err.Error(), "fixture-private-value")
}

func TestMessageV2RetryIsolation(t *testing.T) {
	e := fixtureAuditEnvelope(constants.ActionUpdated)
	e.Audit.ResourceRevision = 9007199254740993
	e.Data.(map[string]any)["number"] = json.Number("9007199254740993")
	message, err := NewMessageV2("fixture_resource", builderOptions(e), fixtureAuditValidator)
	require.NoError(t, err)
	original, err := json.Marshal(message)
	require.NoError(t, err)
	first, err := message.Envelope("Bearer <first-dispatch-token>", fixtureAuditValidator)
	require.NoError(t, err)
	// Mutating caller-owned data or a dispatch envelope cannot change the event.
	e.Audit.EventID = "changed"
	*e.Audit.Attribution.ActorProfile.Name = "Changed Fixture"
	e.Audit.Scope.Before[0] = "project:changed"
	e.IndexingConfig.ObjectID = "changed"
	e.Data.(map[string]any)["name"] = "Changed Fixture"
	first.Audit.Content.Before[0] = '['
	first.Audit.Attribution.ActorProfile.Email = nil
	first.Headers[constants.AuthorizationHeader] = "changed"
	restored, err := RestoreMessageV2(original, "fixture_resource", fixtureAuditValidator)
	require.NoError(t, err)
	original[0] = '[' // Restore also owns its bytes.
	second, err := restored.Envelope("Bearer <fresh-dispatch-token>", fixtureAuditValidator)
	require.NoError(t, err)
	want := fixtureAuditEnvelope(constants.ActionUpdated)
	require.Equal(t, want.Audit.EventID, second.Audit.EventID)
	require.Equal(t, want.Audit.OccurredAt, second.Audit.OccurredAt)
	require.Equal(t, want.Audit.Attribution, second.Audit.Attribution)
	require.Equal(t, uint64(9007199254740993), second.Audit.ResourceRevision)
	require.Equal(t, json.Number("9007199254740993"), second.Data.(map[string]any)["number"])
	require.Equal(t, "Bearer <fresh-dispatch-token>", second.Headers[constants.AuthorizationHeader])
	after, err := json.Marshal(restored)
	require.NoError(t, err)
	before, err := json.Marshal(message)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	_, err = restored.Envelope(" ", fixtureAuditValidator)
	require.ErrorContains(t, err, "headers.authorization")
	_, err = restored.Envelope(strings.Repeat("a", MaxAuditEnvelopeBytes), fixtureAuditValidator)
	require.ErrorContains(t, err, "maximum size")
	dispatchJSON, err := json.Marshal(second)
	require.NoError(t, err)
	_, err = RestoreMessageV2(dispatchJSON, "fixture_resource", fixtureAuditValidator)
	require.ErrorContains(t, err, "transport headers")
	_, err = RestoreMessageV2(before, "fixture_resource", nil)
	require.ErrorContains(t, err, "registered schema validator")
	_, err = (&MessageV2{}).MarshalJSON()
	require.Error(t, err)
	_, err = (*MessageV2)(nil).Envelope("token", nil)
	require.Error(t, err)
}

func TestNewAuditActorProfile(t *testing.T) {
	for _, tt := range []struct {
		name       string
		attributes AuditProfileAttributes
		valid      bool
	}{
		{"unavailable", AuditProfileAttributes{}, true},
		{"partial", AuditProfileAttributes{Name: auditFixtureString("Test User 1")}, true},
		{"full", AuditProfileAttributes{Name: auditFixtureString("Test User 1"), Email: auditFixtureString("user-1@example.com"), Username: auditFixtureString("testuser01"), AvatarURL: auditFixtureString("https://example.com/avatar.png")}, true},
		{"empty", AuditProfileAttributes{Name: auditFixtureString("")}, false},
		{"unsafe_avatar", AuditProfileAttributes{AvatarURL: auditFixtureString("https://example.com/avatar?token=fixture-private-value")}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			profile, err := NewAuditActorProfile(tt.attributes)
			if !tt.valid {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "fixture-private-value")
				return
			}
			require.NoError(t, err)
			payload, err := json.Marshal(profile)
			require.NoError(t, err)
			if tt.name == "unavailable" {
				require.JSONEq(t, `{"availability":"unavailable","name":null,"avatar_url":null,"email":null,"username":null}`, string(payload))
			}
			if tt.attributes.Name != nil {
				*tt.attributes.Name = "Changed Fixture"
				require.Equal(t, "Test User 1", *profile.Name)
			}
		})
	}
}

func TestNewAuditAttribution(t *testing.T) {
	unavailable, err := NewAuditActorProfile(AuditProfileAttributes{})
	require.NoError(t, err)
	for _, tt := range []struct {
		name      string
		actor     AuditActor
		effective *AuditActor
		valid     bool
	}{
		{"user", AuditActor{"user:testuser01", fixtureAuditProfile()}, nil, true},
		{"unavailable", AuditActor{"user:testuser01", unavailable}, nil, true},
		{"service", AuditActor{Ref: "service:test-publisher"}, nil, true},
		{"delegation", AuditActor{"user:testuser01", fixtureAuditProfile()}, &AuditActor{"user:testuser02", unavailable}, true},
		{"service_delegation", AuditActor{Ref: "service:test-publisher"}, &AuditActor{"user:testuser02", unavailable}, true},
		{"missing_user_profile", AuditActor{Ref: "user:testuser01"}, nil, false},
		{"service_profile", AuditActor{"service:test-publisher", unavailable}, nil, false},
		{"missing_ref", AuditActor{}, nil, false},
		{"same_ref_different_profile", AuditActor{"user:testuser01", fixtureAuditProfile()}, &AuditActor{"user:testuser01", unavailable}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			attribution, err := NewAuditAttribution(tt.actor, tt.effective)
			if !tt.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.actor.Profile != nil {
				require.NotSame(t, tt.actor.Profile, attribution.ActorProfile)
				if tt.effective == nil {
					require.Equal(t, attribution.ActorProfile, attribution.EffectiveActorProfile)
					require.NotSame(t, attribution.ActorProfile, attribution.EffectiveActorProfile)
				}
			}
		})
	}
}

func TestNewAuditSnapshots(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{"name":1,"name":2}`)} {
		_, err := NewAuditSnapshots("fixture.audit.v1", nil, raw)
		require.Error(t, err)
	}
	_, err := NewAuditSnapshots("invalid schema", nil, json.RawMessage(`{}`))
	require.Error(t, err)
	before := json.RawMessage(`{"name":"Previous Fixture"}`)
	content, err := NewAuditSnapshots("fixture.audit.v1", before, nil)
	require.NoError(t, err)
	before[0] = '['
	require.Equal(t, byte('{'), content.Before[0])
	require.Nil(t, content.After)
	require.Equal(t, AuditRepresentationSnapshots, content.Representation)
}
