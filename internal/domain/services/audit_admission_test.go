// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func newTestAuditAuth(principal string) *mocks.MockAuthRepository {
	auth := mocks.NewMockAuthRepository()
	auth.ValidateTokenResponse = &contracts.Principal{Principal: principal}
	return auth
}

// TestAuditPolicies_PublisherNamespaces checks policy loading and direct
// admission construction enforce the same namespace grammar and length bounds.
func TestAuditPolicies_PublisherNamespaces(t *testing.T) {
	for _, tt := range []struct {
		name, publisher string
		valid           bool
	}{
		{"shortest", "a", true},
		{"supported separators", "fixture-publisher_2", true},
		{"maximum length", "a" + strings.Repeat("b", 63), true},
		{"empty", "", false},
		{"uppercase and dot", "Committee.Service", false},
		{"identity separator", "a:b", false},
		{"leading digit", "2publisher", false},
		{"oversized", "a" + strings.Repeat("b", 64), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := testAuditPolicy()
			policy.Publisher = tt.publisher
			_, admissionErr := NewAuditAdmission(newTestAuditAuth(""), []AuditPublisherPolicy{policy})
			var manifest AuditPolicyManifest
			require.NoError(t, json.Unmarshal([]byte(testPolicyManifest), &manifest))
			manifest.Publishers[0].Publisher = tt.publisher
			encoded, err := json.Marshal(manifest)
			require.NoError(t, err)
			_, decodeErr := DecodeAuditPolicies(encoded)
			if tt.valid {
				require.NoError(t, admissionErr)
				require.NoError(t, decodeErr)
			} else {
				require.ErrorContains(t, admissionErr, "incomplete audit publisher policy")
				require.ErrorContains(t, decodeErr, "invalid audit policy publisher")
			}
		})
	}
}

// TestAuditPolicies_ResourceTypes rejects unusable resource policies at both
// manifest loading and direct construction, using the wire subject grammar.
func TestAuditPolicies_ResourceTypes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		valid bool
	}{
		{"fixture_resource", true}, {"a" + strings.Repeat("b", 63), true},
		{"index", false}, {"", false}, {"a" + strings.Repeat("b", 64), false},
		{"a" + strings.Repeat("b", 127), false}, {"fixture-resource", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := testAuditPolicy()
			resource := policy.Resources["fixture_resource"]
			policy.Resources = map[string]AuditResourcePolicy{tt.name: resource}
			_, admissionErr := NewAuditAdmission(newTestAuditAuth(""), []AuditPublisherPolicy{policy})
			var manifest AuditPolicyManifest
			require.NoError(t, json.Unmarshal([]byte(testPolicyManifest), &manifest))
			definition := manifest.Publishers[0].Resources["fixture_resource"]
			manifest.Publishers[0].Resources = map[string]AuditResourceManifest{tt.name: definition}
			data, err := json.Marshal(manifest)
			require.NoError(t, err)
			_, decodeErr := DecodeAuditPolicies(data)
			if tt.valid {
				require.NoError(t, admissionErr)
				require.NoError(t, decodeErr)
			} else {
				require.ErrorContains(t, admissionErr, "incomplete audit resource policy")
				require.ErrorContains(t, decodeErr, "invalid audit policy resource")
			}
		})
	}
}

func auditAdmissionFixture() *types.IndexerMessageEnvelope {
	return &types.IndexerMessageEnvelope{
		SchemaVersion: types.AuditSchemaVersion, Action: constants.ActionUpdated,
		Headers:        map[string]string{constants.AuthorizationHeader: "Bearer fixture-credential"},
		Data:           map[string]any{"uid": "fixture-resource"},
		IndexingConfig: &types.IndexingConfig{ObjectID: "fixture-resource", AccessCheckObject: "fixture_resource:fixture-resource", AccessCheckRelation: "viewer", HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor"},
		Audit: &types.AuditEvent{EventID: "fixture-event-1", Publisher: "test-publisher", ResourceRevision: 42, Origin: types.AuditOriginMutation,
			OccurredAt:  time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
			Attribution: types.AuditAttribution{ActorRef: "service:fixture", EffectiveActorRef: "service:fixture"},
			Scope:       types.AuditScope{Before: []string{"project:fixture-project"}, After: []string{"project:fixture-project"}},
			Content: &types.AuditContent{Schema: "fixture.audit.v1", Representation: types.AuditRepresentationSnapshots,
				Before: json.RawMessage(`{"name":"Before"}`), After: json.RawMessage(`{"name":"After"}`)},
		},
	}
}

// testAuditPolicy trusts one synthetic machine principal and a closed snapshot.
func testAuditPolicy() AuditPublisherPolicy {
	return AuditPublisherPolicy{Publisher: "test-publisher", Principal: "fixture@clients", CanAttestActors: true,
		Resources: map[string]AuditResourcePolicy{"fixture_resource": {
			Origins: map[types.AuditOrigin]bool{types.AuditOriginMutation: true, types.AuditOriginReindex: true},
			Schemas: map[string]func(before, after json.RawMessage) error{"fixture.audit.v1": func(before, after json.RawMessage) error {
				for _, snapshot := range []json.RawMessage{before, after} {
					var value map[string]json.RawMessage
					if err := json.Unmarshal(snapshot, &value); err != nil {
						return fmt.Errorf("invalid snapshot")
					}
					if len(value) != 1 || value["name"] == nil {
						return fmt.Errorf("unapproved snapshot field")
					}
				}
				return nil
			}},
		}},
	}
}

// TestAuditAdmission_Admit checks authenticated publisher ownership, schema
// allowlisting and origin restrictions before any storage operation.
func TestAuditAdmission_Admit(t *testing.T) {
	for _, tt := range []struct {
		name      string
		principal string
		modify    func(*types.IndexerMessageEnvelope)
		wantError string
	}{
		{name: "trusted mutation", principal: "fixture@clients"},
		{name: "wrong authenticated producer", principal: "service:other", wantError: "authentication failed"},
		{name: "unregistered publisher", principal: "fixture@clients", modify: func(e *types.IndexerMessageEnvelope) { e.Audit.Publisher = "other" }, wantError: "rejected by registered schema"},
		{name: "unregistered schema", principal: "fixture@clients", modify: func(e *types.IndexerMessageEnvelope) { e.Audit.Content.Schema = "unknown" }, wantError: "rejected by registered schema"},
		{name: "unapproved field", principal: "fixture@clients", modify: func(e *types.IndexerMessageEnvelope) {
			e.Audit.Content.After = json.RawMessage(`{"name":"After","secret":"forbidden"}`)
		}, wantError: "rejected by registered schema"},
		{name: "unsupported origin", principal: "fixture@clients", modify: func(e *types.IndexerMessageEnvelope) {
			e.Audit.Origin = types.AuditOriginSyncEcho
			e.Audit.Content = nil
		}, wantError: "not authorized"},
		{name: "approved reindex", principal: "fixture@clients", modify: func(e *types.IndexerMessageEnvelope) {
			e.Audit.Origin = types.AuditOriginReindex
			e.Audit.Content = nil
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			admission, err := NewAuditAdmission(newTestAuditAuth(tt.principal), []AuditPublisherPolicy{testAuditPolicy()})
			require.NoError(t, err)
			e := auditAdmissionFixture()
			if tt.modify != nil {
				tt.modify(e)
			}
			payload, err := json.Marshal(e)
			require.NoError(t, err)
			_, err = admission.Admit(context.Background(), payload, "fixture_resource")
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAuditAdmission_FailClosed(t *testing.T) {
	_, err := NewAuditAdmission(newTestAuditAuth(""), []AuditPublisherPolicy{{Publisher: "test", Principal: "fixture@clients"}})
	require.ErrorContains(t, err, "incomplete")
	empty, err := NewAuditAdmission(newTestAuditAuth(""), nil)
	require.NoError(t, err)
	payload, err := json.Marshal(auditAdmissionFixture())
	require.NoError(t, err)
	_, err = empty.Admit(context.Background(), payload, "fixture_resource")
	require.ErrorContains(t, err, "not configured")
}

func TestAuditAdmission_PolicyIsFrozen(t *testing.T) {
	policy := testAuditPolicy()
	a, err := NewAuditAdmission(newTestAuditAuth("fixture@clients"), []AuditPublisherPolicy{policy})
	require.NoError(t, err)
	resource := policy.Resources["fixture_resource"]
	delete(resource.Schemas, "fixture.audit.v1")
	delete(resource.Origins, types.AuditOriginMutation)
	delete(policy.Resources, "fixture_resource")
	payload, err := json.Marshal(auditAdmissionFixture())
	require.NoError(t, err)
	_, err = a.Admit(context.Background(), payload, "fixture_resource")
	require.NoError(t, err)
}
