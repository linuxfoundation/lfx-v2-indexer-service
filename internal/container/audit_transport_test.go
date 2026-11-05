// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package container

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// TestAuditOpenSearchClient_ExportedSpans exercises the actual container wiring
// with tracing enabled, including duplicate read-backs, errors and janitor writes.
func TestAuditOpenSearchClient_ExportedSpans(t *testing.T) {
	for _, scenario := range []string{"success", "history duplicate", "receipt duplicate", "server rejection", "transport failure"} {
		t.Run(scenario, func(t *testing.T) {
			const eventID = "fixture-user-event-private"
			const resourceID = "fixture-user-resource-private"
			const actorID = "user:fixture-private-subject"
			const legacyID = "fixture-user-legacy-private"
			const username, password = "fixture-private-runtime", "fixture-private-password"
			const authorization = "Bearer fixture-private-authorization"
			profile := &types.AuditActorProfile{Availability: types.AuditProfileUnavailable}
			e := &types.IndexerMessageEnvelope{
				SchemaVersion: types.AuditSchemaVersion, Action: constants.ActionUpdated,
				Headers: map[string]string{constants.AuthorizationHeader: authorization},
				Data:    map[string]any{"uid": resourceID},
				IndexingConfig: &types.IndexingConfig{ObjectID: resourceID, AccessCheckObject: "fixture_resource:" + resourceID,
					AccessCheckRelation: "viewer", HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor"},
				Audit: &types.AuditEvent{Publisher: "test-publisher", EventID: eventID, Origin: types.AuditOriginMutation, ResourceRevision: 2,
					OccurredAt:  time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
					Attribution: types.AuditAttribution{ActorRef: actorID, EffectiveActorRef: actorID, ActorProfile: profile, EffectiveActorProfile: profile},
					Scope:       types.AuditScope{Before: []string{"project:fixture-project"}, After: []string{"project:fixture-project"}},
					Content: &types.AuditContent{Schema: "fixture.audit.v1", Representation: types.AuditRepresentationSnapshots,
						Before: json.RawMessage(`{"name":"before"}`), After: json.RawMessage(`{"name":"after"}`)},
				},
			}
			fingerprint, err := contracts.AuditFingerprint(e, "fixture_resource")
			require.NoError(t, err)
			mappings := auditMappingFixtures(t)
			cleanupUpdated := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				user, pass, ok := r.BasicAuth()
				if !ok || user != username || pass != password {
					t.Error("OpenSearch client did not preserve runtime authentication")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				path := r.URL.Path
				if strings.HasSuffix(path, "/_mapping") {
					index := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/_mapping")
					_ = json.NewEncoder(w).Encode(map[string]json.RawMessage{index: mappings[index]})
					return
				}
				switch path {
				case "/resource-history-v1/_doc/test-publisher:" + eventID:
					if r.Method == http.MethodGet {
						_ = json.NewEncoder(w).Encode(map[string]any{"_source": map[string]any{
							"publisher": "test-publisher", "event_id": eventID, "fingerprint": fingerprint,
							"object_id": resourceID, "object_type": "fixture_resource", "resource_revision": 2, "action": constants.ActionUpdated,
						}})
					} else if scenario == "history duplicate" {
						w.WriteHeader(http.StatusConflict)
					} else {
						w.WriteHeader(http.StatusCreated)
					}
				case "/resource-audit-deliveries-v1/_doc/test-publisher:" + eventID:
					if r.Method == http.MethodGet {
						_ = json.NewEncoder(w).Encode(map[string]any{"_source": map[string]any{
							"publisher": "test-publisher", "event_id": eventID, "origin": types.AuditOriginMutation, "fingerprint": fingerprint,
						}})
					} else if scenario == "receipt duplicate" {
						w.WriteHeader(http.StatusConflict)
					} else {
						w.WriteHeader(http.StatusCreated)
					}
				case "/resources/_update/fixture_resource:" + resourceID:
					switch scenario {
					case "server rejection":
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, eventID+" "+password)
					case "transport failure":
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error("failed to simulate broken OpenSearch connection")
							return
						}
						_ = conn.Close()
					default:
						_ = json.NewEncoder(w).Encode(map[string]any{"result": "updated", "get": map[string]any{"_source": map[string]any{
							"audit_event_key": "test-publisher:" + eventID, "audit_event_fingerprint": fingerprint,
						}}})
					}
				case "/resources/_search":
					_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": []any{
						map[string]any{"_id": "fixture_resource:" + resourceID, "_seq_no": 1, "_primary_term": 1, "_source": map[string]any{"audit_source_revision": 2}},
						map[string]any{"_id": legacyID, "_seq_no": 1, "_primary_term": 1, "_source": map[string]any{}},
					}}})
				case "/resources/_update/" + legacyID:
					_, _ = io.WriteString(w, `{"result":"updated"}`)
					select {
					case cleanupUpdated <- struct{}{}:
					default:
					}
				case "/resources/_doc/legacy-http-control":
					w.WriteHeader(http.StatusCreated)
				default:
					t.Errorf("unexpected OpenSearch operation: %s", r.Method)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSyncer(exporter))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			defer func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) }()
			policyFile := filepath.Join(t.TempDir(), "policy.json")
			require.NoError(t, os.WriteFile(policyFile, []byte(auditFixturePolicy), 0600))
			// The workflow and janitor log concurrently; do not share the
			// unsynchronized bytes.Buffer returned by logging.TestLogger.
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			c := &Container{Logger: logger, Config: &config.AppConfig{
				OpenSearch: config.OpenSearchConfig{URL: server.URL, Index: "resources", Username: username, Password: password, Timeout: 2 * time.Second},
				Audit:      config.AuditConfig{Enabled: true, PolicyFile: policyFile, RequiredTypes: []string{"fixture_resource"}},
				JWT:        config.JWTConfig{Issuer: "https://issuer.invalid/", Audiences: []string{"fixture"}, JWKSURL: server.URL + "/jwks"},
			}}
			require.NoError(t, c.initializeOpenSearchClients())
			require.NotSame(t, c.OpenSearchClient, c.AuditOpenSearchClient)
			require.NoError(t, c.initializeRepositories())
			auth := mocks.NewMockAuthRepository()
			auth.ValidateTokenResponse = &contracts.Principal{Principal: "fixture@clients"}
			c.AuthRepository, c.MessagingRepository = auth, mocks.NewMockMessagingRepository()
			require.NoError(t, c.initializeApplication())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx, span := provider.Tracer("audit-privacy-fixture").Start(ctx, "audit-delivery")
			c.CleanupRepository.StartItemLoop(ctx)
			var stopOnce sync.Once
			stop := func() { stopOnce.Do(func() { cancel(); c.CleanupRepository.Shutdown(); span.End() }) }
			defer stop()
			payload, err := json.Marshal(e)
			require.NoError(t, err)
			err = c.MessageProcessor.ProcessIndexingMessage(ctx, payload, constants.IndexPrefix+"fixture_resource")
			switch scenario {
			case "server rejection":
				require.ErrorContains(t, err, "status 400")
			case "transport failure":
				require.ErrorContains(t, err, "transport failure")
			default:
				require.NoError(t, err)
			}
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			c.CleanupRepository.CheckAuditedItem("fixture_resource:" + resourceID)
			select {
			case <-cleanupUpdated:
			case <-ctx.Done():
				t.Fatal("audited janitor update did not finish")
			}
			// Positive control: tracing really is active for ordinary indexing.
			require.NoError(t, c.StorageRepository.Index(ctx, "resources", "legacy-http-control", bytes.NewBufferString(`{}`)))
			stop()
			spans := exporter.GetSpans()
			require.Len(t, spans, 2, "only the parent and ordinary-indexing control span should export")
			encoded, err := json.Marshal(spans)
			require.NoError(t, err)
			require.Contains(t, string(encoded), "legacy-http-control")
			for _, marker := range []string{eventID, resourceID, actorID, legacyID, username, password, authorization, base64.StdEncoding.EncodeToString([]byte(username + ":" + password))} {
				require.NotContains(t, string(encoded), marker)
			}
		})
	}
}
