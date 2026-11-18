// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

type auditConcurrentResult struct {
	created, applied bool
	err              error
}

// Launch only after every worker reaches the barrier. Assertions stay on the
// test goroutine, and every repository request uses the shared bounded context.
func runAuditRace(t *testing.T, workers int, operation func(context.Context, int) auditConcurrentResult) []auditConcurrentResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ready := make(chan struct{}, workers)
	start := make(chan struct{})
	results := make([]auditConcurrentResult, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-start
			results[i] = operation(ctx, i)
		}()
	}
	for range workers {
		<-ready
	}
	close(start)
	wg.Wait()
	require.NoError(t, ctx.Err(), "concurrent operations exceeded their deadline")
	return results
}

// concurrentAuditEnvelope supplies complete, immutable synthetic mutation data
// before any worker starts, avoiding shared writes in the race fixtures.
func concurrentAuditEnvelope(resource, event string, revision uint64, action constants.MessageAction) *types.IndexerMessageEnvelope {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	e := &types.IndexerMessageEnvelope{
		SchemaVersion: types.AuditSchemaVersion, Action: action,
		Headers: map[string]string{constants.AuthorizationHeader: "Bearer fixture-credential"},
		Data:    map[string]any{"uid": resource, "name": event},
		IndexingConfig: &types.IndexingConfig{ObjectID: resource, AccessCheckObject: "fixture_resource:" + resource,
			AccessCheckRelation: "viewer", HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor"},
		Audit: &types.AuditEvent{Publisher: "test-publisher", EventID: event, ResourceRevision: revision,
			Origin: types.AuditOriginMutation, OccurredAt: now,
			Attribution: types.AuditAttribution{ActorRef: "service:fixture", EffectiveActorRef: "service:fixture"},
			Scope:       types.AuditScope{Before: []string{"project:fixture-project"}, After: []string{"project:fixture-project"}},
			Content: &types.AuditContent{Schema: "fixture.audit.v1", Representation: types.AuditRepresentationSnapshots,
				Before: json.RawMessage(`{"name":"before"}`), After: json.RawMessage(`{"name":"after"}`)},
		},
	}
	if action == constants.ActionCreated {
		e.Audit.Scope.Before, e.Audit.Content.Before = nil, nil
	}
	if action == constants.ActionDeleted {
		e.Data = resource
		e.Audit.Scope.After, e.Audit.Content.After = nil, nil
	}
	return e
}

// concurrentAuditRecord freezes the approved synthetic history before dispatch.
func concurrentAuditRecord(t *testing.T, e *types.IndexerMessageEnvelope) *contracts.HistoryRecord {
	t.Helper()
	// This fixture approves only its synthetic schema identifier; admission itself
	// is covered by the application/domain tests, not these storage races.
	record, err := contracts.NewHistoryRecord(e, "fixture_resource", func(_, _ string, content types.AuditContent) error {
		if content.Schema != "fixture.audit.v1" {
			return fmt.Errorf("unapproved fixture schema")
		}
		return nil
	}, e.Audit.OccurredAt.Add(time.Minute))
	require.NoError(t, err)
	return record
}

func readAuditSource(t *testing.T, client *opensearch.Client, index, id string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := client.Get(index, url.PathEscape(id), client.Get.WithContext(ctx))
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, http.StatusOK, res.StatusCode) // never print indexed payloads
	var found struct {
		Source map[string]any `json:"_source"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&found))
	return found.Source
}

// TestAuditOpenSearch_ConcurrentWrites checks revision/identity races, duplicate
// deliveries and projection ordering through real OpenSearch adapters.
func TestAuditOpenSearch_ConcurrentWrites(t *testing.T) {
	client, currentIndex := setupAuditIntegration(t)
	history := NewHistoryRepository(client)
	receipts := NewAuditDeliveryReceiptRepository(client, 10*time.Second)
	current := NewAuditProjectionRepository(client, currentIndex)
	process := func(ctx context.Context, e *types.IndexerMessageEnvelope, record *contracts.HistoryRecord, fingerprint string) auditConcurrentResult {
		result := auditConcurrentResult{}
		result.err = receipts.CreateOrVerify(ctx, contracts.AuditDeliveryReceipt{
			Publisher: e.Audit.Publisher, EventID: e.Audit.EventID, Origin: e.Audit.Origin, Fingerprint: fingerprint,
		})
		if result.err == nil && record != nil {
			result.created, result.err = history.CreateOrVerify(ctx, record)
		}
		if result.err == nil {
			_, result.applied, result.err = current.ApplyIfNewer(ctx, e, "fixture_resource", fingerprint)
		}
		return result
	}

	for _, newestAction := range []constants.MessageAction{constants.ActionUpdated, constants.ActionDeleted} {
		t.Run("revision-order/"+string(newestAction), func(t *testing.T) {
			const workers = 4
			envelopes := make([]*types.IndexerMessageEnvelope, workers)
			records := make([]*contracts.HistoryRecord, workers)
			resource := "concurrent-revisions-" + string(newestAction)
			for i := range workers {
				action := constants.ActionUpdated
				switch i {
				case workers - 1:
					action = newestAction
				case 1:
					action = constants.ActionDeleted
				}
				envelopes[i] = concurrentAuditEnvelope(resource, fmt.Sprintf("%s-event-%d", resource, i), uint64(i+1), action)
				records[i] = concurrentAuditRecord(t, envelopes[i])
			}
			results := runAuditRace(t, workers, func(ctx context.Context, i int) auditConcurrentResult {
				return process(ctx, envelopes[i], records[i], records[i].Fingerprint)
			})
			for i, result := range results {
				require.NoError(t, result.err)
				require.True(t, result.created)
				stored := readAuditSource(t, client, historyIndex, "test-publisher:"+envelopes[i].Audit.EventID)
				require.Equal(t, records[i].Fingerprint, stored["fingerprint"])
			}
			winner := envelopes[workers-1]
			stored := readAuditSource(t, client, currentIndex, "fixture_resource:"+resource)
			require.Equal(t, float64(workers), stored["audit_source_revision"])
			require.Equal(t, "test-publisher:"+winner.Audit.EventID, stored["audit_event_key"])
			require.Equal(t, true, stored["latest"])
			if newestAction == constants.ActionDeleted {
				require.NotEmpty(t, stored["deleted_at"])
				require.Empty(t, stored["data"])
			} else {
				require.NotContains(t, stored, "deleted_at")
				require.Equal(t, winner.Audit.EventID, stored["data"].(map[string]any)["name"])
			}
		})
	}

	t.Run("duplicate-delivery", func(t *testing.T) {
		e := concurrentAuditEnvelope("concurrent-duplicate", "concurrent-duplicate-event", 1, constants.ActionCreated)
		record := concurrentAuditRecord(t, e)
		records := make([]contracts.HistoryRecord, 4)
		for i := range records {
			records[i] = *record
			records[i].IngestedAt = record.IngestedAt.Add(time.Duration(i) * time.Second)
		}
		results := runAuditRace(t, len(records), func(ctx context.Context, i int) auditConcurrentResult {
			return process(ctx, e, &records[i], record.Fingerprint)
		})
		created, applied, winner := 0, 0, -1
		for i, result := range results {
			require.NoError(t, result.err)
			if result.created {
				created++
				winner = i
			}
			if result.applied {
				applied++
			}
		}
		require.Equal(t, 1, created)
		require.Equal(t, 1, applied)
		stored := readAuditSource(t, client, historyIndex, "test-publisher:"+e.Audit.EventID)
		require.Equal(t, record.Fingerprint, stored["fingerprint"])
		require.Equal(t, records[winner].IngestedAt.Format(time.RFC3339Nano), stored["ingested_at"])
	})

	t.Run("equal-revision-conflict", func(t *testing.T) {
		envelopes := []*types.IndexerMessageEnvelope{
			concurrentAuditEnvelope("concurrent-conflict", "concurrent-conflict-a", 1, constants.ActionUpdated),
			concurrentAuditEnvelope("concurrent-conflict", "concurrent-conflict-b", 1, constants.ActionDeleted),
		}
		records := []*contracts.HistoryRecord{concurrentAuditRecord(t, envelopes[0]), concurrentAuditRecord(t, envelopes[1])}
		results := runAuditRace(t, 2, func(ctx context.Context, i int) auditConcurrentResult {
			return process(ctx, envelopes[i], records[i], records[i].Fingerprint)
		})
		winner, conflicts := -1, 0
		for i, result := range results {
			require.True(t, result.created) // source mutations survive even if current rejects one
			if result.err != nil {
				require.ErrorContains(t, result.err, "status 400")
				conflicts++
			} else {
				require.True(t, result.applied)
				winner = i
			}
		}
		require.Equal(t, 1, conflicts)
		require.NotEqual(t, -1, winner)
		stored := readAuditSource(t, client, currentIndex, "fixture_resource:concurrent-conflict")
		require.Equal(t, "test-publisher:"+envelopes[winner].Audit.EventID, stored["audit_mutation_key"])
		for i, e := range envelopes {
			stored := readAuditSource(t, client, historyIndex, "test-publisher:"+e.Audit.EventID)
			require.Equal(t, records[i].Fingerprint, stored["fingerprint"])
		}
	})

	t.Run("delivery-identity-conflict", func(t *testing.T) {
		envelopes := []*types.IndexerMessageEnvelope{
			concurrentAuditEnvelope("concurrent-identity", "concurrent-shared-identity", 1, constants.ActionCreated),
			concurrentAuditEnvelope("concurrent-identity", "concurrent-shared-identity", 1, constants.ActionCreated),
		}
		envelopes[1].Data = map[string]any{"uid": "concurrent-identity", "name": "different"}
		records := []*contracts.HistoryRecord{concurrentAuditRecord(t, envelopes[0]), concurrentAuditRecord(t, envelopes[1])}
		results := runAuditRace(t, 2, func(ctx context.Context, i int) auditConcurrentResult {
			return process(ctx, envelopes[i], records[i], records[i].Fingerprint)
		})
		winner, conflicts := -1, 0
		for i, result := range results {
			if result.err != nil {
				require.ErrorContains(t, result.err, "identity reused")
				require.False(t, result.created)
				require.False(t, result.applied)
				conflicts++
			} else {
				require.True(t, result.created)
				require.True(t, result.applied)
				winner = i
			}
		}
		require.Equal(t, 1, conflicts)
		require.NotEqual(t, -1, winner)
		for _, index := range []string{historyIndex, auditDeliveryReceiptsIndex} {
			stored := readAuditSource(t, client, index, "test-publisher:concurrent-shared-identity")
			require.Equal(t, records[winner].Fingerprint, stored["fingerprint"])
		}
		stored := readAuditSource(t, client, currentIndex, "fixture_resource:concurrent-identity")
		require.Equal(t, records[winner].Fingerprint, stored["audit_event_fingerprint"])
	})

	t.Run("mutation-and-projection-order", func(t *testing.T) {
		mutation := concurrentAuditEnvelope("concurrent-projections", "concurrent-projection-mutation", 1, constants.ActionUpdated)
		record := concurrentAuditRecord(t, mutation)
		envelopes := []*types.IndexerMessageEnvelope{mutation}
		fingerprints := []string{record.Fingerprint}
		for _, suffix := range []string{"a", "b"} {
			e := concurrentAuditEnvelope("concurrent-projections", "concurrent-projection-"+suffix, 1, constants.ActionUpdated)
			e.Audit.Origin, e.Audit.Content = types.AuditOriginReindex, nil
			e.Audit.OccurredAt = e.Audit.OccurredAt.Add(time.Second) // tied timestamps exercise identity ordering
			envelopes = append(envelopes, e)
			fingerprint, err := contracts.AuditFingerprint(e, "fixture_resource")
			require.NoError(t, err)
			fingerprints = append(fingerprints, fingerprint)
		}
		results := runAuditRace(t, len(envelopes), func(ctx context.Context, i int) auditConcurrentResult {
			var historyRecord *contracts.HistoryRecord
			if i == 0 {
				historyRecord = record
			}
			return process(ctx, envelopes[i], historyRecord, fingerprints[i])
		})
		for _, result := range results {
			require.NoError(t, result.err)
		}
		stored := readAuditSource(t, client, currentIndex, "fixture_resource:concurrent-projections")
		require.Equal(t, "test-publisher:concurrent-projection-b", stored["audit_event_key"])
		require.Equal(t, "test-publisher:concurrent-projection-mutation", stored["audit_mutation_key"])
		require.Equal(t, record.Fingerprint, stored["audit_mutation_fingerprint"])
		require.Equal(t, "concurrent-projection-b", stored["data"].(map[string]any)["name"])
		for i, e := range envelopes {
			var historyRecord *contracts.HistoryRecord
			if i == 0 {
				historyRecord = record
			}
			result := process(context.Background(), e, historyRecord, fingerprints[i])
			require.NoError(t, result.err)
			require.False(t, result.created)
			require.False(t, result.applied)
		}
	})
}

type auditVerificationGate struct {
	base    http.RoundTripper
	path    string
	reached chan struct{}
	release <-chan struct{}
	once    sync.Once
}

// RoundTrip pauses only the selected verification GET so erasure can commit
// deterministically after an append conflict but before its read-back.
func (g *auditVerificationGate) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet && r.URL.Path == g.path {
		g.once.Do(func() { close(g.reached) })
		select {
		case <-g.release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	return g.base.RoundTrip(r)
}

// TestAuditOpenSearch_ConcurrentErasure checks completed erasure leaves only its
// permanent fence despite concurrent creates, redelivery and read-back races.
func TestAuditOpenSearch_ConcurrentErasure(t *testing.T) {
	client, _ := setupAuditIntegration(t)
	history := NewHistoryRepository(client)
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("append-vs-erasure/existing-%t", existing), func(t *testing.T) {
			e := concurrentAuditEnvelope("erasure-fixture", fmt.Sprintf("concurrent-erasure-%t", existing), 1, constants.ActionCreated)
			record := concurrentAuditRecord(t, e)
			if existing {
				_, err := history.CreateOrVerify(context.Background(), record)
				require.NoError(t, err)
			}
			results := runAuditRace(t, 4, func(ctx context.Context, i int) auditConcurrentResult {
				if i == 0 {
					return auditConcurrentResult{err: history.Erase(ctx, record.Publisher, record.EventID, record.IngestedAt.Add(time.Hour))}
				}
				created, err := history.CreateOrVerify(ctx, record)
				return auditConcurrentResult{created: created, err: err}
			})
			require.NoError(t, results[0].err)
			for _, result := range results[1:] {
				if result.err != nil {
					var erased *contracts.HistoryErasedError
					require.ErrorAs(t, result.err, &erased)
				}
			}
			stored := readAuditSource(t, client, historyIndex, record.Publisher+":"+record.EventID)
			require.Len(t, stored, 4)
			require.Equal(t, true, stored["erased"])
			require.Equal(t, record.Publisher, stored["publisher"])
			require.Equal(t, record.EventID, stored["event_id"])
			require.NotEmpty(t, stored["erased_at"])
			// Every replay after erasure completion must fail, even if an earlier
			// in-flight append verified the original record before erasure won.
			replays := runAuditRace(t, 3, func(ctx context.Context, _ int) auditConcurrentResult {
				created, err := history.CreateOrVerify(ctx, record)
				return auditConcurrentResult{created: created, err: err}
			})
			for _, replay := range replays {
				var erased *contracts.HistoryErasedError
				require.ErrorAs(t, replay.err, &erased)
				require.False(t, replay.created)
			}
			require.Equal(t, stored, readAuditSource(t, client, historyIndex, record.Publisher+":"+record.EventID))
		})
	}

	t.Run("erasure-between-conflict-and-verification", func(t *testing.T) {
		e := concurrentAuditEnvelope("erasure-interleaving", "erasure-interleaving-event", 1, constants.ActionCreated)
		record := concurrentAuditRecord(t, e)
		_, err := history.CreateOrVerify(context.Background(), record)
		require.NoError(t, err)
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		base := http.DefaultTransport.(*http.Transport).Clone()
		defer base.CloseIdleConnections()
		gate := &auditVerificationGate{base: base, path: "/" + historyIndex + "/_doc/test-publisher:" + record.EventID,
			reached: make(chan struct{}), release: release}
		gatedClient, err := opensearch.NewClient(opensearch.Config{
			Addresses: []string{strings.TrimSuffix(os.Getenv("TEST_OPENSEARCH_URL"), "/")}, Transport: gate,
		})
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		done := make(chan auditConcurrentResult, 1)
		go func() {
			created, err := NewHistoryRepositoryWithTimeout(gatedClient, 10*time.Second).CreateOrVerify(ctx, record)
			done <- auditConcurrentResult{created: created, err: err}
		}()
		select {
		case <-gate.reached:
		case <-ctx.Done():
			t.Fatal("redelivery did not reach conflict verification before the deadline")
		}
		// The create-only append has already conflicted, but its verification
		// GET has not reached OpenSearch. Commit erasure in that exact window.
		require.NoError(t, history.Erase(ctx, record.Publisher, record.EventID, record.IngestedAt.Add(time.Hour)))
		unblock()
		select {
		case result := <-done:
			var erased *contracts.HistoryErasedError
			require.ErrorAs(t, result.err, &erased)
			require.False(t, result.created)
		case <-ctx.Done():
			t.Fatal("redelivery verification did not finish before the deadline")
		}
		stored := readAuditSource(t, client, historyIndex, record.Publisher+":"+record.EventID)
		require.Len(t, stored, 4)
		require.Equal(t, true, stored["erased"])
	})
}
