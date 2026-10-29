// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func fixtureAuditEnvelope(action constants.MessageAction) *IndexerMessageEnvelope {
	e := &IndexerMessageEnvelope{
		SchemaVersion: AuditSchemaVersion,
		Action:        action,
		Headers:       map[string]string{constants.AuthorizationHeader: "Bearer <fresh-service-token>"},
		Data:          map[string]any{"uid": "fixture-resource", "name": "Fixture Resource"},
		IndexingConfig: &IndexingConfig{
			ObjectID: "fixture-resource", AccessCheckObject: "fixture_resource:fixture-resource", AccessCheckRelation: "viewer",
			HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor",
		},
		Audit: &AuditEvent{
			EventID: "fixture-event-1", Publisher: "test-publisher", ResourceRevision: 42, Origin: AuditOriginMutation,
			OccurredAt: time.Date(2026, 10, 29, 16, 0, 0, 0, time.UTC),
			Attribution: AuditAttribution{
				ActorRef: "user:testuser01", EffectiveActorRef: "user:testuser01",
				ActorProfile: fixtureAuditProfile(), EffectiveActorProfile: fixtureAuditProfile(),
			},
			Scope: AuditScope{Before: []string{"project:fixture-project"}, After: []string{"project:fixture-project"}},
			Content: &AuditContent{
				Schema: "fixture.audit.v1", Representation: AuditRepresentationSnapshots,
				Before: json.RawMessage(`{"name":"Previous Fixture"}`), After: json.RawMessage(`{"name":"Updated Fixture"}`),
			},
		},
	}
	switch action {
	case constants.ActionCreated:
		e.Audit.Scope.Before = nil
		e.Audit.Content.Before = nil
	case constants.ActionDeleted:
		e.Data = e.IndexingConfig.ObjectID
		e.Audit.Scope.After = nil
		e.Audit.Content.After = nil
	}
	return e
}

func fixtureAuditProfile() *AuditActorProfile {
	return &AuditActorProfile{
		Availability: AuditProfileCaptured,
		Name:         auditFixtureString("Test User 1"),
		AvatarURL:    auditFixtureString("https://example.com/avatar.png"),
		Email:        auditFixtureString("user-1@example.com"),
		Username:     auditFixtureString("testuser01"),
	}
}

func auditFixtureString(value string) *string { return &value }

// This allowlist is a test-only schema, not approval of any resource's audit fields.
func fixtureAuditValidator(publisher, objectType string, content AuditContent) error {
	if publisher != "test-publisher" || objectType != "fixture_resource" || content.Schema != "fixture.audit.v1" {
		return fmt.Errorf("unregistered schema ownership")
	}
	for _, snapshot := range []json.RawMessage{content.Before, content.After} {
		if snapshot == nil {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(snapshot, &data); err != nil {
			return err
		}
		if len(data) != 1 {
			return fmt.Errorf("only name is approved")
		}
		if _, ok := data["name"].(string); !ok {
			return fmt.Errorf("name must be a string")
		}
	}
	return nil
}

func TestIndexerMessageEnvelope_ValidateV2(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*IndexerMessageEnvelope)
		valid  bool
	}{
		{name: "update", valid: true},
		{name: "maximum_revision", valid: true, mutate: func(e *IndexerMessageEnvelope) { e.Audit.ResourceRevision = math.MaxInt64 }},
		{name: "scope_move", valid: true, mutate: func(e *IndexerMessageEnvelope) { e.Audit.Scope.After = []string{"project:fixture-project-2"} }},
		{name: "system_actor", valid: true, mutate: func(e *IndexerMessageEnvelope) {
			e.Audit.Attribution = AuditAttribution{ActorRef: "service:test-publisher", EffectiveActorRef: "service:test-publisher"}
		}},
		{name: "wrong_version", mutate: func(e *IndexerMessageEnvelope) { e.SchemaVersion = 3 }},
		{name: "missing_audit", mutate: func(e *IndexerMessageEnvelope) { e.Audit = nil }},
		{name: "missing_auth", mutate: func(e *IndexerMessageEnvelope) { e.Headers = nil }},
		{name: "missing_config", mutate: func(e *IndexerMessageEnvelope) { e.IndexingConfig = nil }},
		{name: "empty_id", mutate: func(e *IndexerMessageEnvelope) { e.IndexingConfig.ObjectID = "" }},
		{name: "missing_history_anchor", mutate: func(e *IndexerMessageEnvelope) { e.IndexingConfig.HistoryCheckObject = "" }},
		{name: "malformed_history_relation", mutate: func(e *IndexerMessageEnvelope) { e.IndexingConfig.HistoryCheckRelation = "auditor@user:other" }},
		{name: "literal_config_text", valid: true, mutate: func(e *IndexerMessageEnvelope) {
			e.IndexingConfig.SortName = "{{ name }}"
			e.IndexingConfig.Fulltext = "Helm example {{ .Values.image }} and a literal }}"
		}},
		{name: "contact_profile", valid: true, mutate: func(e *IndexerMessageEnvelope) {
			e.IndexingConfig.Contacts = []ContactBody{{Profile: map[string]any{"title": "Developer"}}}
		}},
		{name: "literal_contact_profile", valid: true, mutate: func(e *IndexerMessageEnvelope) {
			e.IndexingConfig.Contacts = []ContactBody{{Profile: map[string]any{"title": "Use {{ name }} or }} literally"}}}
		}},
		{name: "identity_template_rejected", mutate: func(e *IndexerMessageEnvelope) { e.IndexingConfig.ObjectID = "{{ uid }}" }},
		{name: "authorization_template_rejected", mutate: func(e *IndexerMessageEnvelope) { e.IndexingConfig.HistoryCheckObject = "project:{{ uid }}" }},
		{name: "non_json_config_rejected", mutate: func(e *IndexerMessageEnvelope) {
			e.IndexingConfig.Contacts = []ContactBody{{Profile: map[string]any{"value": math.NaN()}}}
		}},
		{name: "missing_event_identity", mutate: func(e *IndexerMessageEnvelope) { e.Audit.EventID = "" }},
		{name: "oversized_event_identity", mutate: func(e *IndexerMessageEnvelope) { e.Audit.EventID = strings.Repeat("a", 129) }},
		{name: "malformed_publisher", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Publisher = "other:publisher" }},
		{name: "missing_occurrence_time", mutate: func(e *IndexerMessageEnvelope) { e.Audit.OccurredAt = time.Time{} }},
		{name: "zero_revision", mutate: func(e *IndexerMessageEnvelope) { e.Audit.ResourceRevision = 0 }},
		{name: "revision_overflow", mutate: func(e *IndexerMessageEnvelope) { e.Audit.ResourceRevision = math.MaxUint64 }},
		{name: "missing_initiator", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Attribution.ActorRef = "" }},
		{name: "missing_effective_actor", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Attribution.EffectiveActorRef = "" }},
		{name: "profile_as_actor", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Attribution.ActorRef = "user:testuser01@example.test" }},
		{name: "wrong_actor_kind", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Attribution.ActorRef = "project:fixture-project" }},
		{name: "missing_before_scope", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Scope.Before = nil }},
		{name: "missing_after_scope", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Scope.After = nil }},
		{name: "malformed_scope", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Scope.Before = []string{"project:fixture#auditor"} }},
		{name: "wrong_action", mutate: func(e *IndexerMessageEnvelope) { e.Action = constants.ActionUpdate }},
		{name: "data_array", mutate: func(e *IndexerMessageEnvelope) { e.Data = []string{"fixture-resource"} }},
		{name: "data_null", mutate: func(e *IndexerMessageEnvelope) { e.Data = nil }},
		{name: "missing_content", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Content = nil }},
		{name: "unapproved_schema", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Content.Schema = "unapproved.audit.v1" }},
		{name: "unknown_representation", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Content.Representation = "diff" }},
		{name: "missing_before_snapshot", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Content.Before = nil }},
		{name: "missing_after_snapshot", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Content.After = nil }},
		{name: "null_snapshot", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Content.After = json.RawMessage(`null`) }},
		{name: "array_snapshot", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Content.After = json.RawMessage(`[]`) }},
		{name: "duplicate_snapshot_keys", mutate: func(e *IndexerMessageEnvelope) {
			e.Audit.Content.After = json.RawMessage(`{"name":"Fixture","name":"Other Fixture"}`)
		}},
		{name: "invalid_utf8_raw_snapshot", mutate: func(e *IndexerMessageEnvelope) {
			e.Audit.Content.After = json.RawMessage(bytesReplace([]byte(`{"name":"Fixture"}`), "Fixture", string([]byte{0xff})))
		}},
		{name: "invalid_utf8_raw_data", mutate: func(e *IndexerMessageEnvelope) {
			e.Data = json.RawMessage(bytesReplace([]byte(`{"name":"Fixture"}`), "Fixture", string([]byte{0xff})))
		}},
		{name: "prohibited_snapshot_field", mutate: func(e *IndexerMessageEnvelope) {
			e.Audit.Content.After = json.RawMessage(`{"name":"Fixture","authorization":"fixture-secret"}`)
		}},
		{name: "unknown_origin", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Origin = "unknown" }},
		{name: "projection_with_snapshot", mutate: func(e *IndexerMessageEnvelope) { e.Audit.Origin = AuditOriginReindex }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := fixtureAuditEnvelope(constants.ActionUpdated)
			if tt.mutate != nil {
				tt.mutate(e)
			}
			err := e.ValidateV2("fixture_resource", fixtureAuditValidator)
			if tt.valid {
				require.NoError(t, err)
				payload, err := json.Marshal(e)
				require.NoError(t, err)
				decoded, err := DecodeV2Envelope(payload, "fixture_resource", fixtureAuditValidator)
				require.NoError(t, err)
				require.Equal(t, e.IndexingConfig, decoded.IndexingConfig, "version 2 must preserve literal config strings")
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "fixture-secret")
			}
		})
	}
	for _, action := range []constants.MessageAction{constants.ActionCreated, constants.ActionUpdated, constants.ActionDeleted} {
		t.Run(string(action)+"_snapshot_rules", func(t *testing.T) {
			e := fixtureAuditEnvelope(action)
			require.NoError(t, e.ValidateV2("fixture_resource", fixtureAuditValidator))
			e.Audit.Content.Before, e.Audit.Content.After = e.Audit.Content.After, e.Audit.Content.Before
			if action != constants.ActionUpdated {
				require.Error(t, e.ValidateV2("fixture_resource", fixtureAuditValidator))
			}
		})
	}
	for _, origin := range []AuditOrigin{AuditOriginReindex, AuditOriginBackfill, AuditOriginSyncEcho} {
		t.Run(string(origin)+"_no_history_content", func(t *testing.T) {
			e := fixtureAuditEnvelope(constants.ActionUpdated)
			e.Audit.Origin, e.Audit.Content = origin, nil
			require.NoError(t, e.ValidateV2("fixture_resource", nil))
		})
	}
	t.Run("schema_registry_required", func(t *testing.T) {
		require.ErrorContains(t, fixtureAuditEnvelope(constants.ActionUpdated).ValidateV2("fixture_resource", nil), "registered schema validator")
	})
	t.Run("delete_identity_mismatch", func(t *testing.T) {
		e := fixtureAuditEnvelope(constants.ActionDeleted)
		e.Data = "another-resource"
		require.ErrorContains(t, e.ValidateV2("fixture_resource", fixtureAuditValidator), "must match")
	})
}

func TestDecodeV2Envelope(t *testing.T) {
	e := fixtureAuditEnvelope(constants.ActionUpdated)
	e.Audit.ResourceRevision = 9007199254740993 // Must not round through float64.
	e.Data.(map[string]any)["large_integer"] = json.Number("9007199254740993")
	payload, err := json.Marshal(e)
	require.NoError(t, err)
	tests := []struct {
		name    string
		payload []byte
		valid   bool
	}{
		{name: "valid", payload: payload, valid: true},
		{name: "unknown_top_field", payload: append([]byte(`{"unexpected":true,`), payload[1:]...)},
		{name: "wrong_field_case", payload: bytesReplace(payload, `"audit":`, `"Audit":`)},
		{name: "unknown_nested_field", payload: bytesReplace(payload, `"actor_ref":`, `"unknown_actor_ref":`)},
		{name: "duplicate_key", payload: append([]byte(`{"schema_version":2,`), payload[1:]...)},
		{name: "duplicate_snapshot_field", payload: bytesReplace(payload, `"name":"Updated Fixture"`, `"name":"Updated Fixture","name":"Other Fixture"`)},
		{name: "invalid_timestamp", payload: bytesReplace(payload, `2026-10-29T16:00:00Z`, `fixture-secret`)},
		{name: "fractional_revision", payload: bytesReplace(payload, `9007199254740993`, `42.5`)},
		{name: "fractional_version", payload: bytesReplace(payload, `"schema_version":2`, `"schema_version":2.0`)},
		{name: "trailing_value", payload: append(append([]byte{}, payload...), []byte(` {}`)...)},
		{name: "null", payload: []byte(`null`)},
		{name: "too_large", payload: []byte(strings.Repeat(" ", MaxAuditEnvelopeBytes+1))},
		{name: "too_deep", payload: bytesReplace(payload, `"name":"Updated Fixture"`, `"name":`+strings.Repeat(`[`, 70)+`0`+strings.Repeat(`]`, 70))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := DecodeV2Envelope(tt.payload, "fixture_resource", fixtureAuditValidator)
			if tt.valid {
				require.NoError(t, err)
				require.Equal(t, uint64(9007199254740993), decoded.Audit.ResourceRevision)
				require.Equal(t, json.Number("9007199254740993"), decoded.Data.(map[string]any)["large_integer"])
			} else {
				require.Error(t, err)
				require.Nil(t, decoded)
				require.NotContains(t, err.Error(), "fixture-secret")
			}
		})
	}
}

func bytesReplace(payload []byte, old, replacement string) []byte {
	return []byte(strings.ReplaceAll(string(payload), old, replacement))
}

func TestIndexerMessageEnvelope_LegacyWireCompatibility(t *testing.T) {
	e := IndexerMessageEnvelope{Action: constants.ActionDeleted, Data: "fixture-resource"}
	payload, err := json.Marshal(e)
	require.NoError(t, err)
	require.NotContains(t, string(payload), `"schema_version"`)
	require.NotContains(t, string(payload), `"audit"`)
}

func TestAuditV2ClientGuideExample(t *testing.T) {
	guide, err := os.ReadFile("../../docs/client-guide.md")
	require.NoError(t, err)
	_, section, found := strings.Cut(string(guide), "## Audit Envelope Version 2")
	require.True(t, found)
	_, example, found := strings.Cut(section, "```json\n")
	require.True(t, found)
	example, _, found = strings.Cut(example, "\n```")
	require.True(t, found)
	_, err = DecodeV2Envelope([]byte(example), "fixture_resource", fixtureAuditValidator)
	require.NoError(t, err, "the illustrative client-guide envelope must match the public contract")
}

func TestDocumentedEnvelopeJSONTags(t *testing.T) {
	for _, path := range []string{"../../docs/client-guide.md", "../../docs/indexer-contract.md"} {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			_, snippet, found := strings.Cut(string(data), "```go\n")
			require.True(t, found)
			snippet, _, found = strings.Cut(snippet, "\n```")
			require.True(t, found)
			file, err := parser.ParseFile(token.NewFileSet(), "example.go", "package example\n"+snippet, 0)
			require.NoError(t, err)
			tags := make(map[string]string)
			ast.Inspect(file, func(node ast.Node) bool {
				decl, ok := node.(*ast.TypeSpec)
				if !ok || decl.Name.Name != "IndexerMessageEnvelope" {
					return true
				}
				body, ok := decl.Type.(*ast.StructType)
				require.True(t, ok)
				for _, field := range body.Fields.List {
					require.NotNil(t, field.Tag)
					tag, err := strconv.Unquote(field.Tag.Value)
					require.NoError(t, err)
					for _, name := range field.Names {
						tags[name.Name] = reflect.StructTag(tag).Get("json")
					}
				}
				return false
			})
			envelope := reflect.TypeOf(IndexerMessageEnvelope{})
			require.Len(t, tags, envelope.NumField())
			for i := 0; i < envelope.NumField(); i++ {
				field := envelope.Field(i)
				require.Equal(t, field.Tag.Get("json"), tags[field.Name], "wire tag for %s", field.Name)
			}
		})
	}
}
