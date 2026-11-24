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

func TestValidLegacyAuditActorRef(t *testing.T) {
	for _, tt := range []struct {
		ref   string
		valid bool
	}{
		{"legacy:v1-platform/actor-001", true},
		{"legacy:v1-salesforce/actor-001", true},
		{"legacy:v1-platform/" + strings.Repeat("a", 244), true},
		{"legacy:v1-platform/" + strings.Repeat("a", 245), false},
		{"legacy:actor-001", false},
		{"legacy:/actor-001", false},
		{"legacy:v1-platform/", false},
		{"legacy:V1/actor-001", false},
		{"legacy:v1-platform/first.last@example.com", false},
		{"legacy:v1-platform/actor:001", false},
		{"legacy:v1-platform/actor 001", false},
		{"user:first-last", false},
		{"service:fixture", false},
	} {
		t.Run(tt.ref, func(t *testing.T) {
			require.Equal(t, tt.valid, ValidLegacyAuditActorRef(tt.ref))
			if strings.HasPrefix(tt.ref, "legacy:") {
				require.Equal(t, tt.valid, ValidAuditActorRef(tt.ref))
			}
		})
	}
}

func TestLegacyActors_OriginAndEvidence(t *testing.T) {
	for _, origin := range []AuditOrigin{AuditOriginMutation, AuditOriginHistoricalImport, AuditOriginReindex, AuditOriginBackfill, AuditOriginSyncEcho} {
		for _, role := range []string{"actor", "effective"} {
			for _, token := range []string{"", "private-evidence"} {
				t.Run(string(origin)+"/"+role+"/"+map[bool]string{true: "with-token", false: "no-token"}[token != ""], func(t *testing.T) {
					envelope := fixtureAuditEnvelope(constants.ActionUpdated)
					envelope.Audit.Origin = origin
					if !origin.RequiresHistory() {
						envelope.Audit.Content = nil
					}
					if role == "actor" {
						envelope.Audit.Attribution.ActorRef = "legacy:v1-platform/actor-001"
						envelope.Audit.Attribution.ActorJWT = token
					} else {
						envelope.Audit.Attribution.EffectiveActorRef = "legacy:v1-platform/actor-001"
						envelope.Audit.Attribution.EffectiveActorJWT = token
					}
					payload, err := json.Marshal(envelope)
					require.NoError(t, err)
					_, wireErr := DecodeV2Envelope(payload, "fixture_resource", fixtureAuditValidator)
					_, builderErr := NewMessageV2("fixture_resource", builderOptions(envelope), fixtureAuditValidator)
					if origin == AuditOriginHistoricalImport && token == "" {
						require.NoError(t, wireErr)
						require.NoError(t, builderErr)
					} else {
						require.ErrorContains(t, wireErr, "legacy actors require historical_import")
						require.ErrorContains(t, builderErr, "legacy actors require historical_import")
						if token != "" {
							require.NotContains(t, wireErr.Error(), token)
						}
					}
				})
			}
		}
	}
}

func TestNewAuditAttribution_LegacyActor(t *testing.T) {
	actor := AuditActor{Ref: "legacy:v1-platform/actor-001"}
	attribution, err := NewAuditAttribution(actor, nil)
	require.NoError(t, err)
	require.Equal(t, actor.Ref, attribution.ActorRef)
	require.Equal(t, actor.Ref, attribution.EffectiveActorRef)
	actor.JWT = "private-evidence"
	_, err = NewAuditAttribution(actor, nil)
	require.ErrorContains(t, err, "legacy actors must not carry")
	require.NotContains(t, err.Error(), actor.JWT)
	_, err = NewAuditAttribution(AuditActor{Ref: "user:first-last"}, &actor)
	require.ErrorContains(t, err, "legacy actors must not carry")
}
