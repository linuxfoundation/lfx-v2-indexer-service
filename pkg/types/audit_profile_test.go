// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func TestAuditAttribution_ProfileSnapshots(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AuditAttribution)
		valid  bool
	}{
		{name: "complete_user_profiles", valid: true},
		{name: "quoted_local_email", valid: true, mutate: func(a *AuditAttribution) {
			p := fixtureAuditProfile()
			p.Email = auditFixtureString(`"test user"@example.com`)
			a.ActorProfile, a.EffectiveActorProfile = p, p
		}},
		{name: "quoted_local_with_angle_characters", valid: true, mutate: func(a *AuditAttribution) {
			p := fixtureAuditProfile()
			p.Email = auditFixtureString(`"test<user>"@example.com`)
			a.ActorProfile, a.EffectiveActorProfile = p, p
		}},
		{name: "unicode_display_name", valid: true, mutate: func(a *AuditAttribution) {
			p := fixtureAuditProfile()
			p.Name = auditFixtureString("Test User 🧪")
			a.ActorProfile, a.EffectiveActorProfile = p, p
		}},
		{name: "partial_user_profiles", valid: true, mutate: func(a *AuditAttribution) {
			p := &AuditActorProfile{Availability: AuditProfileCaptured, Username: auditFixtureString("testuser01")}
			a.ActorProfile, a.EffectiveActorProfile = p, p
		}},
		{name: "explicitly_unavailable", valid: true, mutate: func(a *AuditAttribution) {
			p := &AuditActorProfile{Availability: AuditProfileUnavailable}
			a.ActorProfile, a.EffectiveActorProfile = p, p
		}},
		{name: "delegation_profiles", valid: true, mutate: func(a *AuditAttribution) {
			a.ActorRef = "user:testuser02"
			a.ActorProfile = &AuditActorProfile{
				Availability: AuditProfileCaptured, Name: auditFixtureString("Test User 2"),
				Email: auditFixtureString("user-2@example.com"), Username: auditFixtureString("testuser02"),
			}
		}},
		{name: "service_initiator_user_effective", valid: true, mutate: func(a *AuditAttribution) {
			a.ActorRef, a.ActorProfile = "service:test-publisher", nil
		}},
		{name: "system_profiles_absent", valid: true, mutate: func(a *AuditAttribution) {
			*a = AuditAttribution{ActorRef: "service:test-publisher", EffectiveActorRef: "service:test-publisher"}
		}},
		{name: "no_actor_profile", mutate: func(a *AuditAttribution) { a.ActorProfile = nil }},
		{name: "no_effective_profile", mutate: func(a *AuditAttribution) { a.EffectiveActorProfile = nil }},
		{name: "same_ref_different_name", mutate: func(a *AuditAttribution) { a.ActorProfile.Name = auditFixtureString("Test User 2") }},
		{name: "same_ref_different_availability", mutate: func(a *AuditAttribution) {
			a.ActorProfile = &AuditActorProfile{Availability: AuditProfileUnavailable}
		}},
		{name: "captured_without_attributes", mutate: func(a *AuditAttribution) {
			a.ActorProfile = &AuditActorProfile{Availability: AuditProfileCaptured}
		}},
		{name: "unavailable_with_attributes", mutate: func(a *AuditAttribution) { a.ActorProfile.Availability = AuditProfileUnavailable }},
		{name: "unknown_availability", mutate: func(a *AuditAttribution) { a.ActorProfile.Availability = "unknown" }},
		{name: "empty_attribute_instead_of_null", mutate: func(a *AuditAttribution) { a.ActorProfile.Name = auditFixtureString("") }},
		{name: "control_character", mutate: func(a *AuditAttribution) { a.ActorProfile.Name = auditFixtureString("Test\nUser") }},
		{name: "oversized_name", mutate: func(a *AuditAttribution) { a.ActorProfile.Name = auditFixtureString(strings.Repeat("a", 1025)) }},
		{name: "oversized_username", mutate: func(a *AuditAttribution) { a.ActorProfile.Username = auditFixtureString(strings.Repeat("a", 257)) }},
		{name: "invalid_utf8", mutate: func(a *AuditAttribution) { a.ActorProfile.Name = auditFixtureString(string([]byte{0xff})) }},
		{name: "invalid_email", mutate: func(a *AuditAttribution) { a.ActorProfile.Email = auditFixtureString("not-a-mailbox") }},
		{name: "angle_bracket_email", mutate: func(a *AuditAttribution) { a.ActorProfile.Email = auditFixtureString("<user-1@example.com>") }},
		{name: "empty_display_name_email", mutate: func(a *AuditAttribution) { a.ActorProfile.Email = auditFixtureString(`"" <user-1@example.com>`) }},
		{name: "display_name_in_email", mutate: func(a *AuditAttribution) {
			a.ActorProfile.Email = auditFixtureString("Test User 1 <user-1@example.com>")
		}},
		{name: "insecure_avatar", mutate: func(a *AuditAttribution) {
			a.ActorProfile.AvatarURL = auditFixtureString("http://example.com/avatar.png")
		}},
		{name: "relative_avatar", mutate: func(a *AuditAttribution) { a.ActorProfile.AvatarURL = auditFixtureString("/avatar.png") }},
		{name: "script_avatar", mutate: func(a *AuditAttribution) { a.ActorProfile.AvatarURL = auditFixtureString("javascript:fixture-secret") }},
		{name: "credentialed_avatar", mutate: func(a *AuditAttribution) {
			a.ActorProfile.AvatarURL = auditFixtureString("https://fixture-secret@example.com/avatar.png")
		}},
		{name: "tokenized_avatar", mutate: func(a *AuditAttribution) {
			a.ActorProfile.AvatarURL = auditFixtureString("https://example.com/avatar.png?token=fixture-secret")
		}},
		{name: "fragment_avatar", mutate: func(a *AuditAttribution) {
			a.ActorProfile.AvatarURL = auditFixtureString("https://example.com/avatar.png#fixture-secret")
		}},
		{name: "system_with_human_profile", mutate: func(a *AuditAttribution) { a.ActorRef = "service:test-publisher" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := fixtureAuditEnvelope(constants.ActionUpdated)
			if tt.mutate != nil {
				tt.mutate(&e.Audit.Attribution)
			}
			err := e.ValidateV2("fixture_resource", fixtureAuditValidator)
			if !tt.valid {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "fixture-secret")
				return
			}
			require.NoError(t, err)
			payload, err := json.Marshal(e)
			require.NoError(t, err)
			decoded, err := DecodeV2Envelope(payload, "fixture_resource", fixtureAuditValidator)
			require.NoError(t, err)
			require.Equal(t, e.Audit.Attribution, decoded.Audit.Attribution)
		})
	}
}

func TestDecodeV2Envelope_ProfileWireShape(t *testing.T) {
	e := fixtureAuditEnvelope(constants.ActionUpdated)
	payload, err := json.Marshal(e)
	require.NoError(t, err)
	for _, field := range []string{"availability", "name", "avatar_url", "email", "username"} {
		t.Run("missing_"+field, func(t *testing.T) {
			var wire map[string]any
			require.NoError(t, json.Unmarshal(payload, &wire))
			profile := wire["audit"].(map[string]any)["attribution"].(map[string]any)["actor_profile"].(map[string]any)
			delete(profile, field)
			invalid, err := json.Marshal(wire)
			require.NoError(t, err)
			_, err = DecodeV2Envelope(invalid, "fixture_resource", fixtureAuditValidator)
			require.Error(t, err, "raw clients must explicitly mark unavailable attributes null")
		})
	}
	t.Run("invalid_wire_utf8", func(t *testing.T) {
		invalid := bytesReplace(payload, "Test User 1", string([]byte{0xff}))
		_, err := DecodeV2Envelope(invalid, "fixture_resource", fixtureAuditValidator)
		require.Error(t, err)
	})
	for _, field := range []string{"phone", "address", "authorization", "profile", "Name"} {
		t.Run("unapproved_"+field, func(t *testing.T) {
			invalid := bytesReplace(payload, `"availability":"captured"`, `"availability":"captured","`+field+`":"fixture-secret"`)
			_, err := DecodeV2Envelope(invalid, "fixture_resource", fixtureAuditValidator)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "fixture-secret")
		})
	}
	t.Run("unavailable_values_are_explicit_nulls", func(t *testing.T) {
		data, err := json.Marshal(AuditActorProfile{Availability: AuditProfileUnavailable})
		require.NoError(t, err)
		require.JSONEq(t, `{"availability":"unavailable","name":null,"avatar_url":null,"email":null,"username":null}`, string(data))
	})
}

func TestAuditProfileSnapshot_IndependentOfLiveUser(t *testing.T) {
	e := fixtureAuditEnvelope(constants.ActionUpdated)
	payload, err := json.Marshal(e)
	require.NoError(t, err)
	// Simulate later directory changes followed by the live profiles disappearing.
	// The committed event must retain all original attributes without a lookup.
	e.Audit.Attribution.ActorProfile.Name = auditFixtureString("Renamed Test User")
	e.Audit.Attribution.ActorProfile.Email = nil
	e.Audit.Attribution.ActorProfile = nil
	e.Audit.Attribution.EffectiveActorProfile = nil
	decoded, err := DecodeV2Envelope(payload, "fixture_resource", fixtureAuditValidator)
	require.NoError(t, err)
	require.Equal(t, "Test User 1", *decoded.Audit.Attribution.ActorProfile.Name)
	require.Equal(t, "user-1@example.com", *decoded.Audit.Attribution.ActorProfile.Email)
	require.Equal(t, "https://example.com/avatar.png", *decoded.Audit.Attribution.ActorProfile.AvatarURL)
	require.Equal(t, "testuser01", *decoded.Audit.Attribution.ActorProfile.Username)
	require.Equal(t, decoded.Audit.Attribution.ActorProfile, decoded.Audit.Attribution.EffectiveActorProfile)
}
