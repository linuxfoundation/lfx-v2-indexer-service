// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func TestOfflineExample(t *testing.T) {
	t.Setenv("AUDIT_V2_EXAMPLE_SEND", "")
	t.Setenv("NATS_URL", "invalid://must-not-connect")
	require.NoError(t, run())
	event, err := buildEvent()
	require.NoError(t, err)
	envelope, err := event.Envelope("Bearer <fresh-service-token>", fixtureSchema)
	require.NoError(t, err)
	payload, err := json.Marshal(envelope)
	require.NoError(t, err)
	_, err = types.DecodeV2Envelope(payload, objectType, fixtureSchema)
	require.NoError(t, err)
}

func TestFixtureSchema(t *testing.T) {
	for _, snapshot := range []string{`{"name":"Fixture"}`, `{"name":"Fixture","secret":"not-approved"}`, `{"name":null}`, `{"other":"Fixture"}`} {
		err := fixtureSchema("test-publisher", objectType, types.AuditContent{Schema: "fixture.audit.v1", After: json.RawMessage(snapshot)})
		if snapshot == `{"name":"Fixture"}` {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	require.Error(t, fixtureSchema("other", objectType, types.AuditContent{Schema: "fixture.audit.v1"}))
}
