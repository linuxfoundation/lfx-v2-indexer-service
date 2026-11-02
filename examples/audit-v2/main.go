// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Command audit-v2 demonstrates offline construction and gated JetStream delivery.
// The current indexer rejects all version 2 envelopes. The fixture schema below
// is test-only, not production schema approval or a delivery enablement flag.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

const objectType = "fixture_resource"

func main() {
	logger := logging.NewLogger()
	if err := run(); err != nil {
		// Do not log errors from transport/configuration: they may contain secrets.
		logger.Error("audit example failed; verify configuration and rollout prerequisites")
		os.Exit(1)
	}
	logger.Info("audit example completed; no indexer-processing acknowledgment is implied")
}

func run() error {
	event, err := buildEvent()
	if err != nil {
		return err
	}
	// Commit these bytes and objectType with the mutation, in one source DB
	// transaction. This example does not implement a transactional outbox.
	outboxJSON, err := json.Marshal(event)
	if err != nil {
		return err
	}
	restored, err := types.RestoreMessageV2(outboxJSON, objectType, fixtureSchema)
	if err != nil {
		return err
	}
	if os.Getenv("AUDIT_V2_EXAMPLE_SEND") != "isolated-test-only" {
		return nil // Default: offline preflight only; no connection or publishing.
	}
	// This explicit opt-in is only for a separately provisioned isolated test
	// broker with a compatible consumer. It cannot enable production ingestion.
	url, authorization := os.Getenv("NATS_URL"), os.Getenv("INDEXER_AUTHORIZATION")
	if url == "" || authorization == "" {
		return fmt.Errorf("isolated broker URL and fresh authorization are required")
	}
	envelope, err := restored.Envelope(authorization, fixtureSchema)
	if err != nil {
		return err
	}
	if err := envelope.ValidateV2(objectType, fixtureSchema); err != nil {
		return err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	conn, err := nats.Connect(url, nats.Timeout(5*time.Second), nats.NoReconnect())
	if err != nil {
		return err
	}
	defer conn.Close()
	js, err := jetstream.New(conn)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ack, err := js.Publish(ctx, constants.IndexPrefix+objectType, payload,
		jetstream.WithMsgID(envelope.Audit.Publisher+":"+envelope.Audit.EventID))
	if err != nil {
		// Unknown outcome: retain outbox data and retry this SAME event.
		return err
	}
	if ack == nil || ack.Stream == "" || ack.Sequence == 0 {
		return fmt.Errorf("broker persistence acknowledgment is required")
	}
	// PubAck confirms broker storage ONLY. Keep the durable repair source for
	// the recovery window; monitor consumer failures/exhausted deliveries.
	// Never mark audit processing complete solely because this call succeeded.
	return nil
}

func buildEvent() (*types.MessageV2, error) {
	name := "Test User 1"
	profile, err := types.NewAuditActorProfile(types.AuditProfileAttributes{Name: &name})
	if err != nil {
		return nil, err
	}
	attribution, err := types.NewAuditAttribution(types.AuditActor{Ref: "user:testuser01", Profile: profile}, nil)
	if err != nil {
		return nil, err
	}
	content, err := types.NewAuditSnapshots("fixture.audit.v1", nil, json.RawMessage(`{"name":"Fixture Resource"}`))
	if err != nil {
		return nil, err
	}
	return types.NewMessageV2(objectType, types.MessageV2Options{
		Action: constants.ActionCreated,
		Data:   map[string]any{"uid": "fixture-resource", "name": "Fixture Resource"},
		IndexingConfig: &types.IndexingConfig{
			ObjectID: "fixture-resource", AccessCheckObject: "fixture_resource:fixture-resource", AccessCheckRelation: "viewer",
			HistoryCheckObject: "project:fixture-project", HistoryCheckRelation: "auditor",
		},
		Audit: types.AuditEvent{
			// These fixed fixture values represent one committed source event.
			// Real publishers allocate once in the mutation transaction, not here
			// in the relay and not again on retries or new process starts.
			EventID: "fixture-event-1", Publisher: "test-publisher",
			OccurredAt: time.Date(2026, 10, 29, 16, 0, 0, 0, time.UTC), ResourceRevision: 42,
			Origin: types.AuditOriginMutation, Attribution: attribution,
			Scope: types.AuditScope{After: []string{"project:fixture-project"}}, Content: content,
		},
	}, fixtureSchema)
}

// fixtureSchema is a CLOSED TEST-ONLY schema: one required name string and no
// other fields. Production must supply its separately approved schema registry.
func fixtureSchema(publisher, resourceType string, content types.AuditContent) error {
	if publisher != "test-publisher" || resourceType != objectType || content.Schema != "fixture.audit.v1" {
		return fmt.Errorf("unregistered schema ownership")
	}
	for _, snapshot := range []json.RawMessage{content.Before, content.After} {
		if len(snapshot) == 0 {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(snapshot, &fields); err != nil || len(fields) != 1 {
			return fmt.Errorf("snapshot requires only the approved name field")
		}
		var name string
		if err := json.Unmarshal(fields["name"], &name); err != nil || name == "" || len(name) > 1024 {
			return fmt.Errorf("snapshot requires a bounded nonempty name string")
		}
	}
	return nil
}
