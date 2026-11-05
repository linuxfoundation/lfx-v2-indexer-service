// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func historyFixture() *types.IndexerMessageEnvelope {
	return &types.IndexerMessageEnvelope{
		SchemaVersion: types.AuditSchemaVersion, Action: constants.ActionUpdated,
		Headers:        map[string]string{constants.AuthorizationHeader: "Bearer token-not-persisted"},
		Data:           map[string]any{"uid": "fixture-resource", "count": json.Number("9007199254740993")},
		IndexingConfig: &types.IndexingConfig{ObjectID: "fixture-resource", AccessCheckObject: "fixture_resource:fixture-resource", AccessCheckRelation: "viewer", HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor"},
		Audit: &types.AuditEvent{EventID: "fixture-event-1", Publisher: "test-publisher", ResourceRevision: 42, Origin: types.AuditOriginMutation,
			OccurredAt:  time.Date(2026, 10, 29, 16, 0, 0, 0, time.UTC),
			Attribution: types.AuditAttribution{ActorRef: "service:fixture", EffectiveActorRef: "service:fixture"},
			Scope:       types.AuditScope{Before: []string{"project:fixture-project"}, After: []string{"project:fixture-project"}},
			Content: &types.AuditContent{Schema: "fixture.audit.v1", Representation: types.AuditRepresentationSnapshots,
				Before: json.RawMessage(`{"name":"Before"}`), After: json.RawMessage(`{"name":"After"}`)},
		},
	}
}

// historyFixtureValidator approves only the synthetic resource/schema pairing.
func historyFixtureValidator(publisher, objectType string, content types.AuditContent) error {
	if publisher != "test-publisher" || objectType != "fixture_resource" || content.Schema != "fixture.audit.v1" {
		return fmt.Errorf("unregistered snapshot schema")
	}
	return nil // Test-only schema; not a production registry.
}

func TestNewHistoryRecord(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	e := historyFixture()
	first, err := NewHistoryRecord(e, "fixture_resource", historyFixtureValidator, now)
	require.NoError(t, err)
	require.Equal(t, now, first.IngestedAt)
	require.Equal(t, "project:fixture-project", first.HistoryCheckObject)
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "token-not-persisted")
	require.NotContains(t, string(encoded), "9007199254740993") // projection data is only fingerprinted
	e.Audit.Content.Before = json.RawMessage(`{"name":"Mutated after record creation"}`)
	require.JSONEq(t, `{"name":"Before"}`, string(first.Audit.Content.Before))
	e.Audit.Content.Before = json.RawMessage(`{"name":"Before"}`)

	e.Headers[constants.AuthorizationHeader] = "Bearer new-credential"
	e.Audit.Content.After = json.RawMessage(`{ "name" : "After" }`)
	second, err := NewHistoryRecord(e, "fixture_resource", historyFixtureValidator, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, first.Fingerprint, second.Fingerprint)
	require.Equal(t, now.Add(time.Second), second.IngestedAt) // store keeps the first one

	e.Data.(map[string]any)["count"] = json.Number("9007199254740994")
	changed, err := NewHistoryRecord(e, "fixture_resource", historyFixtureValidator, now)
	require.NoError(t, err)
	require.NotEqual(t, first.Fingerprint, changed.Fingerprint)

	e.Audit.Origin = types.AuditOriginReindex
	e.Audit.Content = nil
	_, err = NewHistoryRecord(e, "fixture_resource", historyFixtureValidator, now)
	require.ErrorContains(t, err, "projection-only")
}
